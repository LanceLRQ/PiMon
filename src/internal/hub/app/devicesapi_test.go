package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 经 REST 生成配对码，agent 按返回的指纹与配对码接入；吊销接口返回时 hub 侧会话已断开、在线状态已清除。
func TestDevices配对接入后吊销返回时会话已断开(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentAddr = "127.0.0.1:0"
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithOnAgentListen(func(addr string) { addrCh <- addr }))
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
	_, port, _ := net.SplitHostPort(addr)

	resp, data := call(t, c, base, "POST", "/api/devices/pairing", map[string]any{"name": "nas1", "role": "nas"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("pairing = %d %s", resp.StatusCode, data)
	}
	var res model.PairingResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Fingerprint != a.AgentSPKI().String() || len(res.URLs) == 0 || !strings.HasSuffix(res.URLs[0], ":"+port) ||
		!strings.Contains(res.Command, "--fp "+res.Fingerprint) || !strings.Contains(res.Command, "--code "+res.Code) {
		t.Fatalf("配对结果 = %+v（监听 %s）", res, addr)
	}

	pin, err := certs.ParseSPKI(res.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	var deviceID string
	sess, err := agentlink.Dial(context.Background(), agentlink.DialerConfig{
		URL: "wss://" + addr, Pin: pin, Role: agent.RoleAgent, Log: agentlinktest.QuietLog(),
		PairCode: res.Code, PairInfo: agent.Pair{Name: "nas1", Hostname: "nas.local", OS: "linux", Arch: "amd64"},
		OnCredential: func(ok agent.PairOK) error { deviceID = ok.DeviceID; return nil },
	})
	if err != nil {
		t.Fatalf("按配对命令里的指纹与配对码接入: %v", err)
	}
	defer sess.Close("")
	if err := sess.Send(&agent.Hello{
		ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion,
		AgentVersion: "0.2.0", OS: "linux", Arch: "amd64", Hostname: "nas.local",
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "设备 online", func() bool {
		st, _ := a.presence.State(deviceID)
		return st == model.PresenceOnline
	})
	resp, data = call(t, c, base, "GET", "/api/devices/"+deviceID, nil)
	var dev model.Device
	if err := json.Unmarshal(data, &dev); err != nil || resp.StatusCode != http.StatusOK || dev.Presence != model.PresenceOnline || dev.Name != "nas1" {
		t.Fatalf("设备详情 = %d %s", resp.StatusCode, data)
	}

	hubSide, ok := a.agents.SessionDone(deviceID)
	if !ok {
		t.Fatal("hello 被接受后应有会话")
	}
	if resp, data = call(t, c, base, "DELETE", "/api/devices/"+deviceID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("吊销 = %d %s", resp.StatusCode, data)
	}
	select {
	case <-hubSide:
	default:
		t.Fatal("吊销返回时 hub 侧会话应已断开")
	}
	if st, _ := a.presence.State(deviceID); st != "" {
		t.Fatalf("吊销返回时在线状态应已清除，得到 %q", st)
	}
	if resp, _ = call(t, c, base, "GET", "/api/devices/"+deviceID, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("吊销后详情 = %d", resp.StatusCode)
	}
}
