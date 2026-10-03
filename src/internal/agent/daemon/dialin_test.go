package daemon

import (
	"context"
	"errors"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agent/hubtest"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

// pairedHub 用真实 hub 走一次 link.PairDialIn（凭据落盘到 h 的配置），返回凭据与设备 ID。
func pairedHub(t *testing.T, hub *hubtest.Hub, h *harness) (cred, id string) {
	t.Helper()
	res, err := link.PairDialIn(context.Background(), link.PairRequest{
		HubURL: hub.URL, Pin: hub.Pin, Code: hub.NewCode(t, "nas"), Name: "nas",
		Save: func(ok agent.PairOK) error {
			cred = ok.Credential
			return config.Save(h.paths.Config, config.Config{
				Mode: config.ModeDialIn, HubURL: hub.URL, HubPin: hub.Pin.String(),
				Credential: config.Credential(ok.Credential), HubHostname: ok.ListenerInfo.Hostname,
			})
		},
		Hello: func() agent.Hello { return link.NewHello("agent-box", nil) },
		Clock: h.clk, Log: agentlinktest.QuietLog(),
	})
	if err != nil {
		t.Fatalf("配对失败: %v", err)
	}
	if !hub.Confirmed(t, res.DeviceID) {
		t.Fatal("配对后 hub 占位应已确认")
	}
	return cred, res.DeviceID
}

func connectedHub(t *testing.T) (*hubtest.Hub, *harness, string, string) {
	t.Helper()
	hub := hubtest.Start(t)
	h := newHarness(t, nil)
	cred, id := pairedHub(t, hub, h)
	h.start()
	waitUntil(t, "守护进程连上 hub", func() bool { return h.status().Connected && hub.Online(id) })
	return hub, h, cred, id
}

func ringContains(h *harness, s string) bool {
	for _, e := range h.ring.Entries(-100, RingCapacity) {
		if strings.Contains(e.Message, s) || strings.Contains(e.Attrs, s) {
			return true
		}
	}
	return false
}

func TestDialIn_配对后守护进程连接且hello带manifest(t *testing.T) {
	hub, h, cred, id := connectedHub(t)
	ps, err := hub.Plugins.MachinePlugins(context.Background(), id)
	if err != nil || len(ps) != 1 || ps[0].Manifest.ID != "probe-x" {
		t.Fatalf("hub 应收到守护进程 hello 携带的 manifest: %v %+v", err, ps)
	}
	st := h.status()
	if st.State != StateRunning || st.Mode != config.ModeDialIn || st.HubURL != hub.URL || st.Pin != hub.Pin.String() ||
		!st.Since.Equal(t0) || st.Version != version.Version || st.ProtocolVersion != agent.ProtocolVersion ||
		st.MinCompatibleVersion != agent.MinCompatibleVersion || st.LastError != "" {
		t.Fatalf("状态 = %+v", st)
	}
	h.assertNoSecret(cred)
	if ringContains(h, cred) {
		t.Fatal("日志里出现了凭据")
	}
}

func TestDialIn_会话断开后用凭据重连(t *testing.T) {
	hub, h, _, id := connectedHub(t)
	s, ok := hub.Server.Session(id)
	if !ok {
		t.Fatal("hub 侧没有会话")
	}
	s.Close("")
	waitUntil(t, "断开", func() bool { return !h.status().Connected })
	waitUntil(t, "重连", func() bool {
		h.clk.Advance(500 * time.Millisecond)
		return h.status().Connected && hub.Online(id)
	})
}

func TestDialIn_吊销后清空凭据不再重连且守护进程不退出(t *testing.T) {
	hub, h, cred, id := connectedHub(t)
	hub.Revoke(t, id)
	waitUntil(t, "状态变为已吊销", func() bool { return h.status().State == StateRevoked })
	st := h.status()
	if st.Connected || !strings.Contains(st.LastError, "已被吊销") {
		t.Fatalf("状态 = %+v", st)
	}
	if c := h.diskConfig(); c.Credential != "" || c.HubURL != hub.URL {
		t.Fatalf("吊销后应只清空凭据: %v", c)
	}
	calls := hub.Auth.Calls()
	for range 3 {
		h.clk.Advance(60 * time.Second)
	}
	never(t, "吊销后再次认证", func() bool { return hub.Auth.Calls() != calls })
	if !h.running() {
		t.Fatal("吊销后守护进程应保持空闲，不退出")
	}
	h.assertNoSecret(cred)
}

func TestDialIn_凭据无效只显示状态不删凭据(t *testing.T) {
	hub := startStubHub(t, nil)
	conf := hub.config()
	other, _ := agent.NewCredential()
	conf.Credential = config.Credential(other)
	h := newHarness(t, conf).start()
	waitUntil(t, "状态变为凭据无效", func() bool { return h.status().State == StateBadCredential })
	if st := h.status(); !strings.Contains(st.LastError, "凭据无效") {
		t.Fatalf("状态 = %+v", st)
	}
	if c := h.diskConfig(); string(c.Credential) != other {
		t.Fatal("bad_credential 不应删除凭据")
	}
	if !h.running() {
		t.Fatal("守护进程不应退出")
	}
}

func TestDialIn_不兼容记录原因并关闭(t *testing.T) {
	hub := startStubHub(t, func(s *agentlink.Session, _ *agent.Hello) {
		_ = s.Send(&agent.Incompatible{Reason: agent.ReasonVersionTooOld, RequiredMin: 2, HubVersion: "0.9.0"})
	})
	h := newHarness(t, hub.config()).start()
	s := hub.nextSession()
	<-s.Done()
	if r := s.Err(); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonVersionTooOld {
		t.Fatalf("agent 应以版本原因 Close，hub 侧看到 %+v", r)
	}
	waitUntil(t, "状态记录不兼容", func() bool { return h.status().State == StateIncompatible })
	st := h.status()
	if st.Incompatible == nil || *st.Incompatible != (Incompatible{Reason: agent.ReasonVersionTooOld, RequiredMin: 2, HubVersion: "0.9.0"}) || st.Connected {
		t.Fatalf("状态 = %+v", st)
	}
}

func TestDialIn_plugins_changed只在hello_ok之后发送(t *testing.T) {
	hold := make(chan struct{})
	hub := startStubHub(t, func(s *agentlink.Session, _ *agent.Hello) {
		go func() {
			<-hold
			_ = s.Send(&agent.HelloOK{HubVersion: "0.2.0", ServerTime: t0})
		}()
	})
	h := newHarness(t, hub.config()).start()
	if m, ok := hub.next().(*agent.Hello); !ok || len(m.Plugins) != 1 {
		t.Fatalf("首条消息应为带 manifest 的 hello，得到 %#v", m)
	}
	h.d.reg.Scan() // hello_ok 之前的目录变化
	never(t, "hello_ok 之前发出 plugins_changed", func() bool { return len(hub.msgs) > 0 })
	close(hold)
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	if _, ok := hub.next().(*agent.PluginsChanged); !ok {
		t.Fatal("hello 发出后、hello_ok 前的变化应在 hello_ok 后补发一次 plugins_changed")
	}
	h.d.reg.Scan()
	if m, ok := hub.next().(*agent.PluginsChanged); !ok || len(m.Plugins) != 1 {
		t.Fatalf("就绪后的变化应立即发 plugins_changed，得到 %#v", m)
	}
}

func TestDialIn_无会话时插件变化被丢弃(t *testing.T) {
	_, pin := agentlinktest.NewCert(t)
	h := newHarness(t, &config.Config{Mode: config.ModeDialIn, HubURL: "wss://127.0.0.1:1",
		HubPin: pin.String(), Credential: "c"}).start()
	waitUntil(t, "运行中", func() bool { return h.status().State == StateRunning })
	h.d.reg.Scan() // 不应 panic，也没有会话可发
}

func TestDialIn_assign与run_now经运行器处理且logs_get可用(t *testing.T) {
	hub := startStubHub(t, nil)
	h := newHarness(t, hub.config()).start()
	s := hub.nextSession()
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	h.d.log.Warn("测试告警")
	must(t, s.Send(&agent.Assign{Version: 9, Instances: []agent.AssignedInstance{
		{ID: "i1", PluginID: "probe-x", IntervalMs: 60000, ConfigHash: "h1", Secrets: map[string]string{"k": "topsecret"}},
	}}))
	for {
		if r, ok := hub.next().(*agent.AssignResult); ok {
			if r.Version != 9 || len(r.Results) != 1 || r.Results[0].Status != agent.AssignStarted {
				t.Fatalf("assign_result = %+v", r)
			}
			break
		}
	}
	if st := h.status(); st.Instances != 1 {
		t.Fatalf("实例数 = %d", st.Instances)
	}
	// 首次周期运行（抖动为 0）可能正占着运行锁，run_now 会回 busy，重试到成功为止。
	waitUntil(t, "run_now 成功", func() bool {
		resp, err := s.Request(context.Background(), &agent.RunNow{InstanceID: "i1"}, 0)
		rr, ok := resp.(*agent.RunResult)
		if err != nil || !ok || (rr.Report == nil && rr.ErrorKind != agent.ErrorKindBusy) {
			t.Fatalf("run_now: %v %#v", err, resp)
		}
		return rr.Report != nil
	})
	resp, err := s.Request(context.Background(), &agent.LogsGet{MinLevel: "warn"}, 0)
	logs, ok := resp.(*agent.Logs)
	if err != nil || !ok || len(logs.Entries) == 0 {
		t.Fatalf("logs: %v %#v", err, resp)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Level != "warn" && e.Level != "error" {
			t.Fatalf("按级别过滤失败: %+v", e)
		}
		found = found || e.Message == "测试告警"
		if strings.Contains(e.Attrs, "topsecret") || strings.Contains(e.Message, "topsecret") {
			t.Fatal("日志里出现了插件密钥")
		}
	}
	if !found {
		t.Fatal("logs_get 应返回刚写的告警")
	}
}

func TestDialIn_升级信息取自当前会话(t *testing.T) {
	_, h, _, _ := connectedHub(t)
	ui, err := h.d.UpgradeInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"linux-amd64": "aa11", "linux-arm64": "bb22", "darwin-arm64": "cc33"}[goruntime.GOOS+"-"+goruntime.GOARCH]
	if ui.HubVersion != hubtest.HubVersion || ui.WebPort != 31415 || ui.SHA256 != want || ui.HubHost != "127.0.0.1" {
		t.Fatalf("升级信息 = %+v", ui)
	}
}

func TestDialIn_未连接时升级信息报错(t *testing.T) {
	h := newHarness(t, nil).start()
	if _, err := h.d.UpgradeInfo(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("应返回 ErrNotConnected，得到 %v", err)
	}
}

func TestDialIn_repin后用新指纹重连(t *testing.T) {
	hub := startStubHub(t, nil)
	conf := hub.config()
	_, wrong := agentlinktest.NewCert(t)
	conf.HubPin = wrong.String()
	h := newHarness(t, conf).start()
	waitUntil(t, "记录指纹不符", func() bool { return strings.Contains(h.status().LastError, "指纹") })
	must(t, h.d.Repin(context.Background(), hub.srv.Pin))
	if c := h.diskConfig(); c.HubPin != hub.srv.Pin.String() {
		t.Fatalf("repin 应落盘: %v", c)
	}
	waitUntil(t, "用新指纹连上", func() bool { return h.status().Connected })
	if st := h.status(); st.Pin != hub.srv.Pin.String() || st.LastError != "" {
		t.Fatalf("状态 = %+v", st)
	}
}

func TestDialIn_拨出模式不能repin(t *testing.T) {
	h := newHarness(t, &config.Config{Mode: config.ModeDialOut}).start()
	h.listenAddr()
	_, pin := agentlinktest.NewCert(t)
	if err := h.d.Repin(context.Background(), pin); !errors.Is(err, ErrNotDialIn) {
		t.Fatalf("应返回 ErrNotDialIn，得到 %v", err)
	}
}

func TestDialIn_直拨失败时按hub主机名查mDNS(t *testing.T) {
	_, pin := agentlinktest.NewCert(t)
	h := newHarness(t, &config.Config{Mode: config.ModeDialIn, HubURL: "wss://127.0.0.1:1",
		HubPin: pin.String(), Credential: "c", HubHostname: "pimon-hub.lan"}).start()
	waitUntil(t, "调用 mDNS 兜底", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.mdnsName) > 0 })
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mdnsName[0] != "pimon-hub.local" {
		t.Fatalf("mDNS 查询名 = %q", h.mdnsName[0])
	}
}

func TestReload_配置错误时空闲不退出_修正后重载即连接(t *testing.T) {
	hub := startStubHub(t, nil)
	h := newHarness(t, nil).start()
	waitUntil(t, "配置错误", func() bool { return h.status().State == StateConfigError })
	if !h.running() || !strings.Contains(h.status().LastError, "配置") {
		t.Fatalf("状态 = %+v", h.status())
	}
	must(t, config.Save(h.paths.Config, *hub.config()))
	must(t, h.d.Reload(context.Background()))
	waitUntil(t, "重载后连上", func() bool { return h.status().Connected })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
