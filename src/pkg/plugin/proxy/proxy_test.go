package proxy_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
)

func get(t *testing.T, tr *http.Transport, target string) int {
	t.Helper()
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := c.Get(target)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestParseInvalid(t *testing.T) {
	for _, raw := range []string{
		"ftp://1.2.3.4:21", "http://", "http://host", "http://host:0", "http://host:99999",
		"socks4://h:1", "://x", "http://h:1/path", "not a url",
	} {
		if _, err := proxy.Parse(raw); err == nil {
			t.Errorf("Parse(%q) 应失败", raw)
		}
	}
}

func TestDirect(t *testing.T) {
	for _, raw := range []string{"", "direct"} {
		p, err := proxy.Parse(raw)
		if err != nil || !p.IsDirect() {
			t.Fatalf("Parse(%q) = %v, %v，应为直连", raw, p, err)
		}
	}
	var nilP *proxy.Proxy
	if !nilP.IsDirect() || nilP.URL() != "" || nilP.Redacted() != "" {
		t.Error("nil 代理应表现为直连")
	}
	if !proxy.Direct().IsDirect() {
		t.Error("Direct() 应为直连")
	}
	tr := nilP.Transport()
	if tr == nil || tr.Proxy != nil {
		t.Error("直连 Transport 不应设置 Proxy")
	}
	env := strings.Join(nilP.Env(), "\n")
	if !strings.Contains(env, "NO_PROXY=*") {
		t.Errorf("直连应置 NO_PROXY=*，得到 %q", env)
	}
	if strings.Contains(env, "HTTP_PROXY=http") || strings.Contains(env, "ALL_PROXY=socks") {
		t.Errorf("直连不应给出代理地址: %q", env)
	}
}

func TestHTTPProxyTransport(t *testing.T) {
	fake := proxytest.NewHTTP(t)
	p, err := proxy.Parse("http://alice:s3cret@" + fake.Addr)
	if err != nil {
		t.Fatal(err)
	}
	if code := get(t, p.Transport(), "http://example.invalid/generate_204"); code != 204 {
		t.Fatalf("状态码 = %d", code)
	}
	seen := fake.Seen()
	if len(seen) != 1 || seen[0].Target != "http://example.invalid/generate_204" {
		t.Fatalf("代理未收到请求: %+v", seen)
	}
	if seen[0].User != "alice" || seen[0].Password != "s3cret" {
		t.Errorf("认证信息 = %+v", seen[0])
	}
}

func TestSocks5ResolvesLocally(t *testing.T) {
	fake := proxytest.NewSocks5(t, "", "")
	p, err := proxy.Parse("socks5://" + fake.Addr)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(fake.Addr)
	if code := get(t, p.Transport(), "http://localhost:"+port+"/x"); code != 204 {
		t.Fatalf("状态码 = %d", code)
	}
	seen := fake.Seen()
	if len(seen) != 1 || seen[0].Domain {
		t.Fatalf("socks5 应本地解析后把 IP 交给代理: %+v", seen)
	}
	host, _, _ := net.SplitHostPort(seen[0].Target)
	if net.ParseIP(host) == nil {
		t.Errorf("目标应为 IP: %q", seen[0].Target)
	}
}

func TestSocks5hSendsDomain(t *testing.T) {
	fake := proxytest.NewSocks5(t, "bob", "pw")
	p, err := proxy.Parse("socks5h://bob:pw@" + fake.Addr)
	if err != nil {
		t.Fatal(err)
	}
	if code := get(t, p.Transport(), "http://remote.example.invalid:8080/x"); code != 204 {
		t.Fatalf("状态码 = %d", code)
	}
	seen := fake.Seen()
	if len(seen) != 1 || !seen[0].Domain || seen[0].Target != "remote.example.invalid:8080" {
		t.Fatalf("socks5h 应把域名交给代理: %+v", seen)
	}
	if seen[0].User != "bob" || seen[0].Password != "pw" {
		t.Errorf("认证 = %+v", seen[0])
	}
}

func TestDialContext(t *testing.T) {
	fake := proxytest.NewSocks5(t, "", "")
	p, _ := proxy.Parse("socks5h://" + fake.Addr)
	conn, err := p.DialContext()(context.Background(), "tcp", "tcp-target.example.invalid:9")
	if err != nil {
		t.Fatalf("经代理拨号失败: %v", err)
	}
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)
	// 拨号后对端在读 HTTP 请求，这里只关心代理侧收到域名。
	deadline := time.Now().Add(2 * time.Second)
	for len(fake.Seen()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := fake.Seen(); len(s) != 1 || s[0].Target != "tcp-target.example.invalid:9" || !s[0].Domain {
		t.Fatalf("seen = %+v", s)
	}
	// 直连的 DialContext 可用
	var nilP *proxy.Proxy
	if nilP.DialContext() == nil {
		t.Error("直连 DialContext 不应为 nil")
	}
}

func TestEnvAndRedaction(t *testing.T) {
	p, err := proxy.Parse("socks5h://u%40x:p%3Aw@10.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(p.URL())
	if pw, _ := u.User.Password(); u.User.Username() != "u@x" || pw != "p:w" || u.Scheme != "socks5h" || u.Host != "10.0.0.1:1080" {
		t.Errorf("URL 往返错误: %s", p.URL())
	}
	if strings.Contains(p.Redacted(), "p%3Aw") || strings.Contains(p.Redacted(), "p:w") {
		t.Errorf("Redacted 泄露密码: %s", p.Redacted())
	}
	env := strings.Join(p.Env(), "\n")
	for _, k := range []string{"ALL_PROXY=", "all_proxy=", "HTTPS_PROXY=", "https_proxy=", "HTTP_PROXY=", "http_proxy="} {
		if !strings.Contains(env, k+p.URL()) {
			t.Errorf("Env 缺少 %s: %q", k, env)
		}
	}
	if strings.Contains(env, "NO_PROXY=*") {
		t.Error("走代理时不应置 NO_PROXY=*")
	}
}

func TestDialContextRejectsHTTPProxies(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			_ = c.Close()
		}
	}()
	for _, scheme := range []string{"http", "https"} {
		p, err := proxy.Parse(scheme + "://" + ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conn, err := p.DialContext()(context.Background(), "tcp", ln.Addr().String())
		if conn != nil {
			_ = conn.Close()
		}
		if !errors.Is(err, proxy.ErrRawTCPUnsupported) {
			t.Errorf("%s 代理拨号应返回 ErrRawTCPUnsupported，得到 %v", scheme, err)
		}
	}
	select {
	case <-accepted:
		t.Error("拒绝时不应发起任何连接")
	case <-time.After(200 * time.Millisecond):
	}
}
