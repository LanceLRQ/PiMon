package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/events"
	"github.com/LanceLRQ/PiMon/src/internal/agent/hubtest"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const taskData = `{"tool":"codex","session":"s1","turn":"t1","state":"running","project":"p","source":"hook"}`

func taskEv(id string) events.Event {
	return events.Event{ID: id, Kind: model.EventKindTask, Data: json.RawMessage(taskData), At: t0.Add(time.Second)}
}

func enqueue(t *testing.T, h *harness, ids ...string) {
	t.Helper()
	for _, id := range ids {
		must(t, h.d.EnqueueEvent(context.Background(), taskEv(id)))
	}
}

func seqIDs(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%03d", prefix, i+1)
	}
	return out
}

// queued 返回队列里尚未确认的条数。
func (h *harness) queued() int64 {
	h.t.Helper()
	h.d.evMu.RLock()
	defer h.d.evMu.RUnlock()
	if h.d.events == nil {
		h.t.Fatal("队列未创建")
	}
	st, err := h.d.events.Stats(context.Background())
	must(h.t, err)
	return st.Pending
}

func hubEventIDs(hub *hubtest.Hub) []string {
	var out []string
	for _, e := range hub.Router.Events() {
		out = append(out, e.ID)
	}
	return out
}

// waitRecorder 记录发送协程每次进入等待时登记的重发计时（0 表示没有在途条目、不登记计时）。
type waitRecorder struct {
	mu sync.Mutex
	n  int
}

func (w *waitRecorder) hook(time.Duration) { w.mu.Lock(); w.n++; w.mu.Unlock() }

func (w *waitRecorder) count() int { w.mu.Lock(); defer w.mu.Unlock(); return w.n }

// connectedHubHooked 与 connectedHub 相同，但在启动前装上发送协程的等待钩子。
func connectedHubHooked(t *testing.T) (*hubtest.Hub, *harness, string, *waitRecorder) {
	t.Helper()
	hub := hubtest.Start(t)
	h := newHarness(t, nil)
	_, id := pairedHub(t, hub, h)
	w := &waitRecorder{}
	h.d.testHookEventWait = w.hook
	h.start()
	waitUntil(t, "守护进程连上 hub", func() bool { return h.status().Connected && hub.Online(id) })
	return hub, h, id, w
}

// stepClock 以 1 秒步长推进 agent 时钟，每步留出真实时间让心跳应答往返，避免一次大跨度推进触发心跳超时。
func stepClock(h *harness, d time.Duration) {
	for i := time.Duration(0); i < d; i += time.Second {
		h.clk.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
}

func TestEvents_EnqueueEvent在Run之前与之后返回错误(t *testing.T) {
	hub := hubtest.Start(t)
	h := newHarness(t, nil)
	pairedHub(t, hub, h)
	if err := h.d.EnqueueEvent(context.Background(), taskEv("a")); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Run 之前应返回 ErrNotRunning，得到 %v", err)
	}
	h.start()
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	enqueue(t, h, "a")
	if err := h.d.EnqueueEvent(context.Background(), events.Event{ID: "bad", Kind: model.EventKindTask, Data: json.RawMessage(`{}`), At: t0}); err == nil {
		t.Fatal("不合法的任务事件应被拒绝")
	}
	h.stop()
	if err := h.d.EnqueueEvent(context.Background(), taskEv("b")); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Run 退出后应返回 ErrNotRunning，得到 %v", err)
	}
}

func TestEvents_端到端event与ack(t *testing.T) {
	hub, h, _, _ := connectedHub(t)
	enqueue(t, h, "a", "b", "c")
	waitUntil(t, "hub 收到并确认", func() bool { return len(hub.Router.Events()) == 3 && h.queued() == 0 })
	got := hub.Router.Events()
	for i, e := range got {
		if e.ID != string(rune('a'+i)) || e.Seq != int64(i+1) || e.Kind != model.EventKindTask || !e.At.Equal(t0.Add(time.Second)) {
			t.Fatalf("第 %d 条事件 = %+v", i, e)
		}
		var te model.TaskEvent
		if err := json.Unmarshal(e.Data, &te); err != nil || te.Session != "s1" || te.State != "running" {
			t.Fatalf("事件数据 = %s (%v)", e.Data, err)
		}
	}
	// 仍在队列里（未确认）的 id 重复入队：静默忽略，不重复发送。
	hub.Router.HoldAcks(true)
	enqueue(t, h, "d", "d")
	waitUntil(t, "hub 收到 d", func() bool { return len(hub.Router.Events()) == 4 })
	never(t, "重复 id 再次发送", func() bool { return len(hub.Router.Events()) != 4 })
	if n := h.queued(); n != 1 {
		t.Fatalf("队列条数 = %d", n)
	}
}

func TestEvents_断线期间只入队重连后按seq补发(t *testing.T) {
	hub, h, _, id := connectedHub(t)
	s, ok := hub.Server.Session(id)
	if !ok {
		t.Fatal("hub 侧没有会话")
	}
	s.Close("")
	waitUntil(t, "断开", func() bool { return !h.status().Connected })
	ids := seqIDs(5, "e")
	enqueue(t, h, ids...)
	never(t, "断线期间发送", func() bool { return len(hub.Router.Events()) != 0 })
	if n := h.queued(); n != 5 {
		t.Fatalf("断线期间应全部留在队列: %d", n)
	}
	waitUntil(t, "重连并补发", func() bool {
		h.clk.Advance(500 * time.Millisecond)
		return len(hub.Router.Events()) == 5 && h.queued() == 0
	})
	if got := hubEventIDs(hub); !slices.Equal(got, ids) {
		t.Fatalf("补发应按 seq 升序: %v", got)
	}
}

func TestEvents_未确认的在途条目断线后由新会话重发(t *testing.T) {
	hub, h, _, id := connectedHub(t)
	hub.Router.HoldAcks(true)
	enqueue(t, h, "a", "b")
	waitUntil(t, "hub 收到", func() bool { return len(hub.Router.Events()) == 2 })
	if n := h.queued(); n != 2 {
		t.Fatalf("未确认前应留在队列: %d", n)
	}
	s, _ := hub.Server.Session(id)
	s.Close("")
	waitUntil(t, "断开", func() bool { return !h.status().Connected })
	hub.Router.HoldAcks(false)
	waitUntil(t, "重连后重发并确认", func() bool {
		h.clk.Advance(500 * time.Millisecond)
		return h.queued() == 0
	})
	if got := hubEventIDs(hub); !slices.Equal(got, []string{"a", "b", "a", "b"}) {
		t.Fatalf("重连后应按 seq 重发未确认条目: %v", got)
	}
}

func TestEvents_在途60秒未确认重发(t *testing.T) {
	hub, h, _, w := connectedHubHooked(t)
	hub.Router.HoldAcks(true)
	before := w.count()
	enqueue(t, h, "a")
	waitUntil(t, "hub 收到", func() bool { return len(hub.Router.Events()) == 1 })
	waitUntil(t, "发送协程登记重发计时", func() bool { return w.count() > before })
	stepClock(h, 59*time.Second)
	never(t, "不足 60 秒就重发", func() bool { return len(hub.Router.Events()) != 1 })
	hub.Router.HoldAcks(false)
	stepClock(h, time.Second)
	waitUntil(t, "60 秒后重发并被确认", func() bool { return len(hub.Router.Events()) == 2 && h.queued() == 0 })
	if !h.status().Connected {
		t.Fatal("推进时钟期间会话不应断开")
	}
}

func TestEvents_发送窗口128条(t *testing.T) {
	hub, h, _, w := connectedHubHooked(t)
	hub.Router.HoldAcks(true)
	ids := seqIDs(130, "w")
	enqueue(t, h, ids...)
	waitUntil(t, "发满窗口", func() bool { return len(hub.Router.Events()) == events.DefaultWindow })
	never(t, "超出窗口", func() bool { return len(hub.Router.Events()) != events.DefaultWindow })
	if got := hubEventIDs(hub); !slices.Equal(got, ids[:events.DefaultWindow]) {
		t.Fatalf("窗口内应是 seq 最小的 128 条: %v", got[:3])
	}
	hub.Router.HoldAcks(false)
	before := w.count()
	enqueue(t, h, "late")
	waitUntil(t, "新入队唤醒发送协程", func() bool { return w.count() > before })
	never(t, "窗口满时新事件不应发送", func() bool { return len(hub.Router.Events()) != events.DefaultWindow })
	stepClock(h, 60*time.Second)
	waitUntil(t, "超时重发后补完剩余", func() bool { return h.queued() == 0 })
	got := hubEventIDs(hub)
	if len(got) != 2*events.DefaultWindow+3 {
		t.Fatalf("应收到 128 + 128 次重发 + 3 条剩余，实际 %d", len(got))
	}
	if tail := got[2*events.DefaultWindow:]; !slices.Equal(tail, []string{"w129", "w130", "late"}) {
		t.Fatalf("剩余条目顺序 = %v", tail)
	}
}

func TestEvents_会话就绪时按7天淘汰(t *testing.T) {
	hub, h, _, id := connectedHub(t)
	s, _ := hub.Server.Session(id)
	s.Close("")
	waitUntil(t, "断开", func() bool { return !h.status().Connected })
	enqueue(t, h, "old")
	h.clk.Advance(7*24*time.Hour + time.Minute)
	waitUntil(t, "重连", func() bool {
		h.clk.Advance(500 * time.Millisecond)
		return h.status().Connected && hub.Online(id)
	})
	waitUntil(t, "就绪时淘汰过期条目", func() bool { return h.queued() == 0 })
	h.d.evMu.RLock()
	dropped, err := h.d.events.Dropped(context.Background())
	h.d.evMu.RUnlock()
	must(t, err)
	if dropped != 1 {
		t.Fatalf("dropped = %d", dropped)
	}
	never(t, "过期条目被发送", func() bool { return len(hub.Router.Events()) != 0 })
}

func TestEvents_被顶替的旧会话停止发送且新会话补发在途(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })
	enqueue(t, h, "e1", "e2")
	gotA := nextEvents(t, a, 2)
	if !slices.Equal(gotA, []string{"e1", "e2"}) {
		t.Fatalf("A 收到 %v", gotA)
	}
	b, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred})
	must(t, err)
	waitUntil(t, "A 被顶替", func() bool { return isClosed(a.sess) })
	// 新会话接管旧会话未确认的在途条目，按 seq 补发。
	if got := nextEvents(t, b, 2); !slices.Equal(got, []string{"e1", "e2"}) {
		t.Fatalf("B 应补发旧会话的在途条目: %v", got)
	}
	enqueue(t, h, "e3")
	if got := nextEvents(t, b, 1); !slices.Equal(got, []string{"e3"}) {
		t.Fatalf("B 收到 %v", got)
	}
	must(t, b.sess.Send(&agent.Ack{IDs: []string{"e1", "e2", "e3"}}))
	waitUntil(t, "确认后清空", func() bool { return h.queued() == 0 })
}

func TestEvents_旧会话上的ack同样删除条目(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })
	enqueue(t, h, "e1")
	nextEvents(t, a, 1)
	must(t, a.sess.Send(&agent.Ack{IDs: []string{"e1", "e1", "不存在"}}))
	waitUntil(t, "确认后删除", func() bool { return h.queued() == 0 })
}

func TestEvents_入队与退出并发(t *testing.T) {
	_, h, _, _ := connectedHub(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				err := h.d.EnqueueEvent(context.Background(), taskEv(fmt.Sprintf("g%d-%d", g, i)))
				if err != nil && !errors.Is(err, ErrNotRunning) {
					t.Errorf("入队错误: %v", err)
					return
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	h.stop()
	close(stop)
	wg.Wait()
}

// nextEvents 从替身 hub 收取 n 条 event 的 id（跳过其他消息）。
func nextEvents(t *testing.T, f *fakeHub, n int) []string {
	t.Helper()
	var out []string
	for len(out) < n {
		if e, ok := f.next().(*agent.Event); ok {
			out = append(out, e.ID)
		}
	}
	return out
}
