package agentsrv

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// fakeAgent 是 agent 监听方的测试替身：hub 作为拨号方连它；会话建立后主动发 hello（hello 为 nil 时不发）。
type fakeAgent struct {
	ln   *agentlinktest.Server
	auth *agentlinktest.StaticAuth

	mu       sync.Mutex
	hello    *agent.Hello
	sessions []*agentlink.Session
}

func startFakeAgent(t *testing.T, code string) *fakeAgent {
	t.Helper()
	a := &fakeAgent{auth: agentlinktest.NewStaticAuth(), hello: helloMsg(agent.ProtocolVersion, agent.MinCompatibleVersion)}
	if code != "" {
		a.auth.AddPairCode(code)
	}
	a.ln = agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleAgent, Auth: a.auth, Clock: clock.NewFake(t0),
		OnSession: func(s *agentlink.Session) {
			a.mu.Lock()
			a.sessions = append(a.sessions, s)
			h := a.hello
			a.mu.Unlock()
			if h != nil {
				_ = s.Send(h)
			}
		},
	})
	return a
}

func (a *fakeAgent) setHello(h *agent.Hello) {
	a.mu.Lock()
	a.hello = h
	a.mu.Unlock()
}

func (a *fakeAgent) sessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

// nopRefs 是没有任何实例引用代理的 proxies.Referrers。
type nopRefs struct{}

func (nopRefs) ListByProxy(context.Context, string) ([]model.ProxyReferrer, error) { return nil, nil }
func (nopRefs) DetachAndPause(context.Context, string) error                       { return nil }

// outEnv 是拨出测试环境：hub 侧服务可重建（模拟重启），数据库与代理仓库不变。
// clk 给 Server 与 Presence 用，dclk 给拨出管理器（重连退避、握手超时、拨出会话心跳）用。
type outEnv struct {
	db   *store.DB
	box  *secret.Box
	clk  *clock.Fake
	dclk *clock.Fake
	prox *proxies.Store

	devs *devices.Store
	pres *devices.Presence
	srv  *Server
	d    *DialOut
}

func newOutEnv(t *testing.T) *outEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	must(t, db.Migrate(context.Background()))
	box, err := secret.New(make([]byte, 32))
	must(t, err)
	e := &outEnv{db: db, box: box, clk: clock.NewFake(t0), dclk: clock.NewFake(t0)}
	// 与 app 一致：代理变化扇出到拨出管理器。
	e.prox = proxies.New(proxies.Config{DB: db, Box: box, Clock: e.clk, Referrers: nopRefs{}, OnChange: func() { e.d.Refresh() }})
	e.boot(t)
	return e
}

// boot 在同一数据库上构造设备仓库、在线状态、Server 与拨出管理器并启动拨出循环。
func (e *outEnv) boot(t *testing.T) {
	t.Helper()
	e.devs = devices.New(devices.Config{DB: e.db, Box: e.box, Clock: e.clk})
	e.pres = devices.NewPresence(e.clk, t0, nil)
	reg := plugins.New(plugins.Config{Dir: t.TempDir(), DB: e.db, Clock: e.clk})
	srv, err := New(Config{
		Devices: e.devs, Presence: e.pres, Plugins: reg, Clock: e.clk, Log: agentlinktest.QuietLog(),
		HubVersion: "0.2.0", Hostname: func() string { return "pimon" },
	})
	must(t, err)
	d, err := NewDialOut(DialOutConfig{Server: srv, Proxies: e.prox, Clock: e.dclk, Log: agentlinktest.QuietLog()})
	must(t, err)
	e.srv, e.d = srv, d
	// 关停顺序与 app 一致：拨出管理器 → Server → Presence（Cleanup 后注册先执行）。
	t.Cleanup(e.pres.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(d.Close)
	must(t, d.Start(context.Background()))
}

// reboot 按关停顺序停掉当前 hub 侧服务后重建。
func (e *outEnv) reboot(t *testing.T) {
	t.Helper()
	e.d.Close()
	e.srv.Close()
	e.pres.Close()
	e.boot(t)
}

func dialReq(a *fakeAgent, code string) model.DialOutRequest {
	return model.DialOutRequest{Address: a.ln.URL, Fingerprint: a.ln.Pin.String(), Code: code}
}

// pairOK 配对成功并返回设备。
func (e *outEnv) pairOK(t *testing.T, a *fakeAgent, code string) model.Device {
	t.Helper()
	dev, err := e.d.Pair(context.Background(), dialReq(a, code))
	if err != nil {
		t.Fatalf("拨出配对应成功: %v", err)
	}
	return dev
}

func (e *outEnv) online(id string) bool {
	st, _ := e.pres.State(id)
	_, ok := e.srv.Session(id)
	return st == model.PresenceOnline && ok
}

// waitNewSession 等设备出现一个不同于 old 的已接受会话。
func (e *outEnv) waitNewSession(t *testing.T, id string, old *agentlink.Session) *agentlink.Session {
	t.Helper()
	var cur *agentlink.Session
	waitFor(t, "设备重新上线", func() bool {
		s, ok := e.srv.Session(id)
		if !ok || s == old || !e.online(id) {
			return false
		}
		cur = s
		return true
	})
	return cur
}

// loopIdle 表示该设备没有重连循环、也没有仍在运行的循环协程（不会再拨号）。
func (e *outEnv) loopIdle(id string) bool {
	e.d.mu.Lock()
	defer e.d.mu.Unlock()
	_, l := e.d.loops[id]
	_, tail := e.d.tails[id]
	return !l && !tail
}

// pairingErr 断言 err 是指定原因的 *PairingError 并返回其副本。
func pairingErr(t *testing.T, err error, reason string) PairingError {
	t.Helper()
	var pe *PairingError
	if !errors.As(err, &pe) || pe.Reason != reason {
		t.Fatalf("应返回 pairing.failed{%s}，得到 %v", reason, err)
	}
	return *pe
}

func (e *outEnv) deviceCount(t *testing.T) int {
	t.Helper()
	list, err := e.devs.List(context.Background())
	must(t, err)
	return len(list)
}

func TestDialOut配对成功入库并上线(t *testing.T) {
	e := newOutEnv(t)
	ctx := context.Background()
	a := startFakeAgent(t, "PAIR-CODE-1")
	req := dialReq(a, " PAIR-CODE-1 ")
	req.Name = "vps1"
	dev, err := e.d.Pair(ctx, req)
	must(t, err)

	if dev.Direction != model.DeviceDialOut || dev.Name != "vps1" || dev.Role != model.DeviceRoleVPS ||
		dev.Address != "wss://"+a.ln.Addr || dev.Presence != model.PresenceOnline || dev.ProxyID != "" {
		t.Fatalf("返回的设备 = %+v", dev)
	}
	det, err := e.devs.Detail(ctx, dev.ID)
	must(t, err)
	if !det.Confirmed || det.Pin != a.ln.Pin.String() || !det.AutoHostMetrics {
		t.Fatalf("设备内部属性 = %+v", det)
	}
	cred, err := e.devs.Credential(ctx, dev.ID)
	must(t, err)
	if agent.DeviceID(cred) != dev.ID {
		t.Fatal("保存的凭据应能推导出设备 ID")
	}
	if !e.online(dev.ID) {
		t.Fatal("配对成功后设备应在线且注册了会话")
	}

	// 配对会话结束后由重连循环接手，用凭据重新连上。
	old, _ := e.srv.Session(dev.ID)
	old.Close("")
	e.waitNewSession(t, dev.ID, old)
	if n := a.sessionCount(); n != 2 {
		t.Fatalf("agent 侧会话数 = %d，期望 2（配对会话 + 重连会话）", n)
	}
	if got := e.d.ConnError(dev.ID); got != "" {
		t.Fatalf("连上后 ConnError 应为空，得到 %q", got)
	}
}

func TestDialOut缺省名称取对端主机名且可关闭自动创建(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C2")
	off := false
	req := dialReq(a, "C2")
	req.Role = model.DeviceRoleServer
	req.AutoHostMetrics = &off
	dev, err := e.d.Pair(context.Background(), req)
	must(t, err)
	det, err := e.devs.Detail(context.Background(), dev.ID)
	must(t, err)
	if dev.Name != "agentlinktest" || dev.Role != model.DeviceRoleServer || det.AutoHostMetrics {
		t.Fatalf("设备 = %+v，内部属性 = %+v", dev, det)
	}
}

func TestDialOut指纹不匹配(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C")
	_, other := agentlinktest.NewCert(t)
	req := dialReq(a, "C")
	req.Fingerprint = other.String()
	_, err := e.d.Pair(context.Background(), req)
	pe := pairingErr(t, err, ReasonPinMismatch)
	if pe.DeviceID != "" || e.deviceCount(t) != 0 || a.auth.Calls() != 0 {
		t.Fatalf("指纹不匹配不应发配对帧、不应建设备: %+v", pe)
	}
}

func TestDialOut监听方不可达(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C")
	must(t, a.ln.Stop())
	_, err := e.d.Pair(context.Background(), dialReq(a, "C"))
	pairingErr(t, err, ReasonUnreachable)
	if e.deviceCount(t) != 0 {
		t.Fatal("不可达时不应建设备")
	}
}

func TestDialOut配对码错误(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "RIGHT")
	_, err := e.d.Pair(context.Background(), dialReq(a, "WRONG"))
	pairingErr(t, err, agent.ReasonBadCode)
	if e.deviceCount(t) != 0 {
		t.Fatal("配对码错误时不应建设备")
	}
}

func TestDialOut不兼容时保留设备且升级后自动接入(t *testing.T) {
	e := newOutEnv(t)
	ctx := context.Background()
	a := startFakeAgent(t, "C")
	a.setHello(helloMsg(99, 99))
	_, err := e.d.Pair(ctx, dialReq(a, "C"))
	pe := pairingErr(t, err, ReasonIncompatible)
	if pe.DeviceID == "" || pe.RequiredMin != 99 || pe.HubVersion != "0.2.0" {
		t.Fatalf("不兼容的细节 = %+v", pe)
	}
	dev, err := e.devs.Get(ctx, pe.DeviceID)
	must(t, err)
	if dev.Direction != model.DeviceDialOut || dev.Incompatible == nil || dev.Incompatible.Reason != agent.ReasonVersionTooNew {
		t.Fatalf("设备应保留并记录不兼容原因: %+v", dev)
	}
	if e.online(pe.DeviceID) {
		t.Fatal("不兼容的设备不应在线")
	}

	// agent 升级后：重连循环按最大退避重试，推进时钟后自动接入。
	a.setHello(helloMsg(agent.ProtocolVersion, agent.MinCompatibleVersion))
	waitFor(t, "升级后自动接入", func() bool {
		if e.online(pe.DeviceID) {
			return true
		}
		e.dclk.Advance(time.Second)
		return false
	})
	dev, err = e.devs.Get(ctx, pe.DeviceID)
	must(t, err)
	if dev.Incompatible != nil {
		t.Fatalf("兼容后应清除不兼容原因: %+v", dev)
	}
}

func TestDialOut等待hello超时(t *testing.T) {
	t.Run("hub 侧 hello 超时", func(t *testing.T) {
		e := newOutEnv(t)
		a := startFakeAgent(t, "C")
		a.setHello(nil)
		errc := make(chan error, 1)
		go func() {
			_, err := e.d.Pair(context.Background(), dialReq(a, "C"))
			errc <- err
		}()
		waitFor(t, "配对会话已 Attach", func() bool {
			e.srv.mu.Lock()
			defer e.srv.mu.Unlock()
			for _, c := range e.srv.conns {
				if c.attached {
					return true
				}
			}
			return false
		})
		e.clk.Advance(HelloTimeout)
		pe := pairingErr(t, <-errc, ReasonTimeout)
		det, err := e.devs.Detail(context.Background(), pe.DeviceID)
		must(t, err)
		if det.Confirmed || det.Direction != model.DeviceDialOut {
			t.Fatalf("超时后设备应保留为未确认的拨出设备: %+v", det)
		}
	})
	t.Run("总时限到期", func(t *testing.T) {
		e := newOutEnv(t)
		e.d.pairTimeout = 300 * time.Millisecond
		a := startFakeAgent(t, "C")
		a.setHello(nil)
		_, err := e.d.Pair(context.Background(), dialReq(a, "C"))
		pe := pairingErr(t, err, ReasonTimeout)
		if pe.DeviceID == "" {
			t.Fatal("凭据已保存，details 应带 device_id")
		}
		// agent 补发 hello 后，重连循环接手并上线。
		a.setHello(helloMsg(agent.ProtocolVersion, agent.MinCompatibleVersion))
		waitFor(t, "重连上线", func() bool {
			if e.online(pe.DeviceID) {
				return true
			}
			e.dclk.Advance(time.Second)
			return false
		})
	})
}

func TestDialOut重启后自动重连(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C")
	dev := e.pairOK(t, a, "C")
	e.reboot(t)
	waitFor(t, "重启后上线", func() bool { return e.online(dev.ID) })
	if n := a.sessionCount(); n != 2 {
		t.Fatalf("agent 侧会话数 = %d，期望 2", n)
	}
}

func TestDialOut重启后对端不可达记录原因(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C")
	dev := e.pairOK(t, a, "C")
	e.d.Close()
	e.srv.Close()
	e.pres.Close()
	must(t, a.ln.Stop())
	e.boot(t)
	waitFor(t, "记录不可达", func() bool { return e.d.ConnError(dev.ID) == ReasonUnreachable })
}

func TestDialOut改代理后经新代理重连且代理删除后停止(t *testing.T) {
	e := newOutEnv(t)
	ctx := context.Background()
	a := startFakeAgent(t, "C")
	dev := e.pairOK(t, a, "C")
	s0, _ := e.srv.Session(dev.ID)

	socks := proxytest.NewSocks5Forward(t, "", "")
	px, err := e.prox.Create(ctx, model.ProxyInput{Name: "s5", Scheme: "socks5", Address: socks.Addr})
	must(t, err)
	_, err = e.devs.Update(ctx, dev.ID, model.DeviceInput{Name: dev.Name, Role: dev.Role, ProxyID: &px.ID})
	must(t, err)
	s1 := e.waitNewSession(t, dev.ID, s0)
	if !seenTarget(socks, a.ln.Addr) {
		t.Fatalf("应经 SOCKS5 代理连到 %s，代理记录 %+v", a.ln.Addr, socks.Seen())
	}

	// 代理本身被改成 HTTP CONNECT：代理仓库的变更回调扇出到 Refresh，按新代理重连。
	hc := proxytest.NewHTTPConnect(t, "", "")
	_, err = e.prox.Update(ctx, px.ID, model.ProxyInput{Name: "s5", Scheme: "http", Address: hc.Addr})
	must(t, err)
	e.waitNewSession(t, dev.ID, s1)
	if !seenTarget(hc, a.ln.Addr) {
		t.Fatalf("应经 HTTP CONNECT 代理连到 %s，代理记录 %+v", a.ln.Addr, hc.Seen())
	}

	// 代理被删除：停止循环、不退回直连，并记录 proxy_removed。
	must(t, e.prox.Delete(ctx, px.ID, false))
	waitFor(t, "代理删除后停止", func() bool {
		return e.d.ConnError(dev.ID) == ConnErrProxyRemoved && e.loopIdle(dev.ID) && !e.online(dev.ID)
	})
	n := a.sessionCount()
	must(t, e.d.reconcileAll())
	e.dclk.Advance(time.Minute)
	if got := a.sessionCount(); got != n || !e.loopIdle(dev.ID) {
		t.Fatalf("代理删除后不应再拨号：会话数 %d → %d", n, got)
	}
}

func seenTarget(p *proxytest.Server, addr string) bool {
	for _, s := range p.Seen() {
		if strings.Contains(s.Target, addr) {
			return true
		}
	}
	return false
}

func TestDialOut吊销后不再拨号(t *testing.T) {
	e := newOutEnv(t)
	ctx := context.Background()
	a := startFakeAgent(t, "C")
	dev := e.pairOK(t, a, "C")
	old, _ := e.srv.Session(dev.ID)
	old.Close("")
	cur := e.waitNewSession(t, dev.ID, old)

	must(t, e.devs.Delete(ctx, dev.ID))
	waitFor(t, "循环停止", func() bool { return e.loopIdle(dev.ID) })
	waitClosed(t, cur)
	calls := a.auth.Calls()
	must(t, e.d.reconcileAll())
	e.dclk.Advance(time.Minute)
	if got := a.auth.Calls(); got != calls || !e.loopIdle(dev.ID) {
		t.Fatalf("吊销后不应再拨号：认证次数 %d → %d", calls, got)
	}
}

func TestDialOut凭据失效后停止并记录原因(t *testing.T) {
	e := newOutEnv(t)
	ctx := context.Background()
	a := startFakeAgent(t, "C")
	dev := e.pairOK(t, a, "C")
	cred, err := e.devs.Credential(ctx, dev.ID)
	must(t, err)
	a.auth.RevokeCredential(cred) // 模拟 VPS 本地重新 pair

	e.reboot(t)
	waitFor(t, "记录 bad_credential", func() bool { return e.d.ConnError(dev.ID) == ConnErrBadCredential })
	calls := a.auth.Calls()
	// 配置未变时不自动重启。
	must(t, e.d.reconcileAll())
	e.dclk.Advance(time.Minute)
	if got := a.auth.Calls(); got != calls || e.d.ConnError(dev.ID) != ConnErrBadCredential {
		t.Fatalf("凭据失效后不应自动重启：认证次数 %d → %d", calls, got)
	}
}

func TestDialOut字段校验(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C")
	cases := []struct {
		name  string
		edit  func(*model.DialOutRequest)
		field string
		code  string
	}{
		{"地址缺失", func(r *model.DialOutRequest) { r.Address = " " }, "address", model.FieldRequired},
		{"地址不是 wss", func(r *model.DialOutRequest) { r.Address = "https://vps.example.com" }, "address", model.FieldInvalid},
		{"指纹缺失", func(r *model.DialOutRequest) { r.Fingerprint = "" }, "fingerprint", model.FieldRequired},
		{"指纹非法", func(r *model.DialOutRequest) { r.Fingerprint = "SHA256:xyz" }, "fingerprint", model.FieldInvalid},
		{"配对码缺失", func(r *model.DialOutRequest) { r.Code = "  " }, "code", model.FieldRequired},
		{"配对码过长", func(r *model.DialOutRequest) { r.Code = strings.Repeat("A", 65) }, "code", model.FieldOutOfRange},
		{"名称过长", func(r *model.DialOutRequest) { r.Name = strings.Repeat("名", 65) }, "name", model.FieldOutOfRange},
		{"角色非法", func(r *model.DialOutRequest) { r.Role = "router" }, "role", model.FieldInvalid},
		{"代理不存在", func(r *model.DialOutRequest) { r.ProxyID = "nope" }, "proxy_id", model.FieldInvalid},
	}
	for _, c := range cases {
		req := dialReq(a, "C")
		c.edit(&req)
		_, err := e.d.Pair(context.Background(), req)
		var fe model.FieldErrors
		if !errors.As(err, &fe) || fe[c.field] != c.code {
			t.Fatalf("%s: 应返回字段错误 %s=%s，得到 %v", c.name, c.field, c.code, err)
		}
	}
	if a.auth.Calls() != 0 || e.deviceCount(t) != 0 {
		t.Fatal("校验失败时不应拨号、不应建设备")
	}
}

func TestPairingReason映射(t *testing.T) {
	pin := fmt.Errorf("x: %w", &certs.PinMismatchError{})
	cases := []struct {
		err  error
		want string
	}{
		{pin, ReasonPinMismatch},
		{&agentlink.AuthFailError{Reason: agent.ReasonBadCode}, agent.ReasonBadCode},
		{&agentlink.AuthFailError{Reason: agent.ReasonCodeExpired}, agent.ReasonCodeExpired},
		{&agentlink.AuthFailError{Reason: agent.ReasonRateLimited}, agent.ReasonRateLimited},
		{&agentlink.AuthFailError{Reason: "<script>"}, ReasonUnknown},
		{&agentlink.AuthFailError{Reason: agent.ReasonBadCredential}, ReasonUnknown},
		{agentlink.ErrHandshakeTimeout, ReasonTimeout},
		{context.DeadlineExceeded, ReasonTimeout},
		{ErrHelloTimeout, ReasonTimeout},
		{&agentlink.DialError{Addr: "x:1", Err: errors.New("refused")}, ReasonUnreachable},
		{&IncompatibleError{Reason: agent.ReasonVersionTooOld}, ReasonIncompatible},
		{agentlink.ErrHandshakeProtocol, ReasonUnknown},
		{context.Canceled, ReasonUnknown},
		{ErrSessionClosed, ReasonUnknown},
		{errors.New("保存配对凭据失败"), ReasonUnknown},
	}
	for _, c := range cases {
		if got := pairingReason(c.err); got != c.want {
			t.Fatalf("%v → %q，期望 %q", c.err, got, c.want)
		}
	}
}

func TestDialOut关闭后拒绝配对且循环全部停止(t *testing.T) {
	e := newOutEnv(t)
	a := startFakeAgent(t, "C")
	dev := e.pairOK(t, a, "C")
	s, _ := e.srv.Session(dev.ID)
	s.Close("")
	cur := e.waitNewSession(t, dev.ID, s)
	e.d.Close()
	waitClosed(t, cur)
	if !e.loopIdle(dev.ID) {
		t.Fatal("Close 后不应留有重连循环")
	}
	if _, err := e.d.Pair(context.Background(), dialReq(a, "C")); !errors.Is(err, ErrDialOutClosed) {
		t.Fatalf("Close 后配对应返回 ErrDialOutClosed，得到 %v", err)
	}
}
