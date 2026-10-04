package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

const probeManifest = `id: probe-x
version: 1.0.0
api_version: 1
name: probe
kind: source
runtime: builtin
runs_on: [agent]
interval: 60s
timeout: 30s
outputs:
  - {key: v, type: number, title: V}
`

// probeSource 是只注入、不注册到全局的测试插件，统计调用次数。
type probeSource struct {
	m     *manifest.Manifest
	calls atomic.Int64
}

func newProbe(t *testing.T) *probeSource {
	m, err := manifest.Parse([]byte(probeManifest))
	if err != nil {
		t.Fatal(err)
	}
	return &probeSource{m: m}
}

func (p *probeSource) Manifest() *manifest.Manifest { return p.m }

func (p *probeSource) Collect(context.Context, runtime.Input) (*report.Report, error) {
	p.calls.Add(1)
	return &report.Report{Status: report.StatusOK, Summary: "ok"}, nil
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

// never 断言一小段真实时间内条件不成立（用于证明「没有发生」）。
func never(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cond() {
			t.Fatalf("不应发生: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// harness 是一个运行中的守护进程。
type harness struct {
	t      *testing.T
	clk    *clock.Fake
	paths  config.Paths
	ring   *logging.Ring
	probe  *probeSource
	d      *Daemon
	cancel context.CancelFunc
	errc   chan error

	mu       sync.Mutex
	addr     string
	mdnsName []string
}

type option func(*Config)

func newHarness(t *testing.T, conf *config.Config, opts ...option) *harness {
	t.Helper()
	paths, err := config.Resolve(config.Env{GOOS: "linux", User: true, Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureDataDir(paths); err != nil {
		t.Fatal(err)
	}
	if conf != nil {
		if err := config.Save(paths.Config, *conf); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{t: t, clk: clock.NewFake(t0), paths: paths, ring: logging.NewRing(RingCapacity), probe: newProbe(t)}
	cfg := Config{
		Paths: paths, Clock: h.clk, Ring: h.ring,
		Log:      logging.NewWithRing(io.Discard, slog.LevelDebug, h.ring),
		Builtins: []runtime.Source{h.probe},
		Jitter:   func(time.Duration) time.Duration { return 0 },
		Rand:     func() float64 { return 0.5 },
		Listen:   "127.0.0.1:0",
		OnListen: func(a string) { h.mu.Lock(); h.addr = a; h.mu.Unlock() },
		Hostname: func() (string, error) { return "agent-box", nil },
		MDNS: func(_ context.Context, name string) ([]netip.Addr, error) {
			h.mu.Lock()
			h.mdnsName = append(h.mdnsName, name)
			h.mu.Unlock()
			return nil, nil
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	h.d = New(cfg)
	return h
}

func (h *harness) start() *harness {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.errc = cancel, make(chan error, 1)
	go func() { h.errc <- h.d.Run(ctx) }()
	h.t.Cleanup(h.stop)
	return h
}

// stop 取消 ctx 并等 Run 返回（可重复调用）。
func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.cancel = nil
	select {
	case err := <-h.errc:
		if err != nil {
			h.t.Errorf("Run 返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		h.t.Fatal("Run 未在取消后返回")
	}
}

// running 报告 Run 是否仍在运行（未返回）。
func (h *harness) running() bool {
	select {
	case err := <-h.errc:
		h.errc <- err
		return false
	default:
		return true
	}
}

func (h *harness) listenAddr() string {
	waitUntil(h.t, "拨出监听就绪", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.addr != "" })
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addr
}

func (h *harness) diskConfig() config.Config {
	h.t.Helper()
	c, err := config.Load(h.paths.Config, euid())
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

func (h *harness) status() Status { return h.d.Status() }

// assertNoSecret 断言 Status 序列化后不含给定的敏感值。
func (h *harness) assertNoSecret(secrets ...string) {
	h.t.Helper()
	b, err := json.Marshal(h.d.Status())
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(string(b), s) {
			h.t.Fatalf("状态输出含敏感值: %s", b)
		}
	}
}

// stubHub 是拨入方向的替身 hub（监听方）：记录会话与业务消息，hello 的应答可编排。
type stubHub struct {
	t    *testing.T
	clk  *clock.Fake
	srv  *agentlinktest.Server
	auth *agentlinktest.StaticAuth
	cred string
	// onHello 为空时立即回 hello_ok。
	onHello func(s *agentlink.Session, m *agent.Hello)
	msgs    chan agent.Message
	sess    chan *agentlink.Session
}

func startStubHub(t *testing.T, onHello func(s *agentlink.Session, m *agent.Hello)) *stubHub {
	t.Helper()
	cred, err := agent.NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHub{t: t, clk: clock.NewFake(t0), auth: agentlinktest.NewStaticAuth(), cred: cred,
		onHello: onHello, msgs: make(chan agent.Message, 64), sess: make(chan *agentlink.Session, 16)}
	h.auth.AddCredential(cred)
	h.srv = agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleHub, Auth: h.auth, Clock: h.clk,
		OnSession: func(s *agentlink.Session) { h.sess <- s },
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(s *agentlink.Session, m agent.Message) {
				h.msgs <- m
				if hi, ok := m.(*agent.Hello); ok {
					if h.onHello != nil {
						h.onHello(s, hi)
					} else {
						_ = s.Send(&agent.HelloOK{HubVersion: "0.2.0", ServerTime: t0})
					}
				}
			}
		},
	})
	return h
}

func (h *stubHub) config() *config.Config {
	return &config.Config{Mode: config.ModeDialIn, HubURL: h.srv.URL, HubPin: h.srv.Pin.String(),
		Credential: config.Credential(h.cred), HubHostname: "pimon-hub.lan"}
}

func (h *stubHub) nextSession() *agentlink.Session {
	h.t.Helper()
	select {
	case s := <-h.sess:
		return s
	case <-time.After(5 * time.Second):
		h.t.Fatal("等待会话超时")
		return nil
	}
}

func (h *stubHub) next() agent.Message {
	h.t.Helper()
	select {
	case m := <-h.msgs:
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("等待 agent 消息超时")
		return nil
	}
}

// fakeHub 是拨出方向的替身 hub（拨号方），收到 hello 时按 hold 决定是否立即回 hello_ok。
type fakeHub struct {
	t    *testing.T
	sess *agentlink.Session
	cred string
	msgs chan agent.Message
	hold chan struct{} // 非空时 hello_ok 等它关闭再发
}

// fakeOpts 是替身 hub 的拨号参数：Cred 与 Code 二选一；OnCred 模拟 hub 入库（返回错误即入库失败）；
// Hold 非空时 hello_ok 等它关闭再发。
type fakeOpts struct {
	Cred, Code string
	OnCred     func(agent.PairOK) error
	Hold       chan struct{}
}

func dialFake(t *testing.T, addr string, pin certs.SPKI, o fakeOpts) (*fakeHub, error) {
	t.Helper()
	f := &fakeHub{t: t, cred: o.Cred, msgs: make(chan agent.Message, 64), hold: o.Hold}
	onCred, code := o.OnCred, o.Code
	cfg := agentlink.DialerConfig{
		URL: "wss://" + addr, Pin: pin, Role: agent.RoleHub, Credential: o.Cred, PairCode: code,
		Clock: clock.NewFake(t0), Log: agentlinktest.QuietLog(),
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(s *agentlink.Session, m agent.Message) {
				f.msgs <- m
				if _, ok := m.(*agent.Hello); ok {
					hold := f.hold
					go func() {
						if hold != nil {
							<-hold
						}
						_ = s.Send(&agent.HelloOK{HubVersion: "0.2.0", ServerTime: t0})
					}()
				}
			}
		},
	}
	if code != "" {
		cfg.PairInfo = agent.Pair{Name: "pimon-hub", Hostname: "pimon"}
		cfg.OnCredential = func(ok agent.PairOK) error {
			if onCred != nil {
				if err := onCred(ok); err != nil {
					return err
				}
			}
			f.cred = ok.Credential
			return nil
		}
	}
	s, err := agentlink.Dial(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	f.sess = s
	t.Cleanup(func() { s.Close("") })
	return f, nil
}

func (f *fakeHub) next() agent.Message {
	f.t.Helper()
	select {
	case m := <-f.msgs:
		return m
	case <-time.After(5 * time.Second):
		f.t.Fatal("等待 agent 消息超时")
		return nil
	}
}
