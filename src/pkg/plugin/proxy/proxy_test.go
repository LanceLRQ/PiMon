package proxy_test

import (
	"bufio"
	"context"
	"io"
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

// echoServer 启动本地 TCP 回显服务；连接建立后先发 hello，随后原样回显。
func echoServer(t *testing.T, hello string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				if hello != "" {
					_, _ = c.Write([]byte(hello))
				}
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Fatalf("回显不一致: %q", buf)
	}
}

func connectProxies(t *testing.T, user, pass string, creds string) map[string]*proxy.Proxy {
	t.Helper()
	hpx := proxytest.NewHTTPConnect(t, user, pass)
	spx := proxytest.NewHTTPSConnect(t, user, pass)
	hp, err := proxy.Parse("http://" + creds + hpx.Addr)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := proxy.Parse("https://" + creds + spx.Addr)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]*proxy.Proxy{"http": hp, "https": proxy.WithRootCAs(sp, spx.CertPool)}
}

func TestDialContext_经HTTP与HTTPS代理CONNECT双向收发(t *testing.T) {
	target := echoServer(t, "")
	for name, p := range connectProxies(t, "", "", "") {
		conn, err := p.DialContext()(context.Background(), "tcp", target)
		if err != nil {
			t.Fatalf("%s 代理 CONNECT 失败: %v", name, err)
		}
		roundTrip(t, conn, "ping-"+name)
		roundTrip(t, conn, "again")
		_ = conn.Close()
	}
}

func TestDialContext_代理认证(t *testing.T) {
	target := echoServer(t, "")
	good := connectProxies(t, "u@x", "p:w", "u%40x:p%3Aw@")
	for name, p := range good {
		conn, err := p.DialContext()(context.Background(), "tcp", target)
		if err != nil {
			t.Fatalf("%s 认证正确应成功: %v", name, err)
		}
		roundTrip(t, conn, "hi")
		_ = conn.Close()
	}
	bad := connectProxies(t, "u", "secret-pw", "u:wrong-pw@")
	for name, p := range bad {
		conn, err := p.DialContext()(context.Background(), "tcp", target)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("%s 认证错误应失败", name)
		}
		if !strings.Contains(err.Error(), "407") {
			t.Errorf("%s 错误应带状态行: %v", name, err)
		}
		if strings.Contains(err.Error(), "wrong-pw") || strings.Contains(err.Error(), "secret-pw") {
			t.Errorf("%s 错误泄露密码: %v", name, err)
		}
	}
}

func TestDialContext_代理返回403报错且不含密码(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = http.ReadRequest(bufio.NewReader(c))
		_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	}()
	p, err := proxy.Parse("http://user:topsecret@" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := p.DialContext()(context.Background(), "tcp", "127.0.0.1:9")
	if err == nil {
		_ = conn.Close()
		t.Fatal("403 应报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") {
		t.Errorf("错误应带状态行: %v", err)
	}
	for _, bad := range []string{"topsecret", "user", ln.Addr().String()} {
		if strings.Contains(msg, bad) {
			t.Errorf("错误文本不应含 %q: %v", bad, err)
		}
	}
}

func TestDialContext_CONNECT应答后紧跟的隧道字节不丢(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = http.ReadRequest(bufio.NewReader(c))
		// 应答与隧道首包同一次写出，客户端读应答时必然多读到隧道字节。
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\nSERVER-HELLO")
		_, _ = io.Copy(c, c)
	}()
	p, err := proxy.Parse("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := p.DialContext()(context.Background(), "tcp", "127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len("SERVER-HELLO"))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "SERVER-HELLO" {
		t.Fatalf("应先吐出缓冲字节: %q %v", buf, err)
	}
	roundTrip(t, conn, "next")
}

func TestDialContext_握手阶段按ctx截止超时(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		if c, err := ln.Accept(); err == nil {
			defer func() { _ = c.Close() }()
			_, _ = io.Copy(io.Discard, c) // 只读不应答
		}
	}()
	p, err := proxy.Parse("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, err := p.DialContext()(ctx, "tcp", "127.0.0.1:9")
	if err == nil {
		_ = conn.Close()
		t.Fatal("代理不应答应超时")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("超时过久: %v", time.Since(start))
	}
}

func TestDialContext_HTTPS代理证书不受信时失败(t *testing.T) {
	spx := proxytest.NewHTTPSConnect(t, "", "")
	p, err := proxy.Parse("https://" + spx.Addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := p.DialContext()(context.Background(), "tcp", "127.0.0.1:9")
	if err == nil {
		_ = conn.Close()
		t.Fatal("未注入根证书应校验失败")
	}
}

func TestDialContext_经Socks5Forward双向收发(t *testing.T) {
	target := echoServer(t, "")
	px := proxytest.NewSocks5Forward(t, "", "")
	p, err := proxy.Parse("socks5://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := p.DialContext()(context.Background(), "tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	roundTrip(t, conn, "via-socks")
}

func TestDialContext_CONNECT应答为HTTP10或Connection关闭时隧道可用(t *testing.T) {
	for _, line := range []string{
		"HTTP/1.0 200 Connection established\r\n\r\n",
		"HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n",
	} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			_, _ = http.ReadRequest(bufio.NewReader(c))
			_, _ = io.WriteString(c, line)
			_, _ = io.Copy(c, c)
		}()
		p, err := proxy.Parse("http://" + ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conn, err := p.DialContext()(context.Background(), "tcp", "127.0.0.1:9")
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		roundTrip(t, conn, "tunnel")
		_ = conn.Close()
		_ = ln.Close()
	}
}
