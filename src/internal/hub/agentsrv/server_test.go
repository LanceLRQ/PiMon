package agentsrv

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

func TestPair占位后首个hello确认(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	code := e.newCode(t, devices.PairingOptions{Name: "nas1", Role: model.DeviceRoleNAS})
	p, err := e.pair(t, code)
	must(t, err)

	det, err := e.devs.Detail(ctx, p.id)
	must(t, err)
	if det.Confirmed || det.Direction != model.DeviceDialIn {
		t.Fatalf("配对后应为未确认的拨入占位: %+v", det)
	}
	if e.presence(p.id) == model.PresenceOnline {
		t.Fatal("hello 前不应在线")
	}

	p.helloOK(t, agent.PluginManifest{Origin: "builtin", Manifest: agentManifestJSON(t, "host-metrics", "1.0.0")})
	det, err = e.devs.Detail(ctx, p.id)
	must(t, err)
	if !det.Confirmed {
		t.Fatal("首个 hello 后应已确认")
	}
	d := e.device(t, p.id)
	if d.Name != "nas1" || d.AgentVersion != "0.2.0" || d.OS != "linux" || d.Arch != "amd64" ||
		d.Hostname != "nas.local" || d.ProtocolVersion != agent.ProtocolVersion || d.Address != "127.0.0.1" || d.Incompatible != nil {
		t.Fatalf("设备行 = %+v", d)
	}
	waitFor(t, "在线", func() bool { return e.presence(p.id) == model.PresenceOnline })
	if s, ok := e.srv.Session(p.id); !ok || s.Peer().ProtocolVersion != agent.ProtocolVersion {
		t.Fatal("注册表应有该设备的会话且记录协议版本")
	}
	waitFor(t, "OnReady", func() bool { return e.router.readyCount(p.id) == 1 })
	if rows := e.manifestRows(t, p.id); len(rows) != 1 || rows[0] != (manifestRow{"host-metrics", "1.0.0", 1}) {
		t.Fatalf("manifest 行 = %+v", rows)
	}
}

func TestPair配对码错误或过期(t *testing.T) {
	e := newEnv(t)
	if _, err := e.pair(t, "NOT-A-REAL-CODE"); !isAuthFail(err, agent.ReasonBadCode) {
		t.Fatalf("错误配对码应 bad_code，得到 %v", err)
	}
	// 用时钟早 15 分钟的仓库生成配对码，使它此刻恰好过期（不推进 hub 时钟，避免清理循环先删掉它）。
	early := devices.New(devices.Config{DB: e.db, Clock: clock.NewFake(t0.Add(-devices.PairingCodeTTL))})
	code, _, err := early.NewPairingCode(context.Background(), devices.PairingOptions{})
	must(t, err)
	if _, err := e.pair(t, code); !isAuthFail(err, agent.ReasonCodeExpired) {
		t.Fatalf("过期配对码应 code_expired，得到 %v", err)
	}
	// 一次性：过期行已删除，再用同一个码是 bad_code。
	if _, err := e.pair(t, code); !isAuthFail(err, agent.ReasonBadCode) {
		t.Fatalf("已消耗的配对码应 bad_code，得到 %v", err)
	}
}

func TestAuthenticate凭据重连与拒绝(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t)
	p.sess.Close(agent.ReasonShutdown)
	waitFor(t, "断开", func() bool { return e.presence(p.id) == model.PresenceDisconnected })
	if _, ok := e.srv.Session(p.id); ok {
		t.Fatal("断开后注册表不应再有会话")
	}

	q, err := e.dial(t, p.cred)
	must(t, err)
	q.helloOK(t)
	waitFor(t, "重连后在线", func() bool { return e.presence(p.id) == model.PresenceOnline })

	other, err := agent.NewCredential()
	must(t, err)
	if _, err := e.dial(t, other); !isAuthFail(err, agent.ReasonBadCredential) {
		t.Fatalf("未知凭据应 bad_credential，得到 %v", err)
	}
	if _, err := e.dial(t, "short"); !isAuthFail(err, agent.ReasonBadCredential) {
		t.Fatalf("格式非法的凭据应 bad_credential，得到 %v", err)
	}
}

func TestAuthenticator直接调用(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var ae *agentlink.AuthError
	if _, err := e.srv.Authenticate(ctx, agent.Auth{Credential: "", Role: agent.RoleAgent}); !errors.As(err, &ae) || ae.Reason != agent.ReasonBadCredential {
		t.Fatalf("空凭据: %v", err)
	}
	if _, err := e.srv.Pair(ctx, agent.Pair{Code: "", Role: agent.RoleAgent}); !errors.As(err, &ae) || ae.Reason != agent.ReasonBadCode {
		t.Fatalf("空配对码: %v", err)
	}
	// 拨出设备的凭据不能用于拨入认证。
	cred, err := agent.NewCredential()
	must(t, err)
	_, err = e.devs.CreateDialOut(ctx, devices.DialOutDevice{Name: "vps", Address: "wss://vps:31418", Credential: cred})
	must(t, err)
	if _, err := e.srv.Authenticate(ctx, agent.Auth{Credential: cred, Role: agent.RoleAgent}); !errors.As(err, &ae) || ae.Reason != agent.ReasonBadCredential {
		t.Fatalf("拨出设备凭据: %v", err)
	}
	// ctx 已取消：返回非 AuthError 的错误。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	code := e.newCode(t, devices.PairingOptions{})
	if _, err := e.srv.Pair(cctx, agent.Pair{Code: code, Role: agent.RoleAgent}); err == nil || errors.As(err, &ae) {
		t.Fatalf("取消的 ctx 应返回内部错误，得到 %v", err)
	}
	if _, err := e.srv.Authenticate(cctx, agent.Auth{Credential: cred, Role: agent.RoleAgent}); err == nil || errors.As(err, &ae) {
		t.Fatalf("取消的 ctx 应返回内部错误，得到 %v", err)
	}
	// 取消的调用没有消耗配对码；配对成功返回监听方信息。
	ok, err := e.srv.Pair(ctx, agent.Pair{Code: code, Role: agent.RoleAgent, Name: "box"})
	must(t, err)
	if ok.DeviceID != agent.DeviceID(ok.Credential) || ok.ListenerInfo.Hostname != "pimon" || ok.ListenerInfo.Version != "0.2.0" {
		t.Fatalf("pair_ok = %+v", ok)
	}
}

func TestHello版本过低(t *testing.T) {
	e := newEnv(t)
	e.setHubMin(2) // hub 要求至少 2，agent 只有 1
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	must(t, p.sess.Send(helloMsg(1, 1)))
	m, ok := p.recv(t).(*agent.Incompatible)
	if !ok || m.Reason != agent.ReasonVersionTooOld || m.RequiredMin != 2 || m.HubVersion != "0.2.0" {
		t.Fatalf("应收到 incompatible，得到 %#v", m)
	}
	if r := waitClosed(t, p.sess); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonVersionTooOld {
		t.Fatalf("关闭原因 = %+v", r)
	}
	d := e.device(t, p.id)
	if d.Incompatible == nil || d.Incompatible.Reason != agent.ReasonVersionTooOld || d.Incompatible.RequiredMin != 2 {
		t.Fatalf("设备行应记录不兼容原因: %+v", d)
	}
	if e.presence(p.id) == model.PresenceOnline {
		t.Fatal("不兼容的设备不应在线")
	}
	if _, ok := e.srv.Session(p.id); ok {
		t.Fatal("不兼容的会话不应注册")
	}
	if e.router.readyCount(p.id) != 0 {
		t.Fatal("不兼容不应 OnReady")
	}
	// agent 已拿到凭据：占位随 hello 确认，升级 agent 后可直接用凭据接入。
	det, err := e.devs.Detail(context.Background(), p.id)
	must(t, err)
	if !det.Confirmed {
		t.Fatal("不兼容的 hello 也应确认占位")
	}

	// 升级后用凭据接入：兼容，清除不兼容原因。
	e.setHubMin(agent.MinCompatibleVersion)
	q, err := e.dial(t, p.cred)
	must(t, err)
	q.helloOK(t)
	if d := e.device(t, p.id); d.Incompatible != nil {
		t.Fatalf("兼容后应清除不兼容原因: %+v", d.Incompatible)
	}
}

func TestHello版本过新(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	must(t, p.sess.Send(helloMsg(agent.ProtocolVersion+1, agent.ProtocolVersion+1)))
	m, ok := p.recv(t).(*agent.Incompatible)
	if !ok || m.Reason != agent.ReasonVersionTooNew || m.RequiredMin != agent.ProtocolVersion+1 {
		t.Fatalf("应收到 incompatible，得到 %#v", m)
	}
	if r := waitClosed(t, p.sess); r.Bye != agent.ReasonVersionTooNew {
		t.Fatalf("关闭原因 = %+v", r)
	}
}

func TestHello同一设备新会话顶替旧会话(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t)
	q, err := e.dial(t, p.cred)
	must(t, err)
	q.helloOK(t)
	if r := waitClosed(t, p.sess); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonReplaced {
		t.Fatalf("旧会话关闭原因 = %+v", r)
	}
	s, ok := e.srv.Session(p.id)
	if !ok {
		t.Fatal("注册表应保留新会话")
	}
	// 旧会话断开不影响新会话：等 hub 侧旧会话的善后完成后，仍在线、注册表仍指向新会话。
	waitFor(t, "旧会话善后", func() bool {
		e.srv.mu.Lock()
		defer e.srv.mu.Unlock()
		return len(e.srv.conns) == 1
	})
	if e.presence(p.id) != model.PresenceOnline {
		t.Fatalf("顶替后应保持在线，得到 %s", e.presence(p.id))
	}
	if s2, _ := e.srv.Session(p.id); s2 != s {
		t.Fatal("注册表不应被旧会话的断开改动")
	}
	select {
	case <-q.sess.Done():
		t.Fatal("新会话不应被关闭")
	default:
	}
}

func TestPluginsChanged重新入库并重新下发(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t, agent.PluginManifest{Origin: "builtin", Manifest: agentManifestJSON(t, "host-metrics", "1.0.0")})
	waitFor(t, "首次 OnReady", func() bool { return e.router.readyCount(p.id) == 1 })

	must(t, p.sess.Send(&agent.PluginsChanged{Plugins: []agent.PluginManifest{
		{Origin: "exec", Manifest: agentManifestJSON(t, "smart", "0.1.0")},
		{Origin: "http", Manifest: agentManifestJSON(t, "bad-origin", "1.0.0")},
		{Origin: "exec", Manifest: []byte(`{"ID":"Bad ID"}`)},
	}}))
	waitFor(t, "重新下发", func() bool { return e.router.readyCount(p.id) == 2 })
	want := []manifestRow{{"host-metrics", "1.0.0", 0}, {"smart", "0.1.0", 1}}
	rows := e.manifestRows(t, p.id)
	if len(rows) != len(want) || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("manifest 行 = %+v", rows)
	}
	if _, ok := e.reg.MachinePlugin(p.id, "smart"); !ok {
		t.Fatal("可用插件缓存应已更新")
	}
}

func TestHello超时断开(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	e.clk.Advance(HelloTimeout - time.Millisecond)
	select {
	case <-p.sess.Done():
		t.Fatal("未到 10 秒不应断开")
	case <-time.After(20 * time.Millisecond):
	}
	e.clk.Advance(time.Millisecond)
	if r := waitClosed(t, p.sess); r.Kind != agentlink.CloseReadError {
		t.Fatalf("hello 超时应直接断开（不发 bye），得到 %+v", r)
	}
	// 超时后发来的 hello 不再处理。
	if _, ok := e.srv.Session(p.id); ok || e.presence(p.id) == model.PresenceOnline {
		t.Fatal("超时的会话不应注册或在线")
	}
}

func TestHello前收到其他消息断开(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	must(t, p.sess.Send(&agent.PluginsChanged{}))
	if r := waitClosed(t, p.sess); r.Kind != agentlink.CloseReadError {
		t.Fatalf("首条不是 hello 应直接断开，得到 %+v", r)
	}
}

func TestCleanup每分钟清理未确认的占位(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.sess.Close("")
	waitFor(t, "hub 侧会话善后", func() bool {
		e.srv.mu.Lock()
		defer e.srv.mu.Unlock()
		return len(e.srv.conns) == 0
	})
	e.clk.Advance(devices.PlaceholderTTL - time.Minute)
	if _, err := e.devs.Detail(context.Background(), p.id); err != nil {
		t.Fatalf("未满 15 分钟不应清理: %v", err)
	}
	// 清理循环每分钟一轮：逐分钟推进到满 15 分钟。
	waitFor(t, "占位被清理", func() bool {
		e.clk.Advance(time.Minute)
		_, err := e.devs.Detail(context.Background(), p.id)
		return errors.Is(err, devices.ErrNotFound)
	})
}

func TestRevoke断开会话并清除在线状态(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t, agent.PluginManifest{Origin: "builtin", Manifest: agentManifestJSON(t, "host-metrics", "1.0.0")})
	waitFor(t, "在线", func() bool { return e.presence(p.id) == model.PresenceOnline })
	must(t, e.devs.Delete(context.Background(), p.id))
	if r := waitClosed(t, p.sess); r.Bye != agent.ReasonRevoked {
		t.Fatalf("吊销应以 revoked 断开，得到 %+v", r)
	}
	waitFor(t, "在线状态被清除", func() bool { return e.presence(p.id) == "" })
	if _, ok := e.srv.Session(p.id); ok {
		t.Fatal("吊销后注册表不应有会话")
	}
	if _, ok := e.reg.MachinePlugin(p.id, "host-metrics"); ok {
		t.Fatal("吊销后不应再有可用插件")
	}
	// 吊销后凭据失效。
	if _, err := e.dial(t, p.cred); !isAuthFail(err, agent.ReasonBadCredential) {
		t.Fatalf("吊销后凭据应 bad_credential，得到 %v", err)
	}
}

func TestRepair确认时替换旧设备并断开旧会话(t *testing.T) {
	e := newEnv(t)
	old, err := e.pair(t, e.newCode(t, devices.PairingOptions{Name: "nas1"}))
	must(t, err)
	old.helloOK(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{Name: "nas1", RepairDeviceID: old.id}))
	must(t, err)
	if old.sess.Err().Kind != "" {
		t.Fatal("新设备 hello 前旧会话不受影响")
	}
	p.helloOK(t)
	if r := waitClosed(t, old.sess); r.Bye != agent.ReasonRevoked {
		t.Fatalf("旧设备会话应以 revoked 断开，得到 %+v", r)
	}
	if _, err := e.devs.Detail(context.Background(), old.id); !errors.Is(err, devices.ErrNotFound) {
		t.Fatalf("旧设备应被删除: %v", err)
	}
	if d := e.device(t, p.id); d.Name != "nas1" {
		t.Fatalf("新设备应沿用旧名称，得到 %q", d.Name)
	}
	waitFor(t, "旧设备在线状态被清除", func() bool { return e.presence(old.id) == "" })
}

// instanceMachine 读实例行的运行位置。
func instanceMachine(t *testing.T, db *store.DB, id string) string {
	t.Helper()
	var m string
	must(t, db.QueryRowContext(context.Background(), `SELECT machine FROM plugin_instances WHERE id = ?`, id).Scan(&m))
	return m
}

// insertInstance 插入一个运行在 machine 上的实例行。
func insertInstance(t *testing.T, db *store.DB, id, machine string) {
	t.Helper()
	now := store.FormatTime(t0)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO plugin_instances (id, plugin_id, name, machine, config_json, config_hash, created_at, updated_at)
		 VALUES (?, 'host-metrics', ?, ?, '{}', 'h', ?, ?)`, id, id, machine, now, now)
	must(t, err)
}

// Ruling 22：重新配对的 hello 不兼容时只记录原因，不确认、不迁移、不吊销旧设备；占位不随 15 分钟清理删除；
// agent 升级后首个兼容的 hello 才确认并迁移。
func TestRepair遇不兼容保留旧设备且升级后接替(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old, err := e.pair(t, e.newCode(t, devices.PairingOptions{Name: "nas1"}))
	must(t, err)
	old.helloOK(t)
	insertInstance(t, e.db, "i1", old.id)

	e.setHubMin(2)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{Name: "nas1", RepairDeviceID: old.id}))
	must(t, err)
	must(t, p.sess.Send(helloMsg(1, 1)))
	if m, ok := p.recv(t).(*agent.Incompatible); !ok || m.Reason != agent.ReasonVersionTooOld {
		t.Fatalf("应收到 incompatible，得到 %#v", m)
	}
	waitClosed(t, p.sess)

	det, err := e.devs.Detail(ctx, p.id)
	must(t, err)
	if det.Confirmed || det.Replaces != old.id {
		t.Fatalf("占位应保持未确认并保留 replaces: %+v", det)
	}
	if d := e.device(t, p.id); d.Incompatible == nil || d.Incompatible.Reason != agent.ReasonVersionTooOld {
		t.Fatalf("占位应记录不兼容原因: %+v", d)
	}
	if _, err := e.devs.Detail(ctx, old.id); err != nil {
		t.Fatalf("旧设备应原样保留: %v", err)
	}
	if m := instanceMachine(t, e.db, "i1"); m != old.id {
		t.Fatalf("实例不应迁移，machine = %q", m)
	}
	select {
	case <-old.sess.Done():
		t.Fatalf("旧设备会话不应被吊销: %+v", old.sess.Err())
	default:
	}
	// 满 15 分钟的清理不删除它（用时钟靠后的仓库直接清理，避免推进 hub 时钟触发心跳超时）。
	late := devices.New(devices.Config{DB: e.db, Clock: clock.NewFake(t0.Add(devices.PlaceholderTTL + time.Minute))})
	must(t, late.Cleanup(ctx))
	if _, err := e.devs.Detail(ctx, p.id); err != nil {
		t.Fatalf("记录了不兼容原因的占位不应被清理: %v", err)
	}

	// agent 升级后用凭据接入：兼容的 hello 确认并迁移，旧设备被替换、旧会话以 revoked 断开。
	e.setHubMin(agent.MinCompatibleVersion)
	q, err := e.dial(t, p.cred)
	must(t, err)
	q.helloOK(t)
	if r := waitClosed(t, old.sess); r.Bye != agent.ReasonRevoked {
		t.Fatalf("旧设备会话应以 revoked 断开，得到 %+v", r)
	}
	if _, err := e.devs.Detail(ctx, old.id); !errors.Is(err, devices.ErrNotFound) {
		t.Fatalf("旧设备应被删除: %v", err)
	}
	if m := instanceMachine(t, e.db, "i1"); m != p.id {
		t.Fatalf("实例应迁到新设备，machine = %q", m)
	}
	if d := e.device(t, p.id); d.Name != "nas1" || d.Incompatible != nil {
		t.Fatalf("新设备 = %+v", d)
	}
}

// Ruling 23：hello 与 pair 中对端可控的字符串入库前截断到 256 字节且保持 UTF-8 合法。
func TestHello对端字符串入库前截断(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("长", 400) // 1200 字节
	p := &peer{msgs: make(chan agent.Message, 16)}
	cfg := e.dialCfg(p)
	cfg.PairCode = e.newCode(t, devices.PairingOptions{})
	cfg.PairInfo = agent.Pair{Name: "box", Hostname: long}
	cfg.OnCredential = func(ok agent.PairOK) error { p.cred, p.id = ok.Credential, ok.DeviceID; return nil }
	s, err := agentlink.Dial(context.Background(), cfg)
	must(t, err)
	p.sess = s
	t.Cleanup(func() { s.Close("") })
	if d := e.device(t, p.id); len(d.Hostname) > maxPeerField || !utf8.ValidString(d.Hostname) {
		t.Fatalf("配对时的主机名未截断: %d 字节", len(d.Hostname))
	}
	e.waitAttached(t, p.id)

	h := helloMsg(agent.ProtocolVersion, agent.MinCompatibleVersion)
	h.Hostname, h.AgentVersion, h.OS, h.Arch = long, long, long, long
	must(t, p.sess.Send(h))
	if _, ok := p.recv(t).(*agent.HelloOK); !ok {
		t.Fatal("应收到 hello_ok")
	}
	d := e.device(t, p.id)
	for name, v := range map[string]string{"hostname": d.Hostname, "agent_version": d.AgentVersion, "os": d.OS, "arch": d.Arch} {
		if len(v) > maxPeerField || len(v) < maxPeerField-8 || !utf8.ValidString(v) {
			t.Fatalf("%s 应截断到 %d 字节内且保持 UTF-8 合法，得到 %d 字节", name, maxPeerField, len(v))
		}
	}
}

// Ruling 23：Server.Close 之后才被分发的 hello（agentlink 分发协程可能在会话关闭后仍取出队列里的消息）
// 直接以会话已结束了结，不再读写库。
func TestClose之后到达的hello不处理(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	var sess *agentlink.Session
	e.srv.mu.Lock()
	for s := range e.srv.conns {
		if s.Peer().DeviceID == p.id {
			sess = s
		}
	}
	e.srv.mu.Unlock()
	e.srv.Close()

	c := &conn{sess: sess, handled: make(chan struct{}), ready: make(chan struct{})}
	e.srv.handleHello(c, helloMsg(agent.ProtocolVersion, agent.MinCompatibleVersion))
	<-c.ready
	if !errors.Is(c.outcome, ErrSessionClosed) {
		t.Fatalf("Close 之后的 hello 结果 = %v，期望 ErrSessionClosed", c.outcome)
	}
	if d := e.device(t, p.id); d.AgentVersion != "" {
		t.Fatalf("Close 之后的 hello 不应写库: %+v", d)
	}
}

func TestRouter收到业务消息(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t)
	must(t, p.sess.Send(&agent.Event{ID: "e1", Kind: "task"}))
	must(t, p.sess.Send(&agent.Report{InstanceID: "i1", Seq: 1, RunOutcome: agent.RunOutcome{Error: "x", ErrorKind: agent.ErrorKindFailed}}))
	must(t, p.sess.Send(&agent.AssignResult{Version: 3}))
	must(t, p.sess.Send(&agent.UpgradeInfoGet{RequestID: "r1"}))
	waitFor(t, "Router 回调", func() bool {
		e.router.mu.Lock()
		defer e.router.mu.Unlock()
		return len(e.router.reports) == 1 && len(e.router.results) == 1 && len(e.router.upgrade) == 1 && len(e.router.events) == 1
	})
	e.router.mu.Lock()
	defer e.router.mu.Unlock()
	if e.router.reports[0] != p.id+"/i1" || e.router.results[0] != 3 || e.router.upgrade[0] != p.id || e.router.events[0] != p.id+"/e1" {
		t.Fatalf("回调参数不符: %+v", e.router)
	}
}

func TestClockSkew取最近样本中位数(t *testing.T) {
	e := newEnv(t)
	e.agentClk.Advance(time.Hour) // agent 的时钟快 1 小时；两边时钟同步推进，偏移恒为 1 小时
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t)
	if _, ok := e.srv.ClockSkew(p.id); ok {
		t.Fatal("还没有样本")
	}
	hubSess, _ := e.srv.Session(p.id)
	// 先推进 agent 时钟再推进 hub 时钟：hub 发 ping 时 agent 已在新时刻，pong 算出的偏移恰为 1 小时。
	e.agentClk.Advance(SkewInterval)
	e.clk.Advance(SkewInterval) // hub 发出 ping；本轮采样时可能还没有 pong
	waitFor(t, "pong", func() bool { _, ok := hubSess.ClockSkew(); return ok })
	e.agentClk.Advance(SkewInterval)
	e.clk.Advance(SkewInterval)
	waitFor(t, "采样", func() bool { _, ok := e.srv.ClockSkew(p.id); return ok })
	d, _ := e.srv.ClockSkew(p.id)
	if d != time.Hour {
		t.Fatalf("偏移 = %s", d)
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []time.Duration
		want time.Duration
	}{
		{[]time.Duration{5}, 5},
		{[]time.Duration{9, 1, 5}, 5},
		{[]time.Duration{4, 1, 3, 2}, 2}, // 偶数个取两个中间值的平均（2.5 向零截断）
		{[]time.Duration{-10, 100, 0, 1, 2}, 1},
	}
	for _, c := range cases {
		if got := median(c.in); got != c.want {
			t.Fatalf("median(%v) = %v，期望 %v", c.in, got, c.want)
		}
	}
	var s skewSamples
	for i := 1; i <= 7; i++ {
		s.add(time.Duration(i))
	}
	if got, ok := s.median(); !ok || got != 5 { // 只保留最近 5 个：3..7
		t.Fatalf("最近 5 个样本中位数 = %v", got)
	}
}

func TestClose停止后台循环并关闭会话(t *testing.T) {
	e := newEnv(t)
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t)
	e.srv.Close()
	if r := waitClosed(t, p.sess); r.Bye != agent.ReasonShutdown {
		t.Fatalf("Close 应以 shutdown 断开会话，得到 %+v", r)
	}
	e.srv.Close() // 可重复调用
	// 关停后 Attach 的新会话直接以 shutdown 断开。
	q := &peer{msgs: make(chan agent.Message, 16)}
	cfg := e.dialCfg(q)
	cfg.Credential = p.cred
	s, err := agentlink.Dial(context.Background(), cfg)
	must(t, err)
	if r := waitClosed(t, s); r.Bye != agent.ReasonShutdown {
		t.Fatalf("关停后的新会话应以 shutdown 断开，得到 %+v", r)
	}
}

func TestSessionDone随会话断开关闭(t *testing.T) {
	e := newEnv(t)
	if _, ok := e.srv.SessionDone("ghost"); ok {
		t.Fatal("没有会话的设备不应有结束信号")
	}
	p, err := e.pair(t, e.newCode(t, devices.PairingOptions{}))
	must(t, err)
	p.helloOK(t)
	done, ok := e.srv.SessionDone(p.id)
	if !ok {
		t.Fatal("hello 被接受后应有结束信号")
	}
	select {
	case <-done:
		t.Fatal("会话未断开时信号不应关闭")
	default:
	}
	must(t, e.devs.Delete(context.Background(), p.id))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("吊销后结束信号应关闭")
	}
}
