package app

import (
	"context"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// waitUntil 有上限地轮询条件，不依赖固定 sleep 时长。
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAgent通道配对后设备在线(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentAddr = "127.0.0.1:0"
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithOnAgentListen(func(addr string) { addrCh <- addr }))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 agent 通道监听超时")
	}
	if got := a.AgentAddr(); got != addr {
		t.Fatalf("AgentAddr = %q，期望 %q", got, addr)
	}

	code, _, err := a.devices.NewPairingCode(context.Background(), devices.PairingOptions{Name: "nas1", Role: model.DeviceRoleNAS})
	if err != nil {
		t.Fatal(err)
	}
	var deviceID string
	sess, err := agentlink.Dial(context.Background(), agentlink.DialerConfig{
		URL: "wss://" + addr, Pin: a.AgentSPKI(), Role: agent.RoleAgent, Log: agentlinktest.QuietLog(),
		PairCode: code,
		PairInfo: agent.Pair{Name: "nas1", Hostname: "nas.local", OS: "linux", Arch: "amd64"},
		OnCredential: func(ok agent.PairOK) error {
			deviceID = ok.DeviceID
			return nil
		},
	})
	if err != nil {
		t.Fatalf("配对: %v", err)
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
	d, err := a.devices.Get(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("设备行应已出现: %v", err)
	}
	if d.Name != "nas1" || d.Hostname != "nas.local" {
		t.Fatalf("设备行不符: %+v", d)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("退出超时")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAgent通道端口被占用时错误带地址(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	cfg := testConfig(t)
	cfg.AgentAddr = hold.Addr().String()
	a := openApp(t, cfg)
	err = a.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), cfg.AgentAddr) {
		t.Fatalf("err = %v，应包含地址 %s", err, cfg.AgentAddr)
	}
}

func TestAgent通道空地址不启用(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentAddr = ""
	called := false
	a := openApp(t, cfg, WithOnAgentListen(func(string) { called = true }))
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	a.opts.onListen = func(addr string) { addrCh <- addr }
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	select {
	case <-addrCh:
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待监听超时")
	}
	cancel()
	<-done
	if called || a.AgentAddr() != "" {
		t.Fatalf("空地址不应启用 agent 通道: called=%v addr=%q", called, a.AgentAddr())
	}
}

func TestAgent通道关停后不泄漏协程(t *testing.T) {
	before := runtime.NumGoroutine()
	cfg := testConfig(t)
	cfg.AgentAddr = "127.0.0.1:0"
	addrCh := make(chan string, 1)
	a, err := Open(context.Background(), cfg, testOpts(WithOnAgentListen(func(addr string) { addrCh <- addr }))...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	select {
	case <-addrCh:
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待监听超时")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "协程数回落", func() bool { return runtime.NumGoroutine() <= before })
}
