// Package httpcheck 是内置的 HTTP 检测插件：按方法请求一个地址，检查状态码、响应关键字，
// 输出延迟，https 时附带证书剩余天数。
//
// 目标站点“不通”（连接失败、超时、状态码不符、关键字缺失）是正常的检查结果，
// 以 critical 状态的报告呈现；只有配置本身不合法时 Collect 才返回错误。
package httpcheck

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
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

// 数据项键名。
const (
	keyStatus   = "status"
	keyCode     = "code"
	keyLatency  = "latency"
	keyCertDays = "cert_days"
)

const (
	defaultTimeout  = 10 * time.Second
	defaultExpect   = "200-299"
	defaultCertWarn = 14
	day             = 24 * time.Hour
)

type plugin struct{ m *manifest.Manifest }

func init() {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("http-check 内置 manifest 不合法: " + err.Error())
	}
	runtime.Register(&plugin{m: m})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

func (p *plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	raw := cfg.String(in.Config, "url")
	if raw == "" {
		return nil, errors.New("未配置 url")
	}
	method := cfg.String(in.Config, "method")
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return nil, errors.New("method 只支持 GET 与 HEAD")
	}
	keyword := cfg.String(in.Config, "keyword")
	if keyword != "" && method == http.MethodHead {
		return nil, errors.New("HEAD 没有响应体，无法检查关键字，请改用 GET")
	}
	expectRaw := cfg.String(in.Config, "expect_status")
	if expectRaw == "" {
		expectRaw = defaultExpect
	}
	expect, err := parseStatusSet(expectRaw)
	if err != nil {
		return nil, fmt.Errorf("expect_status 不合法: %w", err)
	}

	now := clk.Now()
	rep := &report.Report{Status: report.StatusOK, CollectedAt: now.UnixMilli()}

	res, err := probe.HTTP(ctx, probe.HTTPOptions{
		URL:             raw,
		Method:          method,
		Proxy:           in.Proxy,
		Timeout:         cfg.Duration(in.Config, "timeout", defaultTimeout),
		FollowRedirects: cfg.Bool(in.Config, "follow_redirects", true),
		SkipTLSVerify:   cfg.Bool(in.Config, "skip_tls_verify", false),
		Keyword:         keyword,
		Clock:           clk,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 没拿到响应：延迟、状态码、证书都是未知，不输出。
		rep.Status = report.StatusCritical
		rep.Summary = "无法访问 / Unreachable: " + err.Error()
		rep.Items = []report.Item{stateItem(report.StatusCritical, "down")}
		return rep, nil
	}

	failure := ""
	switch {
	case !expect(res.Code):
		failure = fmt.Sprintf("状态码 %d 不符合预期 / Unexpected status %d", res.Code, res.Code)
	case keyword != "" && !res.KeywordFound:
		failure = "响应中没有找到关键字 / Keyword not found"
	}
	if failure != "" {
		rep.Status = report.StatusCritical
		rep.Summary = failure
		rep.Items = append(rep.Items, stateItem(report.StatusCritical, "fail"))
	} else {
		rep.Items = append(rep.Items, stateItem(report.StatusOK, "ok"))
	}
	rep.Items = append(rep.Items,
		report.Item{Key: keyCode, Type: report.TypeNumber, Value: ptr(float64(res.Code))},
		report.Item{Key: keyLatency, Type: report.TypeNumber, Unit: "ms", Value: ptr(float64(res.Latency) / float64(time.Millisecond))},
	)
	if !res.CertNotAfter.IsZero() {
		p.certDays(rep, res.CertNotAfter.Sub(now), cfg.Number(in.Config, "cert_warn_days", defaultCertWarn))
	}
	return rep, nil
}

// certDays 追加证书剩余天数，并按预警阈值抬高状态；已过期为 critical。
// 已经是 critical（请求本身失败）时保持原摘要。
func (p *plugin) certDays(rep *report.Report, left time.Duration, warnDays float64) {
	days := math.Floor(left.Hours() / 24)
	rep.Items = append(rep.Items, report.Item{Key: keyCertDays, Type: report.TypeNumber, Unit: "d", Value: ptr(days)})
	if rep.Status == report.StatusCritical {
		return
	}
	switch {
	case left <= 0:
		setStatus(rep, report.StatusCritical, "certificate expired", "证书已过期 / Certificate expired")
	case days < warnDays:
		setStatus(rep, report.StatusWarning, "cert expiring",
			fmt.Sprintf("证书将在 %d 天后过期 / Certificate expires in %d days", int(days), int(days)))
	}
}

// setStatus 同时更新整体状态、摘要与状态项。
func setStatus(rep *report.Report, st report.Status, text, summary string) {
	rep.Status, rep.Summary = st, summary
	if it := rep.Find(keyStatus); it != nil {
		it.State, it.Text = st, text
	}
}

func stateItem(st report.Status, text string) report.Item {
	return report.Item{Key: keyStatus, Type: report.TypeState, State: st, Text: text}
}

// parseStatusSet 解析「200,204,300-399」形式的状态码集合。
func parseStatusSet(s string) (func(int) bool, error) {
	type span struct{ lo, hi int }
	var spans []span
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("存在空项")
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil || a < 100 || a > 599 {
			return nil, fmt.Errorf("%q 不是合法状态码", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil || b < a || b > 599 {
				return nil, fmt.Errorf("%q 不是合法范围", part)
			}
		}
		spans = append(spans, span{a, b})
	}
	return func(code int) bool {
		for _, sp := range spans {
			if code >= sp.lo && code <= sp.hi {
				return true
			}
		}
		return false
	}, nil
}

func ptr[T any](v T) *T { return &v }
