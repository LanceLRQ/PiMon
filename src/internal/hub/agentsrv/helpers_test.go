package agentsrv

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// recRouter 记录 Router 回调。
type recRouter struct {
	mu      sync.Mutex
	ready   []string
	reports []string
	results []int64
	upgrade []string
}

func (r *recRouter) OnReady(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = append(r.ready, id)
}

func (r *recRouter) OnReport(_ *agentlink.Session, id string, m *agent.Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, id+"/"+m.InstanceID)
}

func (r *recRouter) OnAssignResult(_ string, m *agent.AssignResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, m.Version)
}

func (r *recRouter) OnUpgradeInfoGet(_ *agentlink.Session, id string, _ *agent.UpgradeInfoGet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upgrade = append(r.upgrade, id)
}

func (r *recRouter) readyCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, x := range r.ready {
		if x == id {
			n++
		}
	}
	return n
}

type env struct {
	db       *store.DB
	clk      *clock.Fake
	agentClk *clock.Fake
	devs     *devices.Store
	pres     *devices.Presence
	reg      *plugins.Registry
	router   *recRouter
	srv      *Server
	ln       *agentlinktest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	must(t, db.Migrate(context.Background()))
	box, err := secret.New(make([]byte, 32))
	must(t, err)
	e := &env{db: db, clk: clock.NewFake(t0), agentClk: clock.NewFake(t0), router: &recRouter{}}
	e.devs = devices.New(devices.Config{DB: db, Box: box, Clock: e.clk})
	e.pres = devices.NewPresence(e.clk, t0, nil)
	t.Cleanup(e.pres.Close)
	e.reg = plugins.New(plugins.Config{Dir: t.TempDir(), DB: db, Clock: e.clk})
	e.srv, err = New(Config{
		Devices: e.devs, Presence: e.pres, Plugins: e.reg, Clock: e.clk, Log: agentlinktest.QuietLog(),
		HubVersion: "0.2.0", Hostname: func() string { return "pimon" }, Router: e.router,
	})
	must(t, err)
	t.Cleanup(e.srv.Close)
	e.ln = agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleHub, Auth: e.srv, Handler: e.srv.HandlerFor, OnSession: e.srv.Attach, Clock: e.clk,
	})
	return e
}

// setHubMin 修改 hub 的最低兼容协议版本（与 hello 处理同在 flow 下）。
func (e *env) setHubMin(v int) {
	e.srv.flow.Lock()
	e.srv.hubMin = v
	e.srv.flow.Unlock()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// waitFor 轮询直到条件满足（网络收发是真实的，需要真实等待），上限 5 秒。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// peer 是测试拨号方（agent 侧）：会话与收到的业务消息。
type peer struct {
	sess *agentlink.Session
	msgs chan agent.Message
	cred string
	id   string
}

func (e *env) dialCfg(p *peer) agentlink.DialerConfig {
	return agentlink.DialerConfig{
		URL: e.ln.URL, Pin: e.ln.Pin, Role: agent.RoleAgent, Clock: e.agentClk, Log: agentlinktest.QuietLog(),
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(_ *agentlink.Session, m agent.Message) { p.msgs <- m }
		},
	}
}

// pair 用配对码拨号，返回已认证但未发 hello 的拨号方。
func (e *env) pair(t *testing.T, code string) (*peer, error) {
	t.Helper()
	p := &peer{msgs: make(chan agent.Message, 16)}
	cfg := e.dialCfg(p)
	cfg.PairCode = code
	cfg.PairInfo = agent.Pair{Name: "nas-box", Hostname: "nas.local", OS: "linux", Arch: "amd64"}
	cfg.OnCredential = func(ok agent.PairOK) error { p.cred, p.id = ok.Credential, ok.DeviceID; return nil }
	s, err := agentlink.Dial(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	p.sess = s
	t.Cleanup(func() { s.Close("") })
	e.waitAttached(t, p.id)
	return p, nil
}

// dial 用长期凭据拨号。
func (e *env) dial(t *testing.T, cred string) (*peer, error) {
	t.Helper()
	p := &peer{msgs: make(chan agent.Message, 16), cred: cred, id: agent.DeviceID(cred)}
	cfg := e.dialCfg(p)
	cfg.Credential = cred
	s, err := agentlink.Dial(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	p.sess = s
	t.Cleanup(func() { s.Close("") })
	e.waitAttached(t, p.id)
	return p, nil
}

// waitAttached 等 hub 侧对该设备的会话完成 Attach（hello 超时计时器已登记），之后才可推进假时钟。
func (e *env) waitAttached(t *testing.T, id string) {
	t.Helper()
	waitFor(t, "hub 侧 Attach "+id, func() bool {
		e.srv.mu.Lock()
		defer e.srv.mu.Unlock()
		for s, c := range e.srv.conns {
			if s.Peer().DeviceID == id && c.attached {
				return true
			}
		}
		return false
	})
}

// newCode 生成拨入配对码。
func (e *env) newCode(t *testing.T, o devices.PairingOptions) string {
	t.Helper()
	code, _, err := e.devs.NewPairingCode(context.Background(), o)
	must(t, err)
	return code
}

// agentManifestJSON 返回 runs_on 含 agent 的合法 manifest JSON。
func agentManifestJSON(t *testing.T, id, version string) json.RawMessage {
	t.Helper()
	m, err := manifest.Parse([]byte("id: " + id + "\nversion: " + version +
		"\napi_version: 1\nname: " + id + "\nkind: source\nruntime: builtin\nruns_on: [hub, agent]\n" +
		"outputs:\n  - {key: v, type: number, title: V}\n"))
	must(t, err)
	raw, err := json.Marshal(m)
	must(t, err)
	return raw
}

func helloMsg(proto, minCompat int, ps ...agent.PluginManifest) *agent.Hello {
	return &agent.Hello{
		ProtocolVersion: proto, MinCompatibleVersion: minCompat, AgentVersion: "0.2.0",
		OS: "linux", Arch: "amd64", Hostname: "nas.local", Plugins: ps,
	}
}

// recv 等拨号方收到一条业务消息。
func (p *peer) recv(t *testing.T) agent.Message {
	t.Helper()
	select {
	case m := <-p.msgs:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("等待 hub 消息超时")
		return nil
	}
}

// helloOK 发 hello 并等待 hello_ok。
func (p *peer) helloOK(t *testing.T, ps ...agent.PluginManifest) {
	t.Helper()
	must(t, p.sess.Send(helloMsg(agent.ProtocolVersion, agent.MinCompatibleVersion, ps...)))
	if m, ok := p.recv(t).(*agent.HelloOK); !ok || m.HubVersion != "0.2.0" {
		t.Fatalf("应收到 hello_ok，得到 %#v", m)
	}
}

// waitClosed 等会话关闭并返回关闭原因。
func waitClosed(t *testing.T, s *agentlink.Session) agentlink.CloseReason {
	t.Helper()
	select {
	case <-s.Done():
		return s.Err()
	case <-time.After(5 * time.Second):
		t.Fatal("等待会话关闭超时")
		return agentlink.CloseReason{}
	}
}

func (e *env) presence(id string) string {
	st, _ := e.pres.State(id)
	return st
}

func (e *env) device(t *testing.T, id string) model.Device {
	t.Helper()
	d, err := e.devs.Get(context.Background(), id)
	must(t, err)
	return d
}

type manifestRow struct {
	id, version string
	available   int
}

func (e *env) manifestRows(t *testing.T, machine string) []manifestRow {
	t.Helper()
	rows, err := e.db.QueryContext(context.Background(),
		`SELECT plugin_id, version, available FROM manifests WHERE machine = ? ORDER BY plugin_id, version`, machine)
	must(t, err)
	defer func() { _ = rows.Close() }()
	var out []manifestRow
	for rows.Next() {
		var r manifestRow
		must(t, rows.Scan(&r.id, &r.version, &r.available))
		out = append(out, r)
	}
	must(t, rows.Err())
	return out
}

func isAuthFail(err error, reason string) bool {
	var af *agentlink.AuthFailError
	return errors.As(err, &af) && af.Reason == reason
}
