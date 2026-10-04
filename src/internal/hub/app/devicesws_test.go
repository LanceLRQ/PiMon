package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// pumpWS 在后台读取连接上的全部消息（读取不带超时，避免超时取消把连接关掉）。
func pumpWS(conn *websocket.Conn) <-chan map[string]any {
	ch := make(chan map[string]any, 64)
	go func() {
		defer close(ch)
		for {
			_, b, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				ch <- m
			}
		}
	}()
	return ch
}

// nextDevicePatch 每次推进一个合并窗口后等待消息，直到收到满足条件的设备 patch。
// 触发变化的回调在异步路径上时，登记可能晚于第一次推进，所以逐窗口推进而不是只推进一次。
func nextDevicePatch(t *testing.T, msgs <-chan map[string]any, clk *clock.Fake, match func(m map[string]any) bool) map[string]any {
	t.Helper()
	// 上限小于对账周期（15 秒）：靠对账兜底会掩盖回调没有接线的问题。
	for range 12 {
		clk.Advance(time.Second)
		timer := time.After(200 * time.Millisecond)
	drain:
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					t.Fatal("WebSocket 连接已关闭")
				}
				if (m["entity"] == ui.EntityDevice || m["entity"] == ui.EntityDeviceRemoved) && match(m) {
					return m
				}
			case <-timer:
				break drain
			}
		}
	}
	t.Fatal("等待设备 patch 超时")
	return nil
}

func devOf(m map[string]any) map[string]any {
	d, _ := m["device"].(map[string]any)
	return d
}

// 经真实中枢：管理员订阅 devices 后，拨出配对上线、在线状态变化、连接错误变化、属性修改、吊销都推成 patch。
func TestDevicesWS设备变化经三个来源推送(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	addrCh := make(chan string, 1)
	a := openApp(t, testConfig(t), WithClock(clk), WithOnListen(func(addr string) { addrCh <- addr }))
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
	var base string
	select {
	case addr := <-addrCh:
		base = "http://" + addr
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待监听超时")
	}
	admin := newClient()
	code, _, err := a.SetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp, data := call(t, admin, base, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": testPassword, "language": "zh", "timezone": "UTC", "access_url": "",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}

	f := &screenFixture{t: t, a: a, clk: clk, base: base, admin: admin}
	conn, _ := f.dial(admin)
	f.send(conn, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicInstances, ui.TopicDevices}})
	snap := readWS(t, conn)
	if list, ok := snap["devices"].([]any); !ok || len(list) != 0 {
		t.Fatalf("没有设备时 snapshot 的 devices 应为空数组: %v", snap["devices"])
	}
	msgs := pumpWS(conn)

	agentAuth := agentlinktest.NewStaticAuth()
	agentAuth.AddPairCode("PAIR-1")
	ln := agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleAgent, Auth: agentAuth, Clock: clock.NewFake(time.Now()),
		OnSession: func(s *agentlink.Session) {
			_ = s.Send(&agent.Hello{
				ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion,
				AgentVersion: "0.2.0", OS: "linux", Arch: "amd64", Hostname: "vps.example",
			})
		},
	})
	socks := proxytest.NewSocks5Forward(t, "", "")
	resp, data := call(t, admin, base, "POST", "/api/proxies", map[string]any{"name": "s5", "scheme": "socks5", "address": socks.Addr})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建代理 = %d %s", resp.StatusCode, data)
	}
	var px model.Proxy
	if err := json.Unmarshal(data, &px); err != nil {
		t.Fatal(err)
	}
	resp, data = call(t, admin, base, "POST", "/api/devices/dial-out", map[string]any{
		"address": ln.URL, "fingerprint": ln.Pin.String(), "code": "PAIR-1", "name": "vps1", "proxy_id": px.ID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("拨出配对 = %d %s", resp.StatusCode, data)
	}
	var dev model.Device
	if err := json.Unmarshal(data, &dev); err != nil {
		t.Fatal(err)
	}

	// 设备仓库变更与在线状态：上线后推出 online。
	nextDevicePatch(t, msgs, clk, func(m map[string]any) bool {
		d := devOf(m)
		return d["id"] == dev.ID && d["presence"] == model.PresenceOnline && d["name"] == "vps1"
	})

	// 在线状态来源：状态变化即推送。
	a.presence.Disconnected(dev.ID, clk.Now())
	nextDevicePatch(t, msgs, clk, func(m map[string]any) bool {
		d := devOf(m)
		return d["id"] == dev.ID && d["presence"] == model.PresenceDisconnected
	})

	// 连接错误来源：代理被删除后拨出管理器记 proxy_removed，没有设备仓库或在线状态的变化也要推出来。
	if resp, data = call(t, admin, base, "DELETE", "/api/proxies/"+px.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除代理 = %d %s", resp.StatusCode, data)
	}
	nextDevicePatch(t, msgs, clk, func(m map[string]any) bool {
		d := devOf(m)
		return d["id"] == dev.ID && d["conn_error"] == "proxy_removed"
	})

	// 属性修改。
	resp, data = call(t, admin, base, "PUT", "/api/devices/"+dev.ID, map[string]any{"name": "vps-renamed", "role": dev.Role, "allow_offline": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT 设备 = %d %s", resp.StatusCode, data)
	}
	nextDevicePatch(t, msgs, clk, func(m map[string]any) bool {
		d := devOf(m)
		return d["id"] == dev.ID && d["name"] == "vps-renamed"
	})

	// 吊销。
	if resp, data = call(t, admin, base, "DELETE", "/api/devices/"+dev.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("吊销 = %d %s", resp.StatusCode, data)
	}
	nextDevicePatch(t, msgs, clk, func(m map[string]any) bool {
		return m["entity"] == ui.EntityDeviceRemoved && m["id"] == dev.ID
	})
}
