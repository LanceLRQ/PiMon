// Package netreach 是内置的网络连通性插件：对一组 HTTP 目标各探测数次，
// 输出每个目标的成功率与延迟中位数，用来判断当前出口（直连或经代理）能否访问外部站点。
//
// 探测拿到任何 HTTP 响应都算成功（连得上即可达，不看状态码）；目标不通是正常的检查结果，
// 只有配置错误或上层 ctx 结束才返回错误。
//
// 数据项：每个目标两项，键成员为目标名称（重名时后者加「 (2)」之类的后缀）：
//   - target[名称]  gauge，单位 %，成功率（成功次数 / 探测次数）
//   - latency[名称] number，单位 ms，成功探测延迟的中位数；全部失败时不输出
package netreach

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/probe"
)

//go:embed plugin.yaml
var manifestYAML []byte

const (
	prefixTarget  = "target"
	prefixLatency = "latency"

	defaultAttempts = 3
	maxAttempts     = 5
	defaultTimeout  = 5 * time.Second
	// minTimeout 是可接受的最小单次超时，更小的值（含配置绕过校验的情形）按默认值处理。
	minTimeout = 100 * time.Millisecond
)

// probeFunc 是一次 HTTP 探测，测试可替换。
type probeFunc func(ctx context.Context, o probe.HTTPOptions) (probe.HTTPResult, error)

type plugin struct {
	m     *manifest.Manifest
	probe probeFunc
}

func mustManifest() *manifest.Manifest {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("net-reach 内置 manifest 不合法: " + err.Error())
	}
	return m
}

func init() {
	runtime.Register(&plugin{m: mustManifest(), probe: probe.HTTP})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

type target struct{ name, url string }

// outcome 是一个目标的探测汇总。
type outcome struct {
	ok        int
	latencies []time.Duration
}

func (p *plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	targets, err := parseTargets(in.Config["targets"])
	if err != nil {
		return nil, err
	}
	attempts := int(cfg.Number(in.Config, "attempts", defaultAttempts))
	if attempts < 1 || attempts > maxAttempts {
		return nil, fmt.Errorf("attempts 必须在 1–%d 之间", maxAttempts)
	}
	timeout := cfg.Duration(in.Config, "timeout", defaultTimeout)
	if timeout < minTimeout {
		timeout = defaultTimeout
	}

	// 目标之间并发，同一目标的多次探测串行；各自写自己的槽位，互不影响。
	results := make([]outcome, len(targets))
	var wg sync.WaitGroup
	for i, tg := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range attempts {
				if ctx.Err() != nil {
					return
				}
				res, err := p.probe(ctx, probe.HTTPOptions{
					URL: tg.url, Method: "GET", Proxy: in.Proxy, Timeout: timeout, FollowRedirects: true, Clock: clk,
				})
				if err == nil {
					results[i].ok++
					results[i].latencies = append(results[i].latencies, res.Latency)
				}
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	rep := &report.Report{Status: report.StatusOK, CollectedAt: clk.Now().UnixMilli()}
	var failed, degraded []string
	for i, tg := range targets {
		r := results[i]
		rate := float64(r.ok) * 100 / float64(attempts)
		lo, hi := 0.0, 100.0
		rep.Items = append(rep.Items, report.Item{
			Key: prefixTarget + "[" + tg.name + "]", Type: report.TypeGauge, Unit: "%", Value: &rate, Min: &lo, Max: &hi,
		})
		switch {
		case r.ok == 0:
			failed = append(failed, tg.name)
		case r.ok < attempts:
			degraded = append(degraded, tg.name)
		}
		if r.ok > 0 {
			ms := float64(median(r.latencies)) / float64(time.Millisecond)
			rep.Items = append(rep.Items, report.Item{
				Key: prefixLatency + "[" + tg.name + "]", Type: report.TypeNumber, Unit: "ms", Value: &ms,
			})
		}
	}
	switch {
	case len(failed) == len(targets):
		rep.Status = report.StatusCritical
		rep.Summary = "全部目标不可达 / All targets unreachable"
	case len(failed) > 0 || len(degraded) > 0:
		rep.Status = report.StatusWarning
		rep.Summary = summarize(failed, degraded)
	}
	return rep, nil
}

func summarize(failed, degraded []string) string {
	var parts []string
	if len(failed) > 0 {
		parts = append(parts, "不可达 / Unreachable: "+strings.Join(failed, ", "))
	}
	if len(degraded) > 0 {
		parts = append(parts, "不稳定 / Unstable: "+strings.Join(degraded, ", "))
	}
	return strings.Join(parts, "；")
}

// median 返回延迟中位数；偶数个时取中间两值的均值。入参非空。
func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// parseTargets 读取并校验目标列表；重名的目标加后缀，保证数据项键唯一。
func parseTargets(raw any) ([]target, error) {
	list, _ := raw.([]any)
	if len(list) == 0 {
		return nil, errors.New("未配置探测目标 targets")
	}
	used := map[string]bool{}
	out := make([]target, 0, len(list))
	for i, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("targets[%d] 不是对象", i)
		}
		name := strings.TrimSpace(cfg.String(m, "name"))
		url := strings.TrimSpace(cfg.String(m, "url"))
		if name == "" || url == "" {
			return nil, fmt.Errorf("targets[%d] 需要名称与地址", i)
		}
		name = uniqueName(keySafe(name), used)
		out = append(out, target{name: name, url: url})
	}
	return out, nil
}

// keySafe 把名称改写成可作为动态键成员的形式：方括号换成圆括号，单独的 * 会被当成通配，
// 改写为「(*)」。
func keySafe(name string) string {
	name = strings.NewReplacer("[", "(", "]", ")").Replace(name)
	if name == "*" {
		return "(*)"
	}
	return name
}

// uniqueName 返回未被占用的名称：冲突时循环递增后缀「 (n)」，直到唯一，并登记。
func uniqueName(name string, used map[string]bool) string {
	cand := name
	for n := 2; used[cand]; n++ {
		cand = fmt.Sprintf("%s (%d)", name, n)
	}
	used[cand] = true
	return cand
}
