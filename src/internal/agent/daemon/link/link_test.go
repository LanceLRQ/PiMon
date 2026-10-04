package link

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/agent/hubtest"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

func testManifest(t *testing.T, id string) agent.PluginManifest {
	t.Helper()
	m, err := manifest.Parse([]byte("id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: " + id +
		"\nkind: source\nruntime: builtin\nruns_on: [hub, agent]\noutputs:\n  - {key: v, type: number, title: V}\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return agent.PluginManifest{Origin: "builtin", Manifest: raw}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestPairDialIn_真实hub占位当场确认且hello带manifest(t *testing.T) {
	hub := hubtest.Start(t)
	code := hub.NewCode(t, "nas")
	var saved agent.PairOK
	res, err := PairDialIn(context.Background(), PairRequest{
		HubURL: hub.URL, Pin: hub.Pin, Code: code, Name: "nas-box", Role: "nas",
		Save:  func(ok agent.PairOK) error { saved = ok; return nil },
		Hello: func() agent.Hello { return NewHello("nas.local", []agent.PluginManifest{testManifest(t, "probe-x")}) },
		Clock: clock.NewFake(hubtest.T0), Log: agentlinktest.QuietLog(),
	})
	if err != nil {
		t.Fatalf("配对失败: %v", err)
	}
	if saved.Credential == "" || res.DeviceID != agent.DeviceID(saved.Credential) {
		t.Fatalf("凭据未落盘或设备 ID 不符: res=%+v", res)
	}
	if res.HubHostname != "pimon-hub.lan" || res.HubVersion != hubtest.HubVersion {
		t.Fatalf("结果 = %+v", res)
	}
	if !hub.Confirmed(t, res.DeviceID) {
		t.Fatal("配对会话上的 hello 应当场确认 hub 占位")
	}
	ps, err := hub.Plugins.MachinePlugins(context.Background(), res.DeviceID)
	if err != nil || len(ps) != 1 || ps[0].Manifest.ID != "probe-x" {
		t.Fatalf("hub 应收到 hello 携带的 manifest: %v %+v", err, ps)
	}
	waitUntil(t, "配对会话关闭", func() bool { return !hub.Online(res.DeviceID) })
}

func TestPairDialIn_配对码错误不落盘(t *testing.T) {
	hub := hubtest.Start(t)
	called := false
	_, err := PairDialIn(context.Background(), PairRequest{
		HubURL: hub.URL, Pin: hub.Pin, Code: "AAAA-BBBB",
		Save:  func(agent.PairOK) error { called = true; return nil },
		Hello: func() agent.Hello { return NewHello("h", nil) },
		Clock: clock.NewFake(hubtest.T0), Log: agentlinktest.QuietLog(),
	})
	var af *agentlink.AuthFailError
	if !errors.As(err, &af) || af.Reason != agent.ReasonBadCode {
		t.Fatalf("应返回 bad_code，得到 %v", err)
	}
	if called {
		t.Fatal("配对失败不应落盘")
	}
}

// stubHub 是可编排 hello 应答的替身 hub。
type stubHub struct {
	srv    *agentlinktest.Server
	auth   *agentlinktest.StaticAuth
	hellos chan *agentlink.Session
	onHi   func(s *agentlink.Session)
	sess   chan *agentlink.Session
}

func startStub(t *testing.T, clk clock.Clock, onHello func(s *agentlink.Session)) *stubHub {
	h := &stubHub{auth: agentlinktest.NewStaticAuth(), hellos: make(chan *agentlink.Session, 4),
		onHi: onHello, sess: make(chan *agentlink.Session, 4)}
	h.srv = agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleHub, Auth: h.auth, Clock: clk,
		OnSession: func(s *agentlink.Session) { h.sess <- s },
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(s *agentlink.Session, m agent.Message) {
				if _, ok := m.(*agent.Hello); ok {
					h.hellos <- s
					if h.onHi != nil {
						h.onHi(s)
					}
				}
			}
		},
	})
	return h
}

func (h *stubHub) req(clk clock.Clock, save func(agent.PairOK) error) PairRequest {
	h.auth.AddPairCode("CODE")
	return PairRequest{
		HubURL: h.srv.URL, Pin: h.srv.Pin, Code: "CODE", Save: save,
		Hello: func() agent.Hello { return NewHello("h", nil) }, Clock: clk, Log: agentlinktest.QuietLog(),
	}
}

func TestPairDialIn_不兼容返回原因并以版本原因关闭(t *testing.T) {
	clk := clock.NewFake(hubtest.T0)
	h := startStub(t, clk, func(s *agentlink.Session) {
		_ = s.Send(&agent.Incompatible{Reason: agent.ReasonVersionTooOld, RequiredMin: 3, HubVersion: "0.9.0"})
	})
	saved := false
	res, err := PairDialIn(context.Background(), h.req(clk, func(agent.PairOK) error { saved = true; return nil }))
	var inc *IncompatibleError
	if !errors.As(err, &inc) || inc.Reason != agent.ReasonVersionTooOld || inc.RequiredMin != 3 || inc.HubVersion != "0.9.0" {
		t.Fatalf("应返回不兼容错误，得到 %v", err)
	}
	if !saved || res.DeviceID == "" {
		t.Fatal("不兼容时凭据仍已落盘，结果应带设备 ID")
	}
	s := <-h.sess
	<-s.Done()
	if r := s.Err(); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonVersionTooOld {
		t.Fatalf("agent 应以版本原因关闭，hub 侧看到 %+v", r)
	}
}

func TestPairDialIn_hello超时(t *testing.T) {
	clk := clock.NewFake(hubtest.T0)
	h := startStub(t, clk, nil)
	errc := make(chan error, 1)
	go func() {
		_, err := PairDialIn(context.Background(), h.req(clk, func(agent.PairOK) error { return nil }))
		errc <- err
	}()
	<-h.hellos
	select {
	case err := <-errc:
		t.Fatalf("hello 超时前不应返回: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	clk.Advance(HelloTimeout)
	if err := <-errc; !errors.Is(err, ErrHelloTimeout) {
		t.Fatalf("应返回 ErrHelloTimeout，得到 %v", err)
	}
	s := <-h.sess
	<-s.Done()
	if r := s.Err(); r.Bye != agent.ReasonShutdown {
		t.Fatalf("超时后应以 shutdown 关闭，hub 侧看到 %+v", r)
	}
}

func TestPairDialIn_hub拒绝hello(t *testing.T) {
	clk := clock.NewFake(hubtest.T0)
	h := startStub(t, clk, func(s *agentlink.Session) { go s.Close(agent.ReasonRevoked) })
	_, err := PairDialIn(context.Background(), h.req(clk, func(agent.PairOK) error { return nil }))
	var rej *HelloRejectedError
	if !errors.As(err, &rej) || rej.Bye != agent.ReasonRevoked {
		t.Fatalf("应返回 hello 被拒绝，得到 %v", err)
	}
}

func TestPairDialIn_落盘失败不发hello(t *testing.T) {
	clk := clock.NewFake(hubtest.T0)
	h := startStub(t, clk, nil)
	boom := errors.New("磁盘满")
	_, err := PairDialIn(context.Background(), h.req(clk, func(agent.PairOK) error { return boom }))
	if !errors.Is(err, boom) {
		t.Fatalf("应返回落盘错误，得到 %v", err)
	}
	select {
	case <-h.hellos:
		t.Fatal("落盘失败不应发 hello")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestPairDialIn_缺少Save或Hello是配置错误(t *testing.T) {
	_, err := PairDialIn(context.Background(), PairRequest{HubURL: "wss://127.0.0.1:1", Code: "x"})
	if !errors.Is(err, agentlink.ErrDialConfig) {
		t.Fatalf("应返回配置错误，得到 %v", err)
	}
}

func TestMDNSName(t *testing.T) {
	cases := map[string]string{
		"":                      "pimon.local",
		"pimon-hub":             "pimon-hub.local",
		"pimon-hub.lan":         "pimon-hub.local",
		"raspberrypi.local.":    "raspberrypi.local",
		"bad name":              "pimon.local",
		"中文":                    "pimon.local",
		".x":                    "pimon.local",
		strings.Repeat("a", 64): "pimon.local",
	}
	for in, want := range cases {
		if got := MDNSName(in); got != want {
			t.Errorf("MDNSName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewHello主机名截到256字节且UTF8合法(t *testing.T) {
	h := NewHello(strings.Repeat("机", 100), nil)
	if len(h.Hostname) > 256 || !utf8.ValidString(h.Hostname) || h.Hostname == "" {
		t.Fatalf("主机名 = %d 字节 valid=%v", len(h.Hostname), utf8.ValidString(h.Hostname))
	}
	if err := h.Validate(); err != nil || h.ProtocolVersion != agent.ProtocolVersion || h.Plugins == nil {
		t.Fatalf("hello 不合法: %v %+v", err, h)
	}
}
