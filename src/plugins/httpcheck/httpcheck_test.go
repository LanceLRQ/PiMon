package httpcheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

func src(t *testing.T) runtime.Source {
	t.Helper()
	s, ok := runtime.Builtin("http-check")
	if !ok {
		t.Fatal("http-check 应在 init 中注册")
	}
	return s
}

func collect(t *testing.T, cfg map[string]any, opts ...func(*runtime.Input)) *report.Report {
	t.Helper()
	in := runtime.Input{Config: cfg, Clock: clock.NewFake(time.Unix(1_800_000_000, 0))}
	for _, o := range opts {
		o(&in)
	}
	rep, err := src(t).Collect(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func num(t *testing.T, rep *report.Report, key string) float64 {
	t.Helper()
	it := rep.Find(key)
	if it == nil || it.Value == nil {
		t.Fatalf("缺少数据项 %s: %+v", key, rep.Items)
	}
	return *it.Value
}

func TestManifest(t *testing.T) {
	m := src(t).Manifest()
	types := map[string]schema.Type{}
	for _, f := range m.ConfigSchema {
		types[f.Key] = f.Type
	}
	if types["url"] != schema.TypeURL || types["proxy"] != schema.TypeProxy || types["cert_warn_days"] != schema.TypeNumber {
		t.Fatalf("配置字段不符: %v", types)
	}
	var url schema.Field
	for _, f := range m.ConfigSchema {
		if f.Key == "url" {
			url = f
		}
	}
	if !url.AllowQuery || !url.AllowPublicHTTP {
		t.Fatal("url 应允许查询参数与公网 http")
	}
	if len(m.Outputs) != 4 {
		t.Fatalf("outputs 数量不符: %d", len(m.Outputs))
	}
}

func TestOKHasNoCertDaysForPlainHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) }))
	defer srv.Close()
	rep := collect(t, map[string]any{"url": srv.URL + "/p?token=1"})
	if rep.Status != report.StatusOK || num(t, rep, "code") != 200 {
		t.Fatalf("应为 ok/200: %+v", rep)
	}
	if rep.Find("latency") == nil || rep.Find("cert_days") != nil {
		t.Fatalf("http 应有延迟、无证书天数: %+v", rep.Items)
	}
	if it := rep.Find("status"); it == nil || it.State != report.StatusOK {
		t.Fatalf("状态项应为 ok: %+v", it)
	}
}

func TestKeyword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("service is healthy")) }))
	defer srv.Close()
	if rep := collect(t, map[string]any{"url": srv.URL, "keyword": "healthy"}); rep.Status != report.StatusOK {
		t.Fatalf("关键字命中应为 ok: %+v", rep)
	}
	rep := collect(t, map[string]any{"url": srv.URL, "keyword": "absent"})
	if rep.Status != report.StatusCritical || !strings.Contains(rep.Summary, "关键字") {
		t.Fatalf("关键字未命中应为 critical: %+v", rep)
	}
}

func TestStatusMismatchAndExpectSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	rep := collect(t, map[string]any{"url": srv.URL})
	if rep.Status != report.StatusCritical || num(t, rep, "code") != 503 {
		t.Fatalf("503 默认应为 critical 且记录状态码: %+v", rep)
	}
	if rep := collect(t, map[string]any{"url": srv.URL, "expect_status": "200, 503"}); rep.Status != report.StatusOK {
		t.Fatalf("503 在期望集合内应为 ok: %+v", rep)
	}
	if rep := collect(t, map[string]any{"url": srv.URL, "expect_status": "500-599"}); rep.Status != report.StatusOK {
		t.Fatalf("503 在期望范围内应为 ok: %+v", rep)
	}
}

func TestTimeoutIsCriticalWithoutLatency(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	rep := collect(t, map[string]any{"url": srv.URL, "timeout": "50ms"})
	if rep.Status != report.StatusCritical || !strings.Contains(rep.Summary, "超时") {
		t.Fatalf("超时应为 critical: %+v", rep)
	}
	if rep.Find("latency") != nil || rep.Find("code") != nil {
		t.Fatalf("没拿到响应时不应输出延迟与状态码: %+v", rep.Items)
	}
}

func TestParentContextCancelIsError(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := src(t).Collect(ctx, runtime.Input{Config: map[string]any{"url": srv.URL}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("上层 ctx 到期应返回 ctx 错误: %v", err)
	}
}

func TestCertDays(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	notAfter := srv.Certificate().NotAfter
	at := func(d time.Duration) func(*runtime.Input) {
		return func(in *runtime.Input) { in.Clock = clock.NewFake(notAfter.Add(-d)) }
	}
	base := map[string]any{"url": srv.URL, "skip_tls_verify": true}

	rep := collect(t, base, at(10*24*time.Hour+time.Hour))
	if num(t, rep, "cert_days") != 10 || rep.Status != report.StatusWarning {
		t.Fatalf("剩 10 天且阈值 14 应为 warning: %+v", rep)
	}
	if it := rep.Find("status"); it.State != report.StatusWarning {
		t.Fatalf("状态项应同步为 warning: %+v", it)
	}
	low := map[string]any{"url": srv.URL, "skip_tls_verify": true, "cert_warn_days": 5.0}
	if rep := collect(t, low, at(10*24*time.Hour+time.Hour)); rep.Status != report.StatusOK {
		t.Fatalf("剩 10 天且阈值 5 应为 ok: %+v", rep)
	}
	if rep := collect(t, base, at(-time.Hour)); rep.Status != report.StatusCritical {
		t.Fatalf("证书已过期应为 critical: %+v", rep)
	}
}

func TestSelfSignedFailsWithoutSkip(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	rep := collect(t, map[string]any{"url": srv.URL})
	if rep.Status != report.StatusCritical || rep.Find("code") != nil {
		t.Fatalf("自签名证书未跳过校验应为 critical: %+v", rep)
	}
}

func TestViaHTTPProxy(t *testing.T) {
	px := proxytest.NewHTTP(t)
	pr, err := proxy.Parse("http://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	rep := collect(t, map[string]any{"url": "http://target.invalid:8080/x?a=1", "expect_status": "204"},
		func(in *runtime.Input) { in.Proxy = pr })
	if rep.Status != report.StatusOK || num(t, rep, "code") != 204 {
		t.Fatalf("经代理应得到 204: %+v", rep)
	}
	seen := px.Seen()
	if len(seen) != 1 || !strings.Contains(seen[0].Target, "target.invalid:8080/x") {
		t.Fatalf("代理应看到目标地址: %+v", seen)
	}
}

func TestViaSocksProxy(t *testing.T) {
	px := proxytest.NewSocks5(t, "", "")
	pr, err := proxy.Parse("socks5h://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	rep := collect(t, map[string]any{"url": "http://target.invalid:8080/", "expect_status": "204"},
		func(in *runtime.Input) { in.Proxy = pr })
	if rep.Status != report.StatusOK {
		t.Fatalf("经 socks 代理应 ok: %+v", rep)
	}
	if seen := px.Seen(); len(seen) != 1 || seen[0].Target != "target.invalid:8080" || !seen[0].Domain {
		t.Fatalf("socks5h 应把域名交给代理: %+v", seen)
	}
}

func TestRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", http.StatusFound) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("b")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if rep := collect(t, map[string]any{"url": srv.URL + "/a"}); num(t, rep, "code") != 200 {
		t.Fatalf("默认应跟随重定向: %+v", rep)
	}
	rep := collect(t, map[string]any{"url": srv.URL + "/a", "follow_redirects": false, "expect_status": "302"})
	if num(t, rep, "code") != 302 || rep.Status != report.StatusOK {
		t.Fatalf("不跟随时应得到 302: %+v", rep)
	}
}

func TestErrorDoesNotLeakQuery(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/api?token=query-secret"
	srv.Close()
	rep := collect(t, map[string]any{"url": url})
	if rep.Status != report.StatusCritical || strings.Contains(rep.Summary, "query-secret") {
		t.Fatalf("连接失败为 critical 且不得泄露查询参数: %+v", rep)
	}
}

func TestInvalidConfig(t *testing.T) {
	for name, c := range map[string]map[string]any{
		"缺 url":       {},
		"HEAD 配关键字":   {"url": "http://x", "method": "HEAD", "keyword": "a"},
		"非法 method":   {"url": "http://x", "method": "POST"},
		"非法 expect":   {"url": "http://x", "expect_status": "2xx"},
		"expect 反向范围": {"url": "http://x", "expect_status": "300-200"},
	} {
		if _, err := src(t).Collect(context.Background(), runtime.Input{Config: c}); err == nil {
			t.Errorf("%s 应返回错误", name)
		}
	}
}
