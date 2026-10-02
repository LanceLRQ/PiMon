package agentlink_test

// 真实 TLS + WebSocket 的端到端测试：两个端点都在本机回环上，经 agentlinktest 搭建。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// waitLimit 是所有 channel 等待的真实时间上限。
const waitLimit = 10 * time.Second

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func recv[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitLimit):
		t.Fatalf("等待超时：%s", what)
	}
	var zero T
	return zero
}

func waitDone(t *testing.T, what string, s *agentlink.Session) agentlink.CloseReason {
	t.Helper()
	recv(t, what, s.Done())
	return s.Err()
}

// recordingClock 是假时钟，额外把每次 After 的时长发到 afters（先登记等待者再通知），
// 测试据此精确等到某个计时器（如退避）登记后再推进时间。
type recordingClock struct {
	*clock.Fake
	afters chan time.Duration
}

func newRecordingClock() *recordingClock {
	return &recordingClock{Fake: clock.NewFake(t0), afters: make(chan time.Duration, 1024)}
}

func (c *recordingClock) After(d time.Duration) <-chan time.Time {
	ch := c.Fake.After(d)
	select {
	case c.afters <- d:
	default:
	}
	return ch
}

// waitAfter 丢弃其他时长，直到有人登记了时长为 d 的计时器。
func (c *recordingClock) waitAfter(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.After(waitLimit)
	for {
		select {
		case got := <-c.afters:
			if got == d {
				return
			}
		case <-deadline:
			t.Fatalf("等待登记 %v 计时器超时", d)
		}
	}
}

// hubSide 模拟监听方的上层：登记会话，同一设备的新会话顶替旧会话（旧会话 Close(replaced)），
// 并把每个新会话发到 sessions。
type hubSide struct {
	mu       sync.Mutex
	byDevice map[string]*agentlink.Session
	sessions chan *agentlink.Session
}

func newHubSide() *hubSide {
	return &hubSide{byDevice: make(map[string]*agentlink.Session), sessions: make(chan *agentlink.Session, 16)}
}

func (h *hubSide) onSession(s *agentlink.Session) {
	id := s.Peer().DeviceID
	h.mu.Lock()
	old := h.byDevice[id]
	h.byDevice[id] = s
	h.mu.Unlock()
	if old != nil {
		old.Close(agent.ReasonReplaced)
	}
	h.sessions <- s
}

// startHub 启动 hub 角色的监听方，返回服务端、认证器与上层模拟。
func startHub(t *testing.T, mutate func(*agentlink.ListenerConfig)) (*agentlinktest.Server, *agentlinktest.StaticAuth, *hubSide) {
	t.Helper()
	auth := agentlinktest.NewStaticAuth()
	hub := newHubSide()
	cfg := agentlink.ListenerConfig{Role: agent.RoleHub, Auth: auth, OnSession: hub.onSession}
	if mutate != nil {
		mutate(&cfg)
	}
	return agentlinktest.StartListener(t, cfg), auth, hub
}

func agentDialer(srv *agentlinktest.Server, credential string) agentlink.DialerConfig {
	return agentlink.DialerConfig{
		URL:        srv.URL,
		Pin:        srv.Pin,
		Role:       agent.RoleAgent,
		Credential: credential,
		Log:        agentlinktest.QuietLog(),
	}
}

func dial(t *testing.T, cfg agentlink.DialerConfig) (*agentlink.Session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	s, err := agentlink.Dial(ctx, cfg)
	if err == nil {
		t.Cleanup(func() { s.Close("") })
	}
	return s, err
}

// TestIntegration_配对断开凭据重连心跳超时退避重连 是 M2a 的完成判定：
// 配对 → 断开 → 用凭据重连 → 心跳超时断开 → 退避重连。
//
// 心跳超时的驱动：拨号方（重连循环）走记录型假时钟，连接经 agentlinktest.Relay 中继；
// 冻结中继让监听方的帧再也到不了拨号方（等同对端失去响应），再把假时钟推进 HeartbeatTimeout。
// 冻结后不可能有 pong 回来，判活结果与 ping、判活计时器谁先触发无关，不依赖真实时间竞速。
func TestIntegration_配对断开凭据重连心跳超时退避重连(t *testing.T) {
	srv, auth, hub := startHub(t, nil)
	auth.AddPairCode("PAIR-CODE-1")

	// 1. 配对：一次性 Dial 拿凭据。
	var issued agent.PairOK
	pairCfg := agentDialer(srv, "")
	pairCfg.PairCode = "PAIR-CODE-1"
	pairCfg.PairInfo = agent.Pair{Name: "nas", Hostname: "nas", OS: "linux", Arch: "amd64"}
	pairCfg.OnCredential = func(ok agent.PairOK) error { issued = ok; return nil }
	pairCfg.Clock = clock.NewFake(t0)
	s1, err := dial(t, pairCfg)
	if err != nil {
		t.Fatalf("配对失败: %v", err)
	}
	devID := agent.DeviceID(issued.Credential)
	if issued.Credential == "" || issued.DeviceID != devID || s1.Peer().DeviceID != devID {
		t.Fatalf("配对结果不符: pair_ok 设备 %q，会话设备 %q，期望 %q", issued.DeviceID, s1.Peer().DeviceID, devID)
	}
	hs1 := recv(t, "监听方配对会话", hub.sessions)
	if hs1.Peer().DeviceID != devID || hs1.Peer().Role != agent.RoleAgent {
		t.Fatalf("监听方对端信息 = %+v", hs1.Peer())
	}
	// 配对码一次性：再用同一个码得到 bad_code。
	if _, err := dial(t, pairCfg); !isAuthFail(err, agent.ReasonBadCode) {
		t.Fatalf("重复使用配对码期望 bad_code，得 %v", err)
	}

	// 2. 断开：拨号方主动关闭，监听方收到 bye。
	s1.Close(agent.ReasonShutdown)
	if r := waitDone(t, "监听方配对会话结束", hs1); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonShutdown {
		t.Fatalf("监听方关闭原因 = %+v", r)
	}

	// 3. 用凭据重连：重连循环经中继拨号。
	relay := agentlinktest.NewRelay(t, srv.Addr)
	clk := newRecordingClock()
	cfg := agentDialer(srv, issued.Credential)
	cfg.URL = "wss://" + relay.Addr
	cfg.Clock = clk
	sessions := make(chan *agentlink.Session, 4)
	r := agentlink.NewReconnector(agentlink.ReconnectorConfig{
		Dialer:    cfg,
		OnSession: func(s *agentlink.Session) { sessions <- s },
		Rand:      func() float64 { return 0.25 }, // 抖动系数 0.9：首次退避 1s × 0.9
	})
	runErr := make(chan error, 1)
	go func() { runErr <- r.Run(context.Background()) }()
	defer r.Stop()

	s2 := recv(t, "重连循环首次连上", sessions)
	if s2.Peer().DeviceID != devID {
		t.Fatalf("重连会话设备 = %q", s2.Peer().DeviceID)
	}
	hs2 := recv(t, "监听方凭据会话", hub.sessions)
	if hs2.Peer().DeviceID != devID {
		t.Fatalf("监听方凭据会话设备 = %q", hs2.Peer().DeviceID)
	}

	// 4. 心跳超时：冻结中继后推进假时钟 35 秒。
	if n := relay.Freeze(); n != 1 {
		t.Fatalf("冻结的中继连接数 = %d", n)
	}
	clk.Advance(agentlink.HeartbeatTimeout)
	if reason := waitDone(t, "拨号方心跳超时", s2); reason.Kind != agentlink.CloseHeartbeat {
		t.Fatalf("拨号方关闭原因 = %+v，期望 heartbeat", reason)
	}

	// 5. 退避重连：首次退避 900ms 登记后推进，重连循环经中继建立新连接。
	clk.waitAfter(t, 900*time.Millisecond)
	select {
	case s := <-sessions:
		t.Fatalf("退避未到就重连了: %+v", s.Peer())
	default:
	}
	clk.Advance(900 * time.Millisecond)
	s3 := recv(t, "退避后重连", sessions)
	if s3.Peer().DeviceID != devID {
		t.Fatalf("退避重连会话设备 = %q", s3.Peer().DeviceID)
	}
	hs3 := recv(t, "监听方第二个凭据会话", hub.sessions)
	select {
	case <-s3.Done():
		t.Fatalf("新会话不应断开: %+v", s3.Err())
	default:
	}
	// 新会话可正常发送。
	if err := hs3.Send(&agent.HelloOK{HubVersion: "test", ServerTime: t0}); err != nil {
		t.Fatal(err)
	}
	// 冻结前的监听方旧会话：要么因中继断开读出错，要么被新会话顶替；总之已结束。
	waitDone(t, "监听方旧会话结束", hs2)

	// 6. 停止重连循环：Run 返回 nil，会话以 bye{shutdown} 关闭。
	r.Stop()
	if err := recv(t, "Run 返回", runErr); err != nil {
		t.Fatalf("Run 返回 %v，期望 nil", err)
	}
	if reason := waitDone(t, "监听方收到 shutdown", hs3); reason.Kind != agentlink.ClosePeerBye || reason.Bye != agent.ReasonShutdown {
		t.Fatalf("监听方关闭原因 = %+v", reason)
	}
}

func isAuthFail(err error, reason string) bool {
	var af *agentlink.AuthFailError
	return errors.As(err, &af) && af.Reason == reason
}

// 经代理拨号：真实转发的 SOCKS5 与 HTTP CONNECT 假代理各一次，代理须看到监听方地址，会话双向可用。
func TestIntegration_经代理拨号连通(t *testing.T) {
	cases := []struct {
		name  string
		start func(t *testing.T) (*proxytest.Server, string)
	}{
		{"socks5", func(t *testing.T) (*proxytest.Server, string) {
			px := proxytest.NewSocks5Forward(t, "u", "p")
			return px, "socks5://u:p@" + px.Addr
		}},
		{"http_connect", func(t *testing.T) (*proxytest.Server, string) {
			px := proxytest.NewHTTPConnect(t, "", "")
			return px, "http://" + px.Addr
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, auth, hub := startHub(t, nil)
			cred := "proxy-credential"
			devID := auth.AddCredential(cred)
			px, raw := tc.start(t)
			p, err := proxy.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			got := make(chan agent.Message, 1)
			cfg := agentDialer(srv, cred)
			cfg.Proxy = p
			cfg.Handler = func(agentlink.Peer) agentlink.Handler {
				return func(_ *agentlink.Session, m agent.Message) { got <- m }
			}
			s, err := dial(t, cfg)
			if err != nil {
				t.Fatalf("经代理拨号失败: %v", err)
			}
			if s.Peer().DeviceID != devID {
				t.Fatalf("会话设备 = %q", s.Peer().DeviceID)
			}
			hs := recv(t, "监听方会话", hub.sessions)
			if err := hs.Send(&agent.HelloOK{HubVersion: "test", ServerTime: t0}); err != nil {
				t.Fatal(err)
			}
			if m := recv(t, "拨号方收到 hello_ok", got); m.MsgType() != agent.TypeHelloOK {
				t.Fatalf("拨号方收到 %s", m.MsgType())
			}
			if seen := px.Seen(); len(seen) != 1 || seen[0].Target != srv.Addr {
				t.Fatalf("代理记录 = %+v，期望目标 %s", seen, srv.Addr)
			}
		})
	}
}

// 钉扎在真实 TLS 握手上生效：指纹不符时 Dial 返回 *certs.PinMismatchError，
// 监听方没有收到任何业务帧（认证器、Handler 工厂、OnSession 都未被调用）。
func TestIntegration_钉扎不符时TLS阶段失败且监听方无业务帧(t *testing.T) {
	var handlerCalls int
	var mu sync.Mutex
	srv, auth, hub := startHub(t, func(cfg *agentlink.ListenerConfig) {
		cfg.Handler = func(agentlink.Peer) agentlink.Handler {
			mu.Lock()
			handlerCalls++
			mu.Unlock()
			return nil
		}
	})
	cred := "pin-credential"
	auth.AddCredential(cred)
	_, otherPin := agentlinktest.NewCert(t)
	cfg := agentDialer(srv, cred)
	cfg.Pin = otherPin

	_, err := dial(t, cfg)
	var pm *certs.PinMismatchError
	if !errors.As(err, &pm) {
		t.Fatalf("期望 *certs.PinMismatchError，得 %v", err)
	}
	if pm.Want != otherPin || pm.Got != srv.Pin {
		t.Fatalf("PinMismatchError = want %s got %s，期望 got %s", pm.Want, pm.Got, srv.Pin)
	}
	// 用正确指纹再连一次作为屏障：前一个连接若有帧到达监听方，此时一定已处理完。
	cfg.Pin = srv.Pin
	if _, err := dial(t, cfg); err != nil {
		t.Fatalf("正确指纹拨号失败: %v", err)
	}
	recv(t, "屏障会话", hub.sessions)
	mu.Lock()
	defer mu.Unlock()
	if n := auth.Calls(); n != 1 || handlerCalls != 1 {
		t.Fatalf("认证器调用 %d 次、Handler 工厂调用 %d 次，期望都只来自屏障连接（各 1 次）", n, handlerCalls)
	}
	select {
	case s := <-hub.sessions:
		t.Fatalf("钉扎失败的连接不应建立会话: %+v", s.Peer())
	default:
	}
}

// 凭据错误时拨号方收到 auth_fail{bad_credential}，而不是因监听方关闭连接得到连接错误。
// 重复多次以暴露监听方 auth_fail 后立即关闭可能导致的 RST 竞争。
func TestIntegration_凭据错误收到authFail而非连接错误(t *testing.T) {
	srv, auth, _ := startHub(t, nil)
	auth.AddCredential("right")
	for i := range 5 {
		_, err := dial(t, agentDialer(srv, "wrong"))
		if !isAuthFail(err, agent.ReasonBadCredential) {
			var de *agentlink.DialError
			t.Fatalf("第 %d 次：期望 *AuthFailError{bad_credential}，得 %v（DialError=%v）", i+1, err, errors.As(err, &de))
		}
	}
}

// 同一设备的新连接顶替旧连接：上层在 OnSession 里对旧会话 Close(replaced)，旧的拨号方会话收到 bye{replaced}。
func TestIntegration_新连接顶替旧连接旧会话收到byeReplaced(t *testing.T) {
	srv, auth, hub := startHub(t, nil)
	cred := "replace-credential"
	auth.AddCredential(cred)

	old, err := dial(t, agentDialer(srv, cred))
	if err != nil {
		t.Fatal(err)
	}
	hsOld := recv(t, "监听方旧会话", hub.sessions)
	fresh, err := dial(t, agentDialer(srv, cred))
	if err != nil {
		t.Fatal(err)
	}
	recv(t, "监听方新会话", hub.sessions)

	if r := waitDone(t, "旧拨号方会话被顶替", old); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonReplaced {
		t.Fatalf("旧会话关闭原因 = %+v，期望 peer_bye/replaced", r)
	}
	if r := hsOld.Err(); r.Kind != agentlink.CloseLocal || r.Bye != agent.ReasonReplaced {
		t.Fatalf("监听方旧会话关闭原因 = %+v", r)
	}
	select {
	case <-fresh.Done():
		t.Fatalf("新会话不应断开: %+v", fresh.Err())
	default:
	}
	if got := agentlink.DefaultPolicy(nil, ptr(old.Err())); got != agentlink.Backoff {
		t.Fatalf("被顶替后的处置 = %v，期望 backoff", got)
	}
}

func ptr[T any](v T) *T { return &v }
