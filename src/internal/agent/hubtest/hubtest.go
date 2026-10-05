// Package hubtest 在测试里启动一个真实的 hub 侧 agent 通道（agentsrv + devices + 插件注册表 + 监听方），
// 供 agent 侧的连接层与守护进程测试当对端使用。只应被测试代码导入（会带入 internal/hub 的若干包）。
package hubtest

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	hubplugins "github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// T0 是 hub 假时钟的起点。
var T0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// HubVersion 是测试 hub 的软件版本。
const HubVersion = "0.2.0"

// Hub 是测试用的真实 hub 侧 agent 通道。
type Hub struct {
	Clock   *clock.Fake
	Devices *devices.Store
	Plugins *hubplugins.Registry
	Server  *agentsrv.Server
	Router  *Router
	// Auth 包住 Server，统计认证与配对调用次数。
	Auth *CountingAuth
	URL  string
	Pin  certs.SPKI
	ln   *agentlinktest.Server
}

// Start 启动 hub（假时钟起点 T0），t.Cleanup 时关闭。
func Start(t testing.TB) *Hub {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	must(t, db.Migrate(context.Background()))
	box, err := secret.New(make([]byte, 32))
	must(t, err)
	h := &Hub{Clock: clock.NewFake(T0), Router: &Router{}}
	h.Devices = devices.New(devices.Config{DB: db, Box: box, Clock: h.Clock})
	pres := devices.NewPresence(h.Clock, T0, nil)
	t.Cleanup(pres.Close)
	h.Plugins = hubplugins.New(hubplugins.Config{Dir: t.TempDir(), DB: db, Clock: h.Clock})
	h.Server, err = agentsrv.New(agentsrv.Config{
		Devices: h.Devices, Presence: pres, Plugins: h.Plugins, Clock: h.Clock, Log: agentlinktest.QuietLog(),
		HubVersion: HubVersion, Hostname: func() string { return "pimon-hub.lan" }, Router: h.Router,
	})
	must(t, err)
	t.Cleanup(h.Server.Close)
	h.Auth = &CountingAuth{Inner: h.Server}
	h.ln = agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleHub, Auth: h.Auth, Handler: h.Server.HandlerFor, OnSession: h.Server.Attach, Clock: h.Clock,
	})
	h.URL, h.Pin = h.ln.URL, h.ln.Pin
	return h
}

// NewCode 生成拨入配对码。
func (h *Hub) NewCode(t testing.TB, name string) string {
	t.Helper()
	code, _, err := h.Devices.NewPairingCode(context.Background(), devices.PairingOptions{Name: name})
	must(t, err)
	return code
}

// Confirmed 报告设备占位是否已确认（首个 hello 被接受）；设备不存在返回 false。
func (h *Hub) Confirmed(t testing.TB, id string) bool {
	t.Helper()
	d, err := h.Devices.Detail(context.Background(), id)
	if err != nil {
		return false
	}
	return d.Confirmed
}

// Online 报告设备当前是否有 hello 已被接受的会话。
func (h *Hub) Online(id string) bool {
	_, ok := h.Server.Session(id)
	return ok
}

// Revoke 删除设备（吊销），hub 随即以 bye{revoked} 断开它的会话。
func (h *Hub) Revoke(t testing.TB, id string) {
	t.Helper()
	must(t, h.Devices.Delete(context.Background(), id))
}

// Stop 关停监听方（已建立的会话收到 bye{shutdown}）。
func (h *Hub) Stop() error { return h.ln.Stop() }

// CountingAuth 统计认证与配对调用次数。
type CountingAuth struct {
	Inner agentlink.Authenticator
	n     atomic.Int64
}

// Calls 返回 Authenticate 与 Pair 被调用的总次数。
func (a *CountingAuth) Calls() int64 { return a.n.Load() }

// Authenticate 实现 agentlink.Authenticator。
func (a *CountingAuth) Authenticate(ctx context.Context, m agent.Auth) (string, error) {
	a.n.Add(1)
	return a.Inner.Authenticate(ctx, m)
}

// Pair 实现 agentlink.Authenticator。
func (a *CountingAuth) Pair(ctx context.Context, m agent.Pair) (agent.PairOK, error) {
	a.n.Add(1)
	return a.Inner.Pair(ctx, m)
}

// Router 记录 hello 被接受的设备与收到的事件，对升级信息请求回固定内容；
// 事件默认立即 ack，HoldAcks(true) 后只记录不 ack（模拟 hub 写库失败）。
type Router struct {
	mu     sync.Mutex
	ready  []string
	events []*agent.Event
	hold   bool
}

// HoldAcks 设置之后收到的事件是否只记录不 ack。
func (r *Router) HoldAcks(hold bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hold = hold
}

// Events 返回按到达顺序收到的全部事件（含重发的重复事件）。
func (r *Router) Events() []*agent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// OnEvent 实现 agentsrv.Router：记录事件，未 HoldAcks 时在原会话上回 ack。
func (r *Router) OnEvent(s *agentlink.Session, _ string, m *agent.Event) {
	r.mu.Lock()
	r.events = append(r.events, m)
	hold := r.hold
	r.mu.Unlock()
	if !hold {
		_ = s.Send(&agent.Ack{IDs: []string{m.ID}})
	}
}

// Ready 返回某设备 OnReady 的次数。
func (r *Router) Ready(id string) int {
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

// OnReady 实现 agentsrv.Router。
func (r *Router) OnReady(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = append(r.ready, id)
}

// OnReport 实现 agentsrv.Router。
func (r *Router) OnReport(*agentlink.Session, string, *agent.Report) {}

// OnAssignResult 实现 agentsrv.Router。
func (r *Router) OnAssignResult(string, *agent.AssignResult) {}

// OnUpgradeInfoGet 实现 agentsrv.Router：回固定的升级信息。
func (r *Router) OnUpgradeInfoGet(s *agentlink.Session, _ string, m *agent.UpgradeInfoGet) {
	_ = s.Send(&agent.UpgradeInfo{
		RequestID: m.RequestID, HubVersion: HubVersion, WebPort: 31415,
		Agents: map[string]string{"linux-amd64": "aa11", "linux-arm64": "bb22", "darwin-arm64": "cc33"},
	})
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
