package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// testAgent 是经真实 agent 通道接入的替身 agent：记录收到的 assign。
type testAgent struct {
	sess    *agentlink.Session
	cred    string
	id      string
	assigns chan *agent.Assign
}

// dialAgent 用配对码（code 非空）或长期凭据接入 addr。
func dialAgent(t *testing.T, addr string, pin certs.SPKI, code, cred string) (*testAgent, error) {
	t.Helper()
	ta := &testAgent{cred: cred, assigns: make(chan *agent.Assign, 8)}
	if cred != "" {
		ta.id = agent.DeviceID(cred)
	}
	cfg := agentlink.DialerConfig{
		URL: "wss://" + addr, Pin: pin, Role: agent.RoleAgent, Log: agentlinktest.QuietLog(),
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(_ *agentlink.Session, m agent.Message) {
				if as, ok := m.(*agent.Assign); ok {
					ta.assigns <- as
				}
			}
		},
	}
	if code != "" {
		cfg.PairCode = code
		cfg.PairInfo = agent.Pair{Name: "nas1", Hostname: "nas.local", OS: "linux", Arch: "amd64"}
		cfg.OnCredential = func(ok agent.PairOK) error { ta.cred, ta.id = ok.Credential, ok.DeviceID; return nil }
	} else {
		cfg.Credential = cred
	}
	s, err := agentlink.Dial(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	ta.sess = s
	t.Cleanup(func() { s.Close("") })
	return ta, nil
}

// hello 发送带 host-metrics manifest 的兼容 hello。
func (ta *testAgent) hello(t *testing.T) {
	t.Helper()
	src, ok := runtime.Builtin("host-metrics")
	if !ok {
		t.Fatal("host-metrics 应已注册")
	}
	raw, err := json.Marshal(src.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.sess.Send(&agent.Hello{
		ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion,
		AgentVersion: "0.2.0", OS: "linux", Arch: "amd64", Hostname: "nas.local",
		Plugins: []agent.PluginManifest{{Origin: "builtin", Manifest: raw}},
	}); err != nil {
		t.Fatal(err)
	}
}

// nextAssign 等下一条 assign。
func (ta *testAgent) nextAssign(t *testing.T) *agent.Assign {
	t.Helper()
	select {
	case as := <-ta.assigns:
		return as
	case <-time.After(5 * time.Second):
		t.Fatal("等待 assign 超时")
		return nil
	}
}

func pairingCode(t *testing.T, c *http.Client, base, path string, body any) model.PairingResult {
	t.Helper()
	resp, data := call(t, c, base, "POST", path, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%s = %d %s", path, resp.StatusCode, data)
	}
	var res model.PairingResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func devicePresence(t *testing.T, c *http.Client, base, id string) string {
	t.Helper()
	resp, data := call(t, c, base, "GET", "/api/devices/"+id, nil)
	var dev model.Device
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &dev) != nil {
		t.Fatalf("设备详情 = %d %s", resp.StatusCode, data)
	}
	return dev.Presence
}

// 完成判定第 3 条（拨入）：配对 → 下发 → 报告入库 → 断开满 30 秒（及 60 秒）显示离线 → 重连恢复并重新下发 →
// 重新配对确认后迁移的实例下发到新设备、旧凭据失效 → 吊销后无法重连。时间全部用假时钟推进。
func TestDevice生命周期端到端(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cfg := testConfig(t)
	cfg.AgentAddr = "127.0.0.1:0"
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithClock(clk), WithOnAgentListen(func(addr string) { addrCh <- addr }))
	c, base := setupAdmin(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve 退出超时")
		}
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 agent 通道监听超时")
	}
	bg := context.Background()
	online := func(id string) func() bool {
		return func() bool { st, _ := a.presence.State(id); return st == model.PresenceOnline }
	}

	// 拨入配对 → 首个 hello 自动创建 host-metrics 并全量下发。
	res := pairingCode(t, c, base, "/api/devices/pairing", map[string]any{"name": "nas1", "role": "nas"})
	pin, err := certs.ParseSPKI(res.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := dialAgent(t, addr, pin, res.Code, "")
	if err != nil {
		t.Fatalf("配对: %v", err)
	}
	ag.hello(t)
	waitUntil(t, "设备 online", online(ag.id))
	as := ag.nextAssign(t)
	if len(as.Instances) != 1 || as.Instances[0].PluginID != "host-metrics" {
		t.Fatalf("首次下发应含自动创建的 host-metrics 实例: %+v", as.Instances)
	}
	instID := as.Instances[0].ID

	// 报告入库。
	if err := ag.sess.Send(&agent.Report{InstanceID: instID, Seq: 1, RunOutcome: agent.RunOutcome{
		Report: &report.Report{Status: report.StatusOK, Summary: "第一次"}, CollectedAt: clk.Now(),
		ConfigHash: as.Instances[0].ConfigHash,
	}}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "报告入库", func() bool {
		d, err := a.instances.Get(bg, instID)
		return err == nil && d.Report != nil && d.Report.Summary == "第一次"
	})

	// 断开：未满 30 秒为 disconnected，满 30 秒转 offline，60 秒仍为 offline。
	ag.sess.Close("")
	waitUntil(t, "断开", func() bool { st, _ := a.presence.State(ag.id); return st == model.PresenceDisconnected })
	clk.Advance(devices.OfflineAfter - time.Second)
	if p := devicePresence(t, c, base, ag.id); p != model.PresenceDisconnected {
		t.Fatalf("断开 29 秒应为 disconnected，得到 %q", p)
	}
	clk.Advance(time.Second)
	waitUntil(t, "满 30 秒离线", func() bool { st, _ := a.presence.State(ag.id); return st == model.PresenceOffline })
	if p := devicePresence(t, c, base, ag.id); p != model.PresenceOffline {
		t.Fatalf("断开 30 秒应为 offline，得到 %q", p)
	}
	clk.Advance(30 * time.Second)
	if p := devicePresence(t, c, base, ag.id); p != model.PresenceOffline {
		t.Fatalf("断开 60 秒应为 offline，得到 %q", p)
	}

	// 用凭据重连：恢复在线并重新全量下发。
	re, err := dialAgent(t, addr, pin, "", ag.cred)
	if err != nil {
		t.Fatalf("重连: %v", err)
	}
	re.hello(t)
	waitUntil(t, "重连后 online", online(ag.id))
	if p := devicePresence(t, c, base, ag.id); p != model.PresenceOnline {
		t.Fatalf("重连后应为 online，得到 %q", p)
	}
	if as := re.nextAssign(t); len(as.Instances) != 1 || as.Instances[0].ID != instID {
		t.Fatalf("重连后应重新下发实例: %+v", as.Instances)
	}

	// 重新配对：新设备确认后接替旧设备，迁移的实例下发到新设备；旧会话被断开、旧凭据失效。
	rp := pairingCode(t, c, base, "/api/devices/"+ag.id+"/repair", nil)
	nw, err := dialAgent(t, addr, pin, rp.Code, "")
	if err != nil {
		t.Fatalf("重新配对: %v", err)
	}
	nw.hello(t)
	waitUntil(t, "新设备 online", online(nw.id))
	if as := nw.nextAssign(t); len(as.Instances) != 1 || as.Instances[0].ID != instID {
		t.Fatalf("迁移的实例应下发到新设备: %+v", as.Instances)
	}
	if d, err := a.instances.Get(bg, instID); err != nil || d.RunsOn != nw.id {
		t.Fatalf("实例应迁到新设备: %+v %v", d, err)
	}
	select {
	case <-re.sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("重新配对确认后旧设备会话应被断开")
	}
	if _, err := dialAgent(t, addr, pin, "", ag.cred); !isBadCredential(err) {
		t.Fatalf("旧设备凭据应失效，得到 %v", err)
	}

	// 吊销后无法重连。
	if resp, data := call(t, c, base, "DELETE", "/api/devices/"+nw.id, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("吊销 = %d %s", resp.StatusCode, data)
	}
	if _, err := dialAgent(t, addr, pin, "", nw.cred); !isBadCredential(err) {
		t.Fatalf("吊销后凭据应失效，得到 %v", err)
	}
}

func isBadCredential(err error) bool {
	var af *agentlink.AuthFailError
	return errors.As(err, &af) && af.Reason == agent.ReasonBadCredential
}
