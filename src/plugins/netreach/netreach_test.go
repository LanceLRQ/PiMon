package netreach

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/probe"
)

func src(t *testing.T) runtime.Source {
	t.Helper()
	s, ok := runtime.Builtin("net-reach")
	if !ok {
		t.Fatal("net-reach 应在 init 中注册")
	}
	return s
}

func targets(pairs ...string) []any {
	var out []any
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, map[string]any{"name": pairs[i], "url": pairs[i+1]})
	}
	return out
}

func collect(t *testing.T, s runtime.Source, cfg map[string]any, pr *proxy.Proxy) (*report.Report, error) {
	t.Helper()
	return s.Collect(context.Background(), runtime.Input{Config: cfg, Proxy: pr, Clock: clock.NewFake(time.Unix(1_800_000_000, 0))})
}

func value(t *testing.T, rep *report.Report, key string) float64 {
	t.Helper()
	it := rep.Find(key)
	if it == nil || it.Value == nil {
		t.Fatalf("缺少数据项 %s: %+v", key, rep.Items)
	}
	return *it.Value
}

func closedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

func TestSuccessRateAcrossTargets(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(good.Close)
	// 5xx 也算拿到了响应，属于可达。
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(bad.Close)
	// 第 1 次请求直接断开连接，之后正常：3 次探测成功 2 次。
	var n atomic.Int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			c, _, _ := w.(http.Hijacker).Hijack()
			_ = c.Close()
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(flaky.Close)

	rep, err := collect(t, src(t), map[string]any{
		"targets":  targets("good", good.URL, "bad", bad.URL, "flaky", flaky.URL, "down", closedURL(t)),
		"attempts": 3.0,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := value(t, rep, "target[good]"); v != 100 {
		t.Errorf("good 应 100: %v", v)
	}
	if v := value(t, rep, "target[bad]"); v != 100 {
		t.Errorf("5xx 也应算可达: %v", v)
	}
	if v := value(t, rep, "target[flaky]"); v < 66.6 || v > 66.7 {
		t.Errorf("flaky 应约 66.7: %v", v)
	}
	if v := value(t, rep, "target[down]"); v != 0 {
		t.Errorf("down 应 0: %v", v)
	}
	if rep.Find("latency[down]") != nil {
		t.Error("全失败的目标不应输出延迟")
	}
	if rep.Find("latency[good]") == nil || rep.Find("latency[flaky]") == nil {
		t.Error("有成功探测的目标应输出延迟")
	}
	if rep.Status != report.StatusWarning {
		t.Errorf("部分目标失败应为 warning: %s", rep.Status)
	}
	assertValid(t, src(t), rep)
}

func TestAllTargetsDownIsCritical(t *testing.T) {
	rep, err := collect(t, src(t), map[string]any{"targets": targets("a", closedURL(t), "b", closedURL(t)), "attempts": 1.0, "timeout": "1s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusCritical {
		t.Fatalf("全部不通应 critical: %s", rep.Status)
	}
	assertValid(t, src(t), rep)
}

func TestAllUpIsOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(ts.Close)
	rep, err := collect(t, src(t), map[string]any{"targets": targets("a", ts.URL)}, nil)
	if err != nil || rep.Status != report.StatusOK {
		t.Fatalf("应 ok: %+v %v", rep, err)
	}
}

func TestMedianLatency(t *testing.T) {
	seq := map[string][]time.Duration{
		"odd":  {30 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond},
		"even": {10 * time.Millisecond, 40 * time.Millisecond},
	}
	var idx = map[string]*atomic.Int32{"odd": {}, "even": {}}
	p := &plugin{m: mustManifest(), probe: func(_ context.Context, o probe.HTTPOptions) (probe.HTTPResult, error) {
		name := o.URL[len("http://"):]
		i := int(idx[name].Add(1)) - 1
		if i >= len(seq[name]) {
			return probe.HTTPResult{}, errors.New("失败")
		}
		return probe.HTTPResult{Code: 200, Latency: seq[name][i]}, nil
	}}
	rep, err := collect(t, p, map[string]any{"targets": targets("odd", "http://odd", "even", "http://even"), "attempts": 3.0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := value(t, rep, "latency[odd]"); v != 20 {
		t.Errorf("奇数个取中间值 20: %v", v)
	}
	// even 成功 2 次、失败 1 次：中位数 (10+40)/2 = 25。
	if v := value(t, rep, "latency[even]"); v != 25 {
		t.Errorf("偶数个取中间两值均值 25: %v", v)
	}
	if v := value(t, rep, "target[even]"); v < 66.6 || v > 66.7 {
		t.Errorf("even 成功率约 66.7: %v", v)
	}
}

func TestViaHTTPProxy(t *testing.T) {
	px := proxytest.NewHTTP(t)
	pr, err := proxy.Parse("http://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := collect(t, src(t), map[string]any{"targets": targets("a", "http://example.invalid/x"), "attempts": 2.0}, pr)
	if err != nil {
		t.Fatal(err)
	}
	if len(px.Seen()) != 2 {
		t.Fatalf("探测应经过代理 2 次: %+v", px.Seen())
	}
	if rep.Find("target[a]") == nil {
		t.Fatalf("应有数据项: %+v", rep)
	}
}

func TestViaSocksProxy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(ts.Close)
	px := proxytest.NewSocks5(t, "", "")
	pr, _ := proxy.Parse("socks5://" + px.Addr)
	rep, err := collect(t, src(t), map[string]any{"targets": targets("a", ts.URL), "attempts": 2.0}, pr)
	if err != nil {
		t.Fatal(err)
	}
	if value(t, rep, "target[a]") != 100 || len(px.Seen()) != 2 {
		t.Fatalf("经 socks 代理应成功且被代理看到: %+v %+v", rep.Items, px.Seen())
	}
}

func TestDuplicateNamesGetSuffix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(ts.Close)
	rep, err := collect(t, src(t), map[string]any{"targets": targets("x", ts.URL, "x", ts.URL), "attempts": 1.0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Find("target[x]") == nil || rep.Find("target[x (2)]") == nil {
		t.Fatalf("重名应加后缀: %+v", rep.Items)
	}
	assertValid(t, src(t), rep)
}

func TestParentContextCancelIsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := src(t).Collect(ctx, runtime.Input{Config: map[string]any{"targets": targets("a", "http://127.0.0.1:1")}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("上层 ctx 取消应返回 ctx 错误: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for name, c := range map[string]map[string]any{
		"无目标":    {},
		"空目标":    {"targets": []any{}},
		"缺名称":    {"targets": targets("", "http://a")},
		"缺地址":    {"targets": targets("a", "")},
		"次数过多":   {"targets": targets("a", "http://a"), "attempts": 6.0},
		"次数为 0":  {"targets": targets("a", "http://a"), "attempts": 0.0},
		"目标不是对象": {"targets": []any{"x"}},
	} {
		if _, err := collect(t, src(t), c, nil); err == nil {
			t.Errorf("%s 应返回错误", name)
		}
	}
}

func TestManifestDefaultsAndBounds(t *testing.T) {
	m := src(t).Manifest()
	var maxAttempts, maxTimeout float64
	var names []string
	for _, f := range m.ConfigSchema {
		switch f.Key {
		case "targets":
			list, _ := f.Default.([]any)
			for _, it := range list {
				names = append(names, it.(map[string]any)["name"].(string))
			}
		case "attempts":
			maxAttempts = *f.Max
		case "timeout":
			maxTimeout = *f.Max
		}
	}
	if len(names) != 4 || names[0] != "Google" || names[1] != "GitHub" || names[2] != "Cloudflare" || names[3] != "百度" {
		t.Fatalf("预设目标不符: %v", names)
	}
	if maxAttempts != 5 {
		t.Fatalf("探测次数上限应为 5: %v", maxAttempts)
	}
	if maxAttempts*maxTimeout >= m.Timeout.Seconds() {
		t.Fatalf("最坏耗时 %v 秒必须小于运行超时 %v", maxAttempts*maxTimeout, m.Timeout)
	}
}

func assertValid(t *testing.T, s runtime.Source, rep *report.Report) {
	t.Helper()
	if err := rep.Validate(nil); err != nil {
		t.Fatalf("报告应通过 Validate: %v", err)
	}
	for _, it := range rep.Items {
		ok := false
		for _, o := range s.Manifest().Outputs {
			k, err := report.ParseKey(o.Key)
			if err != nil {
				t.Fatal(err)
			}
			if k.Matches(it.Key) || (!k.Dynamic && o.Key == it.Key) {
				ok = o.Type == it.Type
				break
			}
		}
		if !ok {
			t.Errorf("键 %s（%s）不在 manifest outputs 内或类型不符", it.Key, it.Type)
		}
	}
}
