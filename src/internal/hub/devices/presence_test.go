package devices

import (
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

var presenceStart = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// eventually 轮询等待条件成立，最多 2 秒；1ms 轮询沿用仓库惯例（screenstate）。
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitWaiters 等待假时钟上至少挂了 n 个等待者。
func waitWaiters(t *testing.T, clk *clock.Fake, n int) {
	t.Helper()
	eventually(t, "假时钟等待者", func() bool { return clk.Waiters() >= n })
}

// waitState 等待设备进入期望状态。
func waitState(t *testing.T, p *Presence, id, want string) {
	t.Helper()
	eventually(t, id+" 进入 "+want, func() bool {
		got, _ := p.State(id)
		return got == want
	})
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

func TestPresence断开29秒仍为disconnected(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	assertState(t, p, "a", model.PresenceDisconnected)
	clk.Advance(29 * time.Second)
	// Close 会等计时协程退出，之后断言状态才是确定的。
	p.Close()
	assertState(t, p, "a", model.PresenceDisconnected)
}

func TestPresence断开满30秒变offline(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	clk.Advance(30 * time.Second)
	waitState(t, p, "a", model.PresenceOffline)
	if _, since := p.State("a"); !since.Equal(presenceStart.Add(30 * time.Second)) {
		t.Fatalf("offline 起点 = %v", since)
	}
}

func TestPresence二次断开旧计时到期不生效(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	clk.Advance(20 * time.Second)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	// 到 30 秒时第一次断开的旧计时到期，但已作废。
	clk.Advance(10 * time.Second)
	p.Close()
	assertState(t, p, "a", model.PresenceDisconnected)
}

func TestPresence二次断开按新计时转offline(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	clk.Advance(20 * time.Second)
	p.Connected("a")
	assertState(t, p, "a", model.PresenceOnline)
	p.Disconnected("a", clk.Now())
	clk.Advance(30 * time.Second)
	waitState(t, p, "a", model.PresenceOffline)
	if _, since := p.State("a"); !since.Equal(presenceStart.Add(50 * time.Second)) {
		t.Fatalf("offline 起点 = %v, 期望按第二次断开算", since)
	}
}

func TestPresence重连后旧计时到期仍在线(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	clk.Advance(10 * time.Second)
	p.Connected("a")
	clk.Advance(time.Hour)
	p.Close()
	assertState(t, p, "a", model.PresenceOnline)
}

func TestPresence宽限期89秒仍为pending(t *testing.T) {
	p, clk := newPresence(t, "a")
	assertState(t, p, "a", model.PresencePending)
	clk.Advance(89 * time.Second)
	p.Close()
	assertState(t, p, "a", model.PresencePending)
}

func TestPresence宽限期满未连上变offline(t *testing.T) {
	p, clk := newPresence(t, "a")
	clk.Advance(90 * time.Second)
	waitState(t, p, "a", model.PresenceOffline)
}

func TestPresence宽限期内连上直接online(t *testing.T) {
	p, clk := newPresence(t, "a")
	clk.Advance(30 * time.Second)
	p.Connected("a")
	assertState(t, p, "a", model.PresenceOnline)
	clk.Advance(time.Hour)
	p.Close()
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

func TestPresenceForget后Disconnected不复活(t *testing.T) {
	p, clk := newPresence(t)
	var c changes
	p.Connected("a")
	p.OnChange(c.record)
	p.Forget("a")
	p.Disconnected("a", clk.Now())
	if s, _ := p.State("a"); s != "" {
		t.Fatalf("Forget 后 Disconnected 使状态复活为 %q", s)
	}
	p.Close()
	if c.count() != 1 {
		t.Fatalf("回调 = %d, 期望仅 Forget 的 1 次", c.count())
	}
}

func TestPresencePending收到Disconnected被忽略(t *testing.T) {
	p, clk := newPresence(t, "a")
	var c changes
	p.OnChange(c.record)
	p.Disconnected("a", clk.Now().Add(-time.Minute))
	assertState(t, p, "a", model.PresencePending)
	clk.Advance(60 * time.Second)
	p.Close()
	assertState(t, p, "a", model.PresencePending)
	if c.count() != 0 {
		t.Fatalf("回调 = %d, 期望 0", c.count())
	}
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
	p.Close()
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

func TestPresenceForget后旧计时不复活(t *testing.T) {
	p, clk := newPresence(t, "p")
	p.Connected("a")
	p.Disconnected("a", clk.Now())
	p.Forget("a")
	p.Forget("p")
	clk.Advance(time.Hour)
	p.Close()
	for _, id := range []string{"a", "p"} {
		if s, _ := p.State(id); s != "" {
			t.Fatalf("到期后 %s 状态复活为 %q", id, s)
		}
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
	assertState(t, p, "a", model.PresencePending)
	assertState(t, p, "b", model.PresenceDisconnected)
	// Close 后的断开只更新状态，不再起计时协程。
	p.Connected("c")
	p.Disconnected("c", clk.Now())
	assertState(t, p, "c", model.PresenceDisconnected)
}

func TestPresence断开时刻早于当前时间按剩余时长计时(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	clk.Advance(25 * time.Second)
	p.Disconnected("a", clk.Now().Add(-20*time.Second))
	// 断开发生在 5 秒，到期点为 35 秒，当前 25 秒，还需 10 秒。
	clk.Advance(9 * time.Second)
	p.Close()
	assertState(t, p, "a", model.PresenceDisconnected)
}

func TestPresence断开时刻早于30秒前立刻offline(t *testing.T) {
	p, clk := newPresence(t)
	p.Connected("a")
	clk.Advance(time.Minute)
	p.Disconnected("a", clk.Now().Add(-40*time.Second))
	waitState(t, p, "a", model.PresenceOffline)
}

func TestPresenceClose与Disconnected并发(t *testing.T) {
	clk := clock.NewFake(presenceStart)
	p := NewPresence(clk, presenceStart, nil)
	const n = 64
	ids := make([]string, n)
	for i := range ids {
		ids[i] = string(rune('A'+i/26)) + string(rune('a'+i%26))
		p.Connected(ids[i])
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Disconnected(id, clk.Now())
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.Close()
	}()
	wg.Wait()
	p.Close()
	for _, id := range ids {
		if s, _ := p.State(id); s != model.PresenceDisconnected {
			t.Fatalf("%s 状态 = %q", id, s)
		}
	}
}

func TestClose之后Connected与Disconnected安全(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := NewPresence(clk, clk.Now(), nil)
	p.Close()
	p.Connected("a")
	assertState(t, p, "a", model.PresenceOnline)
	p.Disconnected("a", clk.Now())
	assertState(t, p, "a", model.PresenceDisconnected)
	p.Close()
}
