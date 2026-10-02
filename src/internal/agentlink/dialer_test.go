package agentlink

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const issuedCredential = "issued-credential"

// deviceAuth 是拨号方测试用 Authenticator：设备 ID 按 agent.DeviceID(凭据) 推导，与真实实现一致。
type deviceAuth struct {
	calls atomic.Int64
}

func (a *deviceAuth) Authenticate(_ context.Context, m agent.Auth) (string, error) {
	a.calls.Add(1)
	if m.Credential == goodCredential {
		return agent.DeviceID(m.Credential), nil
	}
	return "", &AuthError{Reason: agent.ReasonBadCredential}
}

func (a *deviceAuth) Pair(_ context.Context, m agent.Pair) (agent.PairOK, error) {
	a.calls.Add(1)
	if m.Code != goodPairCode {
		return agent.PairOK{}, &AuthError{Reason: agent.ReasonBadCode}
	}
	return agent.PairOK{
		Credential:   issuedCredential,
		DeviceID:     agent.DeviceID(issuedCredential),
		ListenerInfo: agent.ListenerInfo{Hostname: "pimon", Version: "test"},
	}, nil
}

// dialTarget 是一个真实监听方加上拨号所需的指纹。
type dialTarget struct {
	*listenerFixture
	pin  certs.SPKI
	auth *deviceAuth
}

// newDialTarget 启动监听方（hub 角色）；useStub 为真时用 listener_test 的 stubAuth（设备 ID 与凭据不符）。
func newDialTarget(t *testing.T, useStub bool) *dialTarget {
	t.Helper()
	d := &dialTarget{auth: &deviceAuth{}}
	d.listenerFixture = newListenerFixture(t, func(cfg *ListenerConfig) {
		pin, err := certs.SPKIOf(cfg.Cert)
		if err != nil {
			t.Fatal(err)
		}
		d.pin = pin
		if !useStub {
			cfg.Auth = d.auth
		}
	})
	return d
}

func (d *dialTarget) config(inbox *recorder) DialerConfig {
	return DialerConfig{
		URL:        "wss://" + d.addr,
		Pin:        d.pin,
		Role:       agent.RoleAgent,
		Credential: goodCredential,
		Clock:      clock.NewFake(t0),
		Log:        quietLog(),
		Handler:    func(Peer) Handler { return inbox.handle },
	}
}

func dialOK(t *testing.T, cfg DialerConfig) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	t.Cleanup(func() { s.Close("") })
	return s
}

func dialErr(t *testing.T, cfg DialerConfig) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Dial(ctx, cfg)
	if err == nil {
		s.Close("")
		t.Fatal("期望 Dial 失败，却成功了")
	}
	return err
}

// exchange 断言双方会话双向收发业务帧。
func exchange(t *testing.T, d *dialTarget, s *Session, inbox *recorder) {
	t.Helper()
	ls := d.waitSession()
	if err := s.Send(&agent.RunNow{RequestID: "r1", InstanceID: "i1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "监听方收到 run_now", func() bool { return len(d.inbox.snapshot()) == 1 })
	if err := ls.Send(&agent.HelloOK{HubVersion: "test", ServerTime: t0}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "拨号方收到 hello_ok", func() bool { return len(inbox.snapshot()) == 1 })
	if _, ok := inbox.snapshot()[0].(*agent.HelloOK); !ok {
		t.Fatalf("拨号方收到 %#v", inbox.snapshot()[0])
	}
}

func TestDial_凭据正确连通并双向收发(t *testing.T) {
	d := newDialTarget(t, false)
	inbox := &recorder{}
	s := dialOK(t, d.config(inbox))
	p := s.Peer()
	if p.DeviceID != agent.DeviceID(goodCredential) || p.Role != agent.RoleHub || p.RemoteAddr != d.addr {
		t.Fatalf("对端信息 = %+v", p)
	}
	exchange(t, d, s, inbox)
}

func TestDial_URL带agent路径也可连通(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.URL = "wss://" + d.addr + "/agent"
	dialOK(t, cfg)
}

func TestDial_凭据错误返回AuthFailError(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.Credential = "wrong"
	err := dialErr(t, cfg)
	var af *AuthFailError
	if !errors.As(err, &af) || af.Reason != agent.ReasonBadCredential {
		t.Fatalf("期望 *AuthFailError{bad_credential}，得 %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("错误文本泄露凭据: %v", err)
	}
}

func TestDial_配对成功调用OnCredential且会话可用(t *testing.T) {
	d := newDialTarget(t, false)
	inbox := &recorder{}
	cfg := d.config(inbox)
	cfg.Credential = ""
	cfg.PairCode = goodPairCode
	cfg.PairInfo = agent.Pair{Name: "nas", Hostname: "nas", OS: "linux", Arch: "amd64"}
	var got agent.PairOK
	var calls atomic.Int64
	cfg.OnCredential = func(ok agent.PairOK) error {
		calls.Add(1)
		got = ok
		return nil
	}
	s := dialOK(t, cfg)
	if calls.Load() != 1 || got.Credential != issuedCredential || got.ListenerInfo.Hostname != "pimon" {
		t.Fatalf("OnCredential 调用 %d 次，参数 %#v", calls.Load(), got)
	}
	if s.Peer().DeviceID != agent.DeviceID(issuedCredential) {
		t.Fatalf("对端信息 = %+v", s.Peer())
	}
	exchange(t, d, s, inbox)
}

func TestDial_OnCredential报错时关闭连接并返回错误(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.Credential = ""
	cfg.PairCode = goodPairCode
	errSave := errors.New("写盘失败")
	cfg.OnCredential = func(agent.PairOK) error { return errSave }
	err := dialErr(t, cfg)
	if !errors.Is(err, errSave) {
		t.Fatalf("期望包装 OnCredential 的错误，得 %v", err)
	}
	ls := d.waitSession()
	waitDone(t, ls)
}

func TestDial_配对码错误返回AuthFailError(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.Credential = ""
	cfg.PairCode = "WRONG"
	cfg.OnCredential = func(agent.PairOK) error { t.Error("配对失败时不应调用 OnCredential"); return nil }
	var af *AuthFailError
	if err := dialErr(t, cfg); !errors.As(err, &af) || af.Reason != agent.ReasonBadCode {
		t.Fatalf("期望 *AuthFailError{bad_code}，得 %v", err)
	}
}

func TestDial_指纹不匹配返回PinMismatchError且不发任何帧(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.Pin[0] ^= 0xff
	err := dialErr(t, cfg)
	var pm *certs.PinMismatchError
	if !errors.As(err, &pm) || pm.Got != d.pin {
		t.Fatalf("期望 *certs.PinMismatchError，得 %v", err)
	}
	var de *DialError
	if errors.As(err, &de) {
		t.Fatalf("钉扎失败不应归为 DialError: %v", err)
	}
	if n := d.auth.calls.Load(); n != 0 {
		t.Fatalf("监听方 Authenticator 被调用 %d 次", n)
	}
	select {
	case s := <-d.sessions:
		t.Fatalf("监听方不应建立会话: %+v", s.Peer())
	default:
	}
}

func TestDial_设备ID与凭据不符被拒(t *testing.T) {
	d := newDialTarget(t, true) // stubAuth 对 goodCredential 返回 dev-good
	cfg := d.config(&recorder{})
	err := dialErr(t, cfg)
	if !errors.Is(err, ErrHandshakeProtocol) {
		t.Fatalf("期望 ErrHandshakeProtocol，得 %v", err)
	}
	ls := d.waitSession()
	waitDone(t, ls)
}

// silentListener 是只做 TLS + WebSocket 升级、此后不回应任何帧的假监听方。
type silentListener struct {
	addr string
	pin  certs.SPKI
	got  atomic.Int64 // 收到的帧数
}

func newSilentListener(t *testing.T) *silentListener {
	t.Helper()
	dir := t.TempDir()
	cert, err := certs.LoadOrCreate(filepath.Join(dir, "s.crt"), filepath.Join(dir, "s.key"), time.Now(), certs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := certs.SPKIOf(cert)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := &silentListener{addr: ln.Addr().String(), pin: pin}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
			sl.got.Add(1)
		}
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = srv.Serve(tls.NewListener(ln, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}))
	}()
	t.Cleanup(func() { _ = srv.Close() })
	return sl
}

func TestDial_监听方10秒不应答返回ErrHandshakeTimeout(t *testing.T) {
	sl := newSilentListener(t)
	clk := clock.NewFake(t0)
	cfg := DialerConfig{URL: "wss://" + sl.addr, Pin: sl.pin, Role: agent.RoleAgent, Credential: goodCredential, Clock: clk, Log: quietLog()}
	errc := make(chan error, 1)
	go func() {
		s, err := Dial(context.Background(), cfg)
		if s != nil {
			s.Close("")
		}
		errc <- err
	}()
	waitFor(t, "握手计时器登记", func() bool { return clk.Waiters() == 1 })
	waitFor(t, "监听方收到认证帧", func() bool { return sl.got.Load() == 1 })
	clk.Advance(HandshakeTimeout - time.Nanosecond)
	select {
	case err := <-errc:
		t.Fatalf("未到 10 秒就返回: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	clk.Advance(time.Nanosecond)
	select {
	case err := <-errc:
		if !errors.Is(err, ErrHandshakeTimeout) {
			t.Fatalf("期望 ErrHandshakeTimeout，得 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dial 未在握手超时后返回")
	}
}

func TestDial_经SOCKS5代理连通(t *testing.T) {
	d := newDialTarget(t, false)
	px := proxytest.NewSocks5Forward(t, "u", "p")
	p, err := proxy.Parse("socks5://u:p@" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	inbox := &recorder{}
	cfg := d.config(inbox)
	cfg.Proxy = p
	s := dialOK(t, cfg)
	exchange(t, d, s, inbox)
	if seen := px.Seen(); len(seen) != 1 || seen[0].Target != d.addr {
		t.Fatalf("代理记录 = %+v", seen)
	}
}

func TestDial_经HTTP_CONNECT代理连通(t *testing.T) {
	d := newDialTarget(t, false)
	px := proxytest.NewHTTPConnect(t, "", "")
	p, err := proxy.Parse("http://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	inbox := &recorder{}
	cfg := d.config(inbox)
	cfg.Proxy = p
	s := dialOK(t, cfg)
	exchange(t, d, s, inbox)
	if len(px.Seen()) != 1 {
		t.Fatalf("代理记录 = %+v", px.Seen())
	}
}

// unreachableURL 把监听方地址换成同端口的 [::1]（监听方只在 127.0.0.1 上），直拨会被拒绝。
func unreachableURL(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return "wss://" + net.JoinHostPort("::1", port)
}

func TestDial_直拨不可达时用Fallback地址连通(t *testing.T) {
	d := newDialTarget(t, false)
	inbox := &recorder{}
	cfg := d.config(inbox)
	cfg.URL = unreachableURL(d.addr)
	var calls atomic.Int64
	cfg.Fallback = func(context.Context) ([]netip.Addr, error) {
		calls.Add(1)
		return []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	s := dialOK(t, cfg)
	if calls.Load() != 1 {
		t.Fatalf("Fallback 调用 %d 次", calls.Load())
	}
	if s.Peer().RemoteAddr != d.addr {
		t.Fatalf("RemoteAddr = %q，期望 %q", s.Peer().RemoteAddr, d.addr)
	}
	exchange(t, d, s, inbox)
}

func TestDial_Fallback全部失败返回最后一个DialError(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.URL = unreachableURL(d.addr)
	cfg.Fallback = func(context.Context) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("::1")}, nil
	}
	err := dialErr(t, cfg)
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("期望 *DialError，得 %v", err)
	}
}

func TestDial_配置了代理时不调用Fallback(t *testing.T) {
	d := newDialTarget(t, false)
	px := proxytest.NewSocks5Forward(t, "", "")
	p, err := proxy.Parse("socks5://" + px.Addr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := d.config(&recorder{})
	cfg.URL = unreachableURL(d.addr)
	cfg.Proxy = p
	var calls atomic.Int64
	cfg.Fallback = func(context.Context) ([]netip.Addr, error) {
		calls.Add(1)
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	err = dialErr(t, cfg)
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("期望 *DialError，得 %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("配置了代理却调用了 Fallback %d 次", calls.Load())
	}
}

func TestDial_钉扎失败不调用Fallback(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	cfg.Pin[0] ^= 0xff
	var calls atomic.Int64
	cfg.Fallback = func(context.Context) ([]netip.Addr, error) {
		calls.Add(1)
		return nil, nil
	}
	var pm *certs.PinMismatchError
	if err := dialErr(t, cfg); !errors.As(err, &pm) {
		t.Fatalf("期望 *certs.PinMismatchError，得 %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("钉扎失败却调用了 Fallback %d 次", calls.Load())
	}
}

func TestDial_HTTP升级失败返回DialError(t *testing.T) {
	d := newDialTarget(t, false)
	cfg := d.config(&recorder{})
	// 监听方对非 /agent 路径回 404；通过不导出的拨号路径构造，验证升级失败的分类。
	cfg.URL = "wss://" + d.addr
	err := dialURLPath(t, cfg, "/nope")
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("期望 *DialError，得 %v", err)
	}
	if isConnectStageError(err) {
		t.Fatalf("升级失败不应归为 TCP/TLS 阶段失败（否则会触发 Fallback）: %v", err)
	}
}

// dialURLPath 绕过 URL 校验，用指定路径拨号，只用于构造升级失败。
func dialURLPath(t *testing.T, cfg DialerConfig, path string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tgt, err := parseDialURL(cfg.URL)
	if err != nil {
		t.Fatal(err)
	}
	tgt.path = path
	cfg = withDialDefaults(cfg)
	_, _, err = connect(ctx, cfg, tgt, tgt.hostport)
	return err
}

func TestDial_配置错误(t *testing.T) {
	base := DialerConfig{URL: "wss://127.0.0.1:31418", Role: agent.RoleAgent, Credential: "c"}
	cases := []struct {
		name   string
		mutate func(*DialerConfig)
	}{
		{"凭据与配对码都给", func(c *DialerConfig) { c.PairCode = "X" }},
		{"凭据与配对码都不给", func(c *DialerConfig) { c.Credential = "" }},
		{"scheme 不是 wss", func(c *DialerConfig) { c.URL = "ws://127.0.0.1:31418" }},
		{"https scheme", func(c *DialerConfig) { c.URL = "https://127.0.0.1:31418" }},
		{"缺端口", func(c *DialerConfig) { c.URL = "wss://127.0.0.1" }},
		{"其他路径", func(c *DialerConfig) { c.URL = "wss://127.0.0.1:31418/other" }},
		{"带查询", func(c *DialerConfig) { c.URL = "wss://127.0.0.1:31418/agent?x=1" }},
		{"角色非法", func(c *DialerConfig) { c.Role = "x" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			_, err := Dial(context.Background(), cfg)
			if !errors.Is(err, ErrDialConfig) {
				t.Fatalf("期望 ErrDialConfig，得 %v", err)
			}
		})
	}
}
