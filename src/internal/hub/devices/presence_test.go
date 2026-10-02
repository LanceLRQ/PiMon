package devices

import (
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

var presenceStart = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// waitWaiters 等待假时钟上至少挂了 n 个等待者。
func waitWaiters(t *testing.T, clk *clock.Fake, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for clk.Waiters() < n {
		if time.Now().After(deadline) {
			t.Fatalf("等待者 = %d, 期望至少 %d", clk.Waiters(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitState 等待设备进入期望状态。
func waitState(t *testing.T, p *Presence, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := p.State(id)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("设备 %s 状态 = %q, 期望 %q", id, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func assertState(t *testing.T, p *Presence, id, want string) {
	t.Helper()
	if got, _ := p.State(id); got != want {
		t.Fatalf("设备 %s 状态 = %q, 期望 %q", id, got, want)
	}
}

type changes struct {
	mu  sync.Mutex
	ids []string
}

func (c *changes) record(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
}

func (c *changes) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ids)
}

func newPresence(t *testing.T, known ...string) (*Presence, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(presenceStart)
	p := NewPresence(clk, presenceStart, known)
	t.Cleanup(p.Close)
	return p, clk
}

func TestPresence断开30秒前后状态(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	assertState(t, p, "a", model.PresenceOnline)
	p.Disconnected("a", clk.Now())
	assertState(t, p, "a", model.PresenceDisconnected)
	clk.Advance(29 * time.Second)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "a", model.PresenceDisconnected)
	clk.Advance(time.Second)
	waitState(t, p, "a", model.PresenceOffline)
	if _, since := p.State("a"); !since.Equal(presenceStart.Add(30 * time.Second)) {
		t.Fatalf("offline 起点 = %v", since)
	}
}

func TestPresence30秒内重连旧计时作废(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	clk.Advance(20 * time.Second)
	p.Connected("a")
	assertState(t, p, "a", model.PresenceOnline)
	// 再次断开：新计时从第二次断开算起，旧计时在原到期点不能把它改成 offline。
	p.Disconnected("a", clk.Now())
	clk.Advance(10 * time.Second)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "a", model.PresenceDisconnected)
	clk.Advance(20 * time.Second)
	waitState(t, p, "a", model.PresenceOffline)
}

func TestPresence重连后旧计时到期仍在线(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	clk.Advance(10 * time.Second)
	p.Connected("a")
	clk.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "a", model.PresenceOnline)
}

func TestPresence宽限期内未连上为pending且90秒后offline(t *testing.T) {
	p, clk := newPresence(t, "a")
	assertState(t, p, "a", model.PresencePending)
	clk.Advance(89 * time.Second)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "a", model.PresencePending)
	clk.Advance(time.Second)
	waitState(t, p, "a", model.PresenceOffline)
}

func TestPresence宽限期内连上直接online(t *testing.T) {
	p, clk := newPresence(t, "a")
	clk.Advance(30 * time.Second)
	p.Connected("a")
	assertState(t, p, "a", model.PresenceOnline)
	clk.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "a", model.PresenceOnline)
}

func TestPresence未知设备与新配对设备(t *testing.T) {
	p, _ := newPresence(t)
	if s, _ := p.State("x"); s != "" {
		t.Fatalf("未知设备状态 = %q", s)
	}
	p.Connected("x")
	assertState(t, p, "x", model.PresenceOnline)
}

func TestPresenceOnChange次数(t *testing.T) {
	p, clk := newPresence(t, "b")
	var c changes
	p.OnChange(c.record)
	p.Connected("a") // 新增 online：1
	p.Connected("a") // 无变化
	if c.count() != 1 {
		t.Fatalf("回调 = %d, 期望 1", c.count())
	}
	p.Disconnected("a", clk.Now()) // 2
	p.Disconnected("a", clk.Now()) // 无变化
	clk.Advance(30 * time.Second)
	waitState(t, p, "a", model.PresenceOffline) // 3
	clk.Advance(60 * time.Second)
	waitState(t, p, "b", model.PresenceOffline) // 4
	p.Forget("a")                               // 5
	if c.count() != 5 {
		t.Fatalf("回调 = %d, 期望 5", c.count())
	}
}

func TestPresence回调在锁外调用(t *testing.T) {
	p, _ := newPresence(t)
	p.OnChange(func(id string) {
		// 回调里再读状态，持锁时会死锁。
		p.State(id)
		p.Online()
	})
	done := make(chan struct{})
	go func() { p.Connected("a"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("回调内读取状态死锁")
	}
}

func TestPresenceOnline计数(t *testing.T) {
	p, clk := newPresence(t, "c")
	if p.Online() != 0 {
		t.Fatalf("Online = %d", p.Online())
	}
	p.Connected("a")
	p.Connected("b")
	p.Connected("c")
	if p.Online() != 3 {
		t.Fatalf("Online = %d", p.Online())
	}
	p.Disconnected("b", clk.Now())
	if p.Online() != 2 {
		t.Fatalf("Online = %d", p.Online())
	}
	p.Forget("a")
	if p.Online() != 1 {
		t.Fatalf("Online = %d", p.Online())
	}
}

func TestPresenceForget后状态消失且计时无效(t *testing.T) {
	p, clk := newPresence(t, "p")
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	p.Forget("a")
	p.Forget("p")
	if s, _ := p.State("a"); s != "" {
		t.Fatalf("Forget 后状态 = %q", s)
	}
	clk.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	if s, _ := p.State("a"); s != "" {
		t.Fatalf("到期后状态复活为 %q", s)
	}
	if s, _ := p.State("p"); s != "" {
		t.Fatalf("pending 设备被 Forget 后状态复活为 %q", s)
	}
}

func TestPresenceClose停止计时协程(t *testing.T) {
	clk := clock.NewFake(presenceStart)
	p := NewPresence(clk, presenceStart, []string{"a"})
	p.Connected("b")
	p.Disconnected("b", clk.Now())
	waitWaiters(t, clk, 2)
	p.Close()
	p.Close() // 重复关闭安全
	clk.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "b", model.PresenceDisconnected)
}

func TestPresence断开时刻早于当前时间按剩余时长计时(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	clk.Advance(25 * time.Second)
	p.Disconnected("a", clk.Now().Add(-20*time.Second))
	// 断开发生在 5 秒，到期点为 35 秒，当前 25 秒，还需 10 秒。
	clk.Advance(9 * time.Second)
	time.Sleep(10 * time.Millisecond)
	assertState(t, p, "a", model.PresenceDisconnected)
	clk.Advance(time.Second)
	waitState(t, p, "a", model.PresenceOffline)
}
