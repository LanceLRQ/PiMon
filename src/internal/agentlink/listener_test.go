package agentlink

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	goodCredential = "good-credential"
	goodDeviceID   = "dev-good"
	goodPairCode   = "PAIRCODE"
)

// stubAuth 是测试用 Authenticator：只认 goodCredential 与 goodPairCode，并统计调用次数。
type stubAuth struct {
	calls atomic.Int64
}

func (a *stubAuth) Authenticate(_ context.Context, m agent.Auth) (string, error) {
	a.calls.Add(1)
	if m.Credential == goodCredential {
		return goodDeviceID, nil
	}
	return "", &AuthError{Reason: agent.ReasonBadCredential}
}

func (a *stubAuth) Pair(_ context.Context, m agent.Pair) (agent.PairOK, error) {
	a.calls.Add(1)
	switch m.Code {
	case goodPairCode:
		return agent.PairOK{
			Credential:   "issued-credential",
			DeviceID:     "dev-paired",
			ListenerInfo: agent.ListenerInfo{Hostname: "pimon", Version: "test"},
		}, nil
	case "EXPIRED":
		return agent.PairOK{}, &AuthError{Reason: agent.ReasonCodeExpired}
	}
	return agent.PairOK{}, &AuthError{Reason: agent.ReasonBadCode}
}

type listenerFixture struct {
	t        *testing.T
	clk      *clock.Fake
	auth     *stubAuth
	addr     string
	cancel   context.CancelFunc
	errc     chan error
	sessions chan *Session
	inbox    *recorder
}

func newListenerFixture(t *testing.T, mutate func(*ListenerConfig)) *listenerFixture {
	t.Helper()
	dir := t.TempDir()
	cert, err := certs.LoadOrCreate(filepath.Join(dir, "hub.crt"), filepath.Join(dir, "hub.key"), time.Now(), certs.Options{CommonName: "PiMon Test"})
	if err != nil {
		t.Fatal(err)
	}
	f := &listenerFixture{
		t:        t,
		clk:      clock.NewFake(t0),
		auth:     &stubAuth{},
		errc:     make(chan error, 1),
		sessions: make(chan *Session, 64),
		inbox:    &recorder{},
	}
	cfg := ListenerConfig{
		Cert:      cert,
		Role:      agent.RoleHub,
		Auth:      f.auth,
		Clock:     f.clk,
		Log:       quietLog(),
		Handler:   func(Peer) Handler { return f.inbox.handle },
		OnSession: func(s *Session) { f.sessions <- s },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	l, err := NewListener(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	go func() { f.errc <- l.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-f.errc:
		case <-time.After(5 * time.Second):
			t.Error("Serve 未在关停后返回")
		}
	})
	return f
}

func testHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 测试客户端不钉扎
	}}
}

// dial 在测试里手写最小拨号：只做 TLS + WebSocket 升级，不发任何帧。
func (f *listenerFixture) dial() (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "wss://"+f.addr+"/agent", &websocket.DialOptions{HTTPClient: testHTTPClient()})
}

func (f *listenerFixture) mustDial() *websocket.Conn {
	f.t.Helper()
	c, _, err := f.dial()
	if err != nil {
		f.t.Fatalf("拨号失败: %v", err)
	}
	f.t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func send(t *testing.T, c *websocket.Conn, m agent.Message) {
	t.Helper()
	b, err := agent.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	sendRaw(t, c, b)
}

func sendRaw(t *testing.T, c *websocket.Conn, b []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
}

// recvMsg 读一帧并解码；连接关闭时返回错误。
func recvMsg(c *websocket.Conn) (agent.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	return agent.Decode(b)
}

func mustRecv(t *testing.T, c *websocket.Conn) agent.Message {
	t.Helper()
	m, err := recvMsg(c)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	return m
}

// expectClosed 断言连接被关闭且没有收到任何业务帧。
func expectClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	m, err := recvMsg(c)
	if err == nil {
		t.Fatalf("期望连接被关闭，却收到 %#v", m)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("连接未被关闭")
	}
}

func expectAuthFail(t *testing.T, c *websocket.Conn, reason string) {
	t.Helper()
	m := mustRecv(t, c)
	af, ok := m.(*agent.AuthFail)
	if !ok || af.Reason != reason {
		t.Fatalf("期望 auth_fail{%s}，得 %#v", reason, m)
	}
	expectClosed(t, c)
}

func (f *listenerFixture) waitSession() *Session {
	f.t.Helper()
	select {
	case s := <-f.sessions:
		return s
	case <-time.After(5 * time.Second):
		f.t.Fatal("OnSession 未被调用")
		return nil
	}
}

func agentAuth(cred string) *agent.Auth {
	return &agent.Auth{Credential: cred, Role: agent.RoleAgent}
}

func TestListener_凭据正确连通并收发业务帧(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, agentAuth(goodCredential))
	ok, isOK := mustRecv(t, c).(*agent.AuthOK)
	if !isOK || ok.DeviceID != goodDeviceID {
		t.Fatalf("期望 auth_ok{%s}，得 %#v", goodDeviceID, ok)
	}
	s := f.waitSession()
	p := s.Peer()
	if p.DeviceID != goodDeviceID || p.Role != agent.RoleAgent {
		t.Fatalf("对端信息 = %+v", p)
	}
	if host, _, err := net.SplitHostPort(p.RemoteAddr); err != nil || host != "127.0.0.1" {
		t.Fatalf("RemoteAddr = %q", p.RemoteAddr)
	}

	send(t, c, &agent.RunNow{RequestID: "r1", InstanceID: "i1"})
	waitFor(t, "Handler 收到 run_now", func() bool { return len(f.inbox.snapshot()) == 1 })
	if rn, ok := f.inbox.snapshot()[0].(*agent.RunNow); !ok || rn.InstanceID != "i1" {
		t.Fatalf("Handler 收到 %#v", f.inbox.snapshot()[0])
	}

	if err := s.Send(&agent.HelloOK{HubVersion: "test", ServerTime: t0}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustRecv(t, c).(*agent.HelloOK); !ok {
		t.Fatal("客户端未收到 hello_ok")
	}
}

func TestListener_凭据错误收到authFail(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, agentAuth("wrong"))
	expectAuthFail(t, c, agent.ReasonBadCredential)
}

func TestListener_配对成功收到pairOK(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, &agent.Pair{Code: goodPairCode, Role: agent.RoleAgent, Name: "nas", Hostname: "nas", OS: "linux", Arch: "amd64"})
	ok, isOK := mustRecv(t, c).(*agent.PairOK)
	if !isOK || ok.Credential != "issued-credential" || ok.DeviceID != "dev-paired" {
		t.Fatalf("期望 pair_ok，得 %#v", ok)
	}
	if p := f.waitSession().Peer(); p.DeviceID != "dev-paired" || p.Role != agent.RoleAgent {
		t.Fatalf("对端信息 = %+v", p)
	}
}

func TestListener_配对码过期收到codeExpired(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, &agent.Pair{Code: "EXPIRED", Role: agent.RoleAgent, Name: "nas", Hostname: "nas", OS: "linux", Arch: "amd64"})
	expectAuthFail(t, c, agent.ReasonCodeExpired)
}

func TestListener_角色相同被拒(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, &agent.Auth{Credential: goodCredential, Role: agent.RoleHub})
	expectAuthFail(t, c, agent.ReasonBadCredential)
	if n := f.auth.calls.Load(); n != 0 {
		t.Fatalf("角色不符不应调用 Authenticator，调用了 %d 次", n)
	}
}

func TestListener_首帧不是认证或配对直接关闭(t *testing.T) {
	cases := map[string][]byte{
		"非法JSON": []byte("{not json"),
		"未知类型":   []byte(`{"type":"what"}`),
		"ping":   []byte(`{"type":"ping","sent_at":"2026-10-02T08:00:00Z"}`),
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			f := newListenerFixture(t, nil)
			c := f.mustDial()
			sendRaw(t, c, frame)
			expectClosed(t, c)
			if n := f.auth.calls.Load(); n != 0 {
				t.Fatalf("不应调用 Authenticator，调用了 %d 次", n)
			}
		})
	}
}

func TestListener_非agent路径返回404(t *testing.T) {
	f := newListenerFixture(t, nil)
	client := testHTTPClient()
	for _, path := range []string{"/", "/other", "/agent"} {
		resp, err := client.Get("https://" + f.addr + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s 状态码 = %d，期望 404", path, resp.StatusCode)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "wss://"+f.addr+"/other", &websocket.DialOptions{HTTPClient: testHTTPClient()})
	if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("升级到 /other 应 404：err=%v resp=%v", err, resp)
	}
}

func TestListener_第17个未认证连接被拒_释放后可再连(t *testing.T) {
	// 测试连接都来自 127.0.0.1，放宽单 IP 上限以单独验证全局上限（跨 IP 的组合见 admit 单元测试）。
	setVar(t, &maxPendingPerIP, MaxPendingHandshakes+1)
	f := newListenerFixture(t, nil)
	conns := make([]*websocket.Conn, 0, MaxPendingHandshakes)
	for i := 0; i < MaxPendingHandshakes; i++ {
		conns = append(conns, f.mustDial())
	}
	c, resp, err := f.dial()
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("第 17 个未认证连接应被拒绝")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，得 resp=%v err=%v", resp, err)
	}

	// 一个未认证连接完成握手后释放名额。
	send(t, conns[0], agentAuth(goodCredential))
	if _, ok := mustRecv(t, conns[0]).(*agent.AuthOK); !ok {
		t.Fatal("期望 auth_ok")
	}
	// 名额在 auth_ok 写出之后才释放，客户端可能先收到 auth_ok，所以轮询重拨。
	var c2 *websocket.Conn
	waitFor(t, "名额释放后重拨成功", func() bool {
		c, _, err := f.dial()
		if err != nil {
			return false
		}
		c2 = c
		return true
	})
	t.Cleanup(func() { _ = c2.CloseNow() })
	send(t, c2, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c2).(*agent.AuthOK); !ok {
		t.Fatal("释放名额后应能连通")
	}
}

func TestListener_10秒不发首帧被断开(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	waitFor(t, "握手计时器登记", func() bool { return f.clk.Waiters() == 1 })
	f.clk.Advance(HandshakeTimeout)
	expectClosed(t, c)
}

func TestListener_握手计时差一点仍可认证(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	waitFor(t, "握手计时器登记", func() bool { return f.clk.Waiters() == 1 })
	f.clk.Advance(HandshakeTimeout - time.Millisecond)
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("期望 auth_ok")
	}
}

func TestListener_首帧超过4KiB被断开(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	big := `{"type":"auth","role":"agent","credential":"` + strings.Repeat("x", agent.MaxPreAuthFrame) + `"}`
	sendRaw(t, c, []byte(big))
	expectClosed(t, c)
	if n := f.auth.calls.Load(); n != 0 {
		t.Fatalf("不应调用 Authenticator，调用了 %d 次", n)
	}
}

func TestListener_同IP失败10次锁定15分钟(t *testing.T) {
	f := newListenerFixture(t, nil)
	for i := 1; i <= 10; i++ {
		c := f.mustDial()
		send(t, c, agentAuth("wrong"))
		expectAuthFail(t, c, agent.ReasonBadCredential)
	}
	calls := f.auth.calls.Load()

	c := f.mustDial()
	send(t, c, agentAuth(goodCredential))
	expectAuthFail(t, c, agent.ReasonRateLimited)
	if n := f.auth.calls.Load(); n != calls {
		t.Fatal("被锁 IP 不应调用 Authenticator")
	}

	f.clk.Advance(15*time.Minute - time.Second)
	c = f.mustDial()
	send(t, c, agentAuth(goodCredential))
	expectAuthFail(t, c, agent.ReasonRateLimited)

	f.clk.Advance(time.Second)
	c = f.mustDial()
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("15 分钟后应解锁")
	}
}

func TestListener_认证成功清零失败计数(t *testing.T) {
	f := newListenerFixture(t, nil)
	fail := func() {
		c := f.mustDial()
		send(t, c, agentAuth("wrong"))
		expectAuthFail(t, c, agent.ReasonBadCredential)
	}
	for i := 0; i < 9; i++ {
		fail()
	}
	c := f.mustDial()
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("期望 auth_ok")
	}
	fail() // 已清零，这次不触发锁定
	c = f.mustDial()
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("成功应清零失败计数")
	}
}

// countLimiter 记录 Fail 次数，验证哪些原因计入失败。
type countLimiter struct {
	mu    sync.Mutex
	fails int
}

func (l *countLimiter) Locked(string) time.Duration { return 0 }
func (l *countLimiter) Fail(string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails++
	return 1
}
func (l *countLimiter) Success(string) {}

func TestListener_bad_code与code_expired计入失败(t *testing.T) {
	lim := &countLimiter{}
	f := newListenerFixture(t, func(c *ListenerConfig) { c.Limiter = lim })
	for _, code := range []string{"WRONG", "EXPIRED"} {
		c := f.mustDial()
		send(t, c, &agent.Pair{Code: code, Role: agent.RoleAgent, Name: "n", Hostname: "n", OS: "linux", Arch: "amd64"})
		m := mustRecv(t, c)
		if _, ok := m.(*agent.AuthFail); !ok {
			t.Fatalf("期望 auth_fail，得 %#v", m)
		}
	}
	lim.mu.Lock()
	defer lim.mu.Unlock()
	if lim.fails != 2 {
		t.Fatalf("Fail 次数 = %d，期望 2", lim.fails)
	}
}

func TestListener_关停时会话收到byeShutdown且Serve返回(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("期望 auth_ok")
	}
	s := f.waitSession()
	pending := f.mustDial() // 一个握手中的连接也应被关闭

	f.cancel()
	bye, ok := mustRecv(t, c).(*agent.Bye)
	if !ok || bye.Reason != agent.ReasonShutdown {
		t.Fatalf("期望 bye{shutdown}，得 %#v", bye)
	}
	select {
	case err := <-f.errc:
		if err != nil {
			t.Fatalf("Serve 返回 %v", err)
		}
		f.errc <- nil // 留给 Cleanup
	case <-time.After(5 * time.Second):
		t.Fatal("Serve 未返回")
	}
	if r := waitDone(t, s); r.Kind != CloseLocal || r.Bye != agent.ReasonShutdown {
		t.Fatalf("会话关闭原因 = %+v", r)
	}
	expectClosed(t, pending)
}

func TestNewListener_校验配置(t *testing.T) {
	dir := t.TempDir()
	cert, err := certs.LoadOrCreate(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"), time.Now(), certs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]ListenerConfig{
		"缺Authenticator": {Cert: cert, Role: agent.RoleHub},
		"角色非法":           {Cert: cert, Role: "x", Auth: &stubAuth{}},
		"缺证书":            {Role: agent.RoleHub, Auth: &stubAuth{}},
	}
	for name, cfg := range cases {
		if _, err := NewListener(cfg); err == nil {
			t.Errorf("%s：应返回错误", name)
		}
	}
}

// setVar 在测试期间改写包内变量，结束时还原（全仓不用 t.Parallel，无并发改写）。
func setVar[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// rawTLS 建立不走 net/http 客户端的 TLS 连接，便于观察服务端何时关闭连接。
func (f *listenerFixture) rawTLS() *tls.Conn {
	f.t.Helper()
	c, err := tls.Dial("tcp", f.addr, &tls.Config{InsecureSkipVerify: true}) // 测试客户端不钉扎
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = c.Close() })
	return c
}

// expectEOF 断言服务端在 within 内关闭连接（读到 EOF 或连接错误，而不是读超时）。
func expectEOF(t *testing.T, c net.Conn, within time.Duration) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 1)
	_, err := c.Read(buf)
	if err == nil {
		t.Fatal("期望连接被关闭，却读到数据")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("连接在 %v 内未被关闭", within)
	}
}

func TestListener_404与503响应后服务端关闭连接(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.rawTLS()
	if _, err := c.Write([]byte("GET /other HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !resp.Close {
		t.Fatalf("状态码 = %d，Connection: close = %v", resp.StatusCode, resp.Close)
	}
	expectEOF(t, c, 5*time.Second)

	// 503：占满同一 IP 的名额后再升级。
	for i := 0; i < maxPendingPerIP; i++ {
		f.mustDial()
	}
	_, resp, err = f.dial()
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable || !resp.Close {
		t.Fatalf("期望 503 + Connection: close：resp=%v err=%v", resp, err)
	}
}

func TestListener_空闲连接按HTTP超时关闭(t *testing.T) {
	setVar(t, &httpTimeout, 200*time.Millisecond)
	f := newListenerFixture(t, nil)
	c := f.rawTLS() // 握手完成后不发请求
	expectEOF(t, c, 5*time.Second)
}

func TestListener_升级后的会话不受HTTP超时影响(t *testing.T) {
	setVar(t, &httpTimeout, 200*time.Millisecond)
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("期望 auth_ok")
	}
	s := f.waitSession()
	// 真实时间越过 HTTP 读写与空闲超时（这里验证的是网络截止时间，只能用真实时间）。
	time.Sleep(5 * httpTimeout)
	send(t, c, &agent.RunNow{RequestID: "r1", InstanceID: "i1"})
	waitFor(t, "Handler 收到 run_now", func() bool { return len(f.inbox.snapshot()) == 1 })
	if err := s.Send(&agent.HelloOK{HubVersion: "test", ServerTime: t0}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustRecv(t, c).(*agent.HelloOK); !ok {
		t.Fatal("客户端未收到 hello_ok")
	}
}

func TestListener_同一IP第5个未认证连接被拒(t *testing.T) {
	f := newListenerFixture(t, nil)
	conns := make([]*websocket.Conn, 0, maxPendingPerIP)
	for i := 0; i < maxPendingPerIP; i++ {
		conns = append(conns, f.mustDial())
	}
	c, resp, err := f.dial()
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("同一 IP 第 5 个未认证连接应被拒绝")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，得 resp=%v err=%v", resp, err)
	}
	// 握手失败（超时、非法首帧）不计失败次数，但会释放名额。
	sendRaw(t, conns[0], []byte("{not json"))
	expectClosed(t, conns[0])
	waitFor(t, "名额释放后重拨成功", func() bool {
		c, _, err := f.dial()
		if err != nil {
			return false
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		return true
	})
}

func TestListener_admit单IP与全局上限(t *testing.T) {
	l, err := NewListener(ListenerConfig{Cert: tls.Certificate{Certificate: [][]byte{{1}}}, Role: agent.RoleHub, Auth: &stubAuth{}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPendingPerIP; i++ {
		if !l.admit("a") {
			t.Fatalf("a 第 %d 个应放行", i+1)
		}
	}
	if l.admit("a") {
		t.Fatal("a 第 5 个应拒绝")
	}
	if !l.admit("b") {
		t.Fatal("其他 IP 不受影响")
	}
	l.leave("a")
	if !l.admit("a") {
		t.Fatal("释放后 a 应可再放行")
	}
	// 用多个 IP 凑满全局上限。
	total := maxPendingPerIP + 1
	for i := 0; total < MaxPendingHandshakes; i++ {
		if !l.admit(fmt.Sprintf("ip-%d", i)) {
			t.Fatalf("全局未满时应放行（已有 %d）", total)
		}
		total++
	}
	if l.admit("fresh") {
		t.Fatal("全局满 16 后应拒绝任何 IP")
	}
	l.leave("b")
	if !l.admit("fresh") {
		t.Fatal("全局释放后应放行")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pendingTotal != MaxPendingHandshakes || l.pendingByIP["b"] != 0 {
		t.Fatalf("计数 total=%d b=%d", l.pendingTotal, l.pendingByIP["b"])
	}
	if _, ok := l.pendingByIP["b"]; ok {
		t.Fatal("计数归零的 IP 应从表中删除")
	}
}

func TestSourceKey_IPv6归一化到64位前缀(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:5":                "1.2.3.4",
		"[::ffff:1.2.3.4]:5":       "1.2.3.4",
		"[2001:db8:1:2:aaaa::1]:5": "2001:db8:1:2::/64",
		"[2001:db8:1:2:bbbb::9]:6": "2001:db8:1:2::/64",
		"[fe80::1%en0]:5":          "fe80::/64",
		"[2001:db8:1:3::1]:5":      "2001:db8:1:3::/64",
		"not-an-addr":              "not-an-addr",
	}
	for in, want := range cases {
		if got := sourceKey(in); got != want {
			t.Errorf("sourceKey(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestListener_认证后可收超过4KiB的业务帧(t *testing.T) {
	f := newListenerFixture(t, nil)
	c := f.mustDial()
	send(t, c, agentAuth(goodCredential))
	if _, ok := mustRecv(t, c).(*agent.AuthOK); !ok {
		t.Fatal("期望 auth_ok")
	}
	f.waitSession()
	big := strings.Repeat("x", 64*1024)
	send(t, c, &agent.RunNow{RequestID: "r1", InstanceID: big})
	waitFor(t, "Handler 收到大帧", func() bool { return len(f.inbox.snapshot()) == 1 })
	if rn, ok := f.inbox.snapshot()[0].(*agent.RunNow); !ok || rn.InstanceID != big {
		t.Fatal("Handler 收到的大帧内容不符")
	}
}
