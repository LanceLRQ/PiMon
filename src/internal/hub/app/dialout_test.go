package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 拨出配对经 API 完成；Serve 启动拨出管理器，配对会话结束后经设备代理重连；代理删除经扇出让设备记 proxy_removed。
func TestDialOut经API配对且代理变化扇出到拨出管理器(t *testing.T) {
	cfg := testConfig(t)
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithOnListen(func(addr string) { addrCh <- addr }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve = %v", err)
			}
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

	c := newClient()
	code, _, err := a.SetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp, data := call(t, c, base, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": testPassword, "language": "zh", "timezone": "UTC", "access_url": "",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}

	// agent 监听方替身：会话建立后主动发 hello。
	auth := agentlinktest.NewStaticAuth()
	auth.AddPairCode("PAIR-1")
	ln := agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Role: agent.RoleAgent, Auth: auth, Clock: clock.NewFake(time.Now()),
		OnSession: func(s *agentlink.Session) {
			_ = s.Send(&agent.Hello{
				ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion,
				AgentVersion: "0.2.0", OS: "linux", Arch: "amd64", Hostname: "vps.example",
			})
		},
	})
	socks := proxytest.NewSocks5Forward(t, "", "")
	resp, data := call(t, c, base, "POST", "/api/proxies", map[string]any{"name": "s5", "scheme": "socks5", "address": socks.Addr})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建代理 = %d %s", resp.StatusCode, data)
	}
	var px model.Proxy
	if err := json.Unmarshal(data, &px); err != nil {
		t.Fatal(err)
	}

	resp, data = call(t, c, base, "POST", "/api/devices/dial-out", map[string]any{
		"address": ln.URL, "fingerprint": ln.Pin.String(), "code": "PAIR-1", "name": "vps1", "proxy_id": px.ID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("拨出配对 = %d %s", resp.StatusCode, data)
	}
	var dev model.Device
	if err := json.Unmarshal(data, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Name != "vps1" || dev.Direction != model.DeviceDialOut || dev.ProxyID != px.ID || dev.Presence != model.PresenceOnline {
		t.Fatalf("设备 = %+v", dev)
	}

	// 配对会话结束后由 Serve 启动的拨出管理器接手重连。
	old, ok := a.agents.Session(dev.ID)
	if !ok {
		t.Fatal("配对成功后应有会话")
	}
	old.Close("")
	waitUntil(t, "重连上线", func() bool {
		s, ok := a.agents.Session(dev.ID)
		return ok && s != old
	})

	resp, data = call(t, c, base, "DELETE", "/api/proxies/"+px.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除代理 = %d %s", resp.StatusCode, data)
	}
	waitUntil(t, "代理删除后记 proxy_removed", func() bool {
		return a.dialout.ConnError(dev.ID) == agentsrv.ConnErrProxyRemoved
	})
}
