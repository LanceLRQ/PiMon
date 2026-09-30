package tcpcheck

import (
	"context"
	"errors"
	"net"
	"strconv"
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
	s, ok := runtime.Builtin("tcp-check")
	if !ok {
		t.Fatal("tcp-check 应在 init 中注册")
	}
	return s
}

func listen(t *testing.T) (net.Listener, float64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return ln, float64(n)
}

func collect(t *testing.T, cfg map[string]any, pr *proxy.Proxy) (*report.Report, error) {
	t.Helper()
	return src(t).Collect(context.Background(), runtime.Input{
		Config: cfg, Proxy: pr, Clock: clock.NewFake(time.Unix(1_800_000_000, 0)),
	})
}

func TestManifest(t *testing.T) {
	types := map[string]schema.Type{}
	for _, f := range src(t).Manifest().ConfigSchema {
		types[f.Key] = f.Type
	}
	if types["host"] != schema.TypeString || types["port"] != schema.TypeNumber || types["proxy"] != schema.TypeProxy {
		t.Fatalf("配置字段不符: %v", types)
	}
}

func TestReachableThenClosed(t *testing.T) {
	ln, port := listen(t)
	cfg := map[string]any{"host": "127.0.0.1", "port": port}
	rep, err := collect(t, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusOK || rep.Find("latency") == nil || rep.Find("latency").Value == nil {
		t.Fatalf("监听中应可达并带延迟: %+v", rep)
	}
	if it := rep.Find("status"); it.State != report.StatusOK || it.Text != "open" {
		t.Fatalf("状态项不符: %+v", it)
	}

	_ = ln.Close()
	rep, err = collect(t, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusCritical || rep.Find("latency") != nil {
		t.Fatalf("关闭后应 critical 且无延迟: %+v", rep)
	}
}

func TestViaSocksProxy(t *testing.T) {
	px := proxytest.NewSocks5(t, "", "")
	pr, err := proxy.Parse("socks5://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := collect(t, map[string]any{"host": "127.0.0.1", "port": 9000.0}, pr)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusOK {
		t.Fatalf("经 socks 代理应可达: %+v", rep)
	}
	if seen := px.Seen(); len(seen) != 1 || seen[0].Target != "127.0.0.1:9000" {
		t.Fatalf("代理应看到目标: %+v", seen)
	}
}

func TestHTTPProxyRejected(t *testing.T) {
	px := proxytest.NewHTTP(t)
	pr, err := proxy.Parse("http://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = collect(t, map[string]any{"host": "127.0.0.1", "port": 9000.0}, pr)
	if err == nil || !strings.Contains(err.Error(), "socks") {
		t.Fatalf("http 代理应报清楚的错误: %v", err)
	}
	if len(px.Seen()) != 0 {
		t.Fatal("不得向 http 代理发起任何请求")
	}
}

func TestTimeoutIsCritical(t *testing.T) {
	// 不响应握手的假 socks 服务：接受连接后不读不写，靠超时结束。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		<-done
		_ = c.Close()
	}()
	t.Cleanup(func() { close(done); _ = ln.Close() })
	pr, _ := proxy.Parse("socks5://" + ln.Addr().String())
	rep, err := collect(t, map[string]any{"host": "127.0.0.1", "port": 9000.0, "timeout": "50ms"}, pr)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusCritical || !strings.Contains(rep.Summary, "超时") {
		t.Fatalf("超时应为 critical: %+v", rep)
	}
}

func TestParentContextCancelIsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := src(t).Collect(ctx, runtime.Input{Config: map[string]any{"host": "127.0.0.1", "port": 9.0}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("上层 ctx 取消应返回 ctx 错误: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for name, c := range map[string]map[string]any{
		"缺 host": {"port": 80.0},
		"缺 port": {"host": "x"},
		"端口越界":   {"host": "x", "port": 70000.0},
	} {
		if _, err := collect(t, c, nil); err == nil {
			t.Errorf("%s 应返回错误", name)
		}
	}
}
