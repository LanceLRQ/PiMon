package app

import (
	"context"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
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

// logCapture 收集日志记录，用于断言关停期间没有出现数据库已关闭之类的错误。
type logCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	line := r.Level.String() + " " + r.Message
	r.Attrs(func(a slog.Attr) bool { line += " " + a.Key + "=" + a.Value.String(); return true })
	c.mu.Lock()
	c.msgs = append(c.msgs, line)
	c.mu.Unlock()
	return nil
}
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }
func (c *logCapture) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.msgs...)
}

// settleGoroutines 等协程数不再下降（前序测试的遗留协程退出完毕）后返回当前数量，等待有上限。
func settleGoroutines() int {
	deadline := time.Now().Add(3 * time.Second)
	last, stable := runtime.NumGoroutine(), 0
	for stable < 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if n := runtime.NumGoroutine(); n < last {
			last, stable = n, 0
		} else {
			stable++
		}
	}
	return last
}

func TestAgent通道配对后设备在线且关停无泄漏(t *testing.T) {
	logs := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(prev) })
	baseline := settleGoroutines()

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
	assigns := make(chan *agent.Assign, 4)
	sess, err := agentlink.Dial(context.Background(), agentlink.DialerConfig{
		URL: "wss://" + addr, Pin: a.AgentSPKI(), Role: agent.RoleAgent, Log: agentlinktest.QuietLog(),
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(_ *agentlink.Session, m agent.Message) {
				if as, ok := m.(*agent.Assign); ok {
					assigns <- as
				}
			}
		},
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
	// 下发器已接入 agent 通道：上线后立即收到全量 assign（尚无实例）
	select {
	case as := <-assigns:
		if as.Version != 1 || len(as.Instances) != 0 {
			t.Fatalf("上线后的 assign = v%d，%d 个实例", as.Version, len(as.Instances))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上线后应收到 assign")
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
	// 会话已被 Close 断开，拨号方应已结束；全部协程应回落到打开之前的水平。
	sess.Close("")
	waitUntil(t, "协程数回落到基线", func() bool { return runtime.NumGoroutine() <= baseline })
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

// 会话仍在线时 App.Close：agents.Close 必须早于关库，会话断开善后才能写下最后在线时间。
// 这里不经 Serve（Serve 返回时监听已先断开会话），而是挂一个独立监听，让会话在 Close 时仍然存活。
func TestApp关停顺序会话善后先于关库(t *testing.T) {
	logs := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := testConfig(t)
	a, err := Open(context.Background(), cfg, testOpts()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := agentlinktest.StartListener(t, agentlink.ListenerConfig{
		Auth: a.agents, Handler: a.agents.HandlerFor, OnSession: a.agents.Attach, Clock: a.opts.clk,
	})
	code, _, err := a.devices.NewPairingCode(context.Background(), devices.PairingOptions{Name: "nas1", Role: model.DeviceRoleNAS})
	if err != nil {
		t.Fatal(err)
	}
	var deviceID string
	sess, err := agentlink.Dial(context.Background(), agentlink.DialerConfig{
		URL: srv.URL, Pin: srv.Pin, Role: agent.RoleAgent, Log: agentlinktest.QuietLog(), PairCode: code,
		PairInfo:     agent.Pair{Name: "nas1", Hostname: "nas.local", OS: "linux", Arch: "amd64"},
		OnCredential: func(ok agent.PairOK) error { deviceID = ok.DeviceID; return nil },
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

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, l := range logs.lines() {
		if strings.Contains(l, "closed") || strings.Contains(l, "记录设备最后在线时间失败") {
			t.Fatalf("关停期间出现数据库已关闭类错误: %s", l)
		}
	}
	re, err := Open(context.Background(), cfg, testOpts()...)
	if err != nil {
		t.Fatalf("重新打开数据库: %v", err)
	}
	d, err := re.devices.Get(context.Background(), deviceID)
	_ = re.Close()
	if err != nil {
		t.Fatal(err)
	}
	if d.LastSeenAt == nil {
		t.Fatal("关停后设备最后在线时间应已落库（agents.Close 应先于关库）")
	}
}
