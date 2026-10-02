package screenstate

import (
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

type vpRecorder struct {
	mu  sync.Mutex
	got []model.Viewport
}

func (r *vpRecorder) add(v model.Viewport) {
	r.mu.Lock()
	r.got = append(r.got, v)
	r.mu.Unlock()
}

func (r *vpRecorder) list() []model.Viewport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.Viewport(nil), r.got...)
}

// waitSettled 等待 tracker 已处理（无论采信与否）n 次到期的待采信检查。
func waitSettled(t *testing.T, tr *ViewportTracker, n int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for tr.settled.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("已处理 %d 次, 期望 %d", tr.settled.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func newTracker(clk clock.Clock) (*ViewportTracker, *vpRecorder) {
	rec := &vpRecorder{}
	return NewViewportTracker(clk, rec.add), rec
}

func TestViewport_稳定2秒才采信(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	tr.Report(model.Viewport{W: 1024, H: 600, DPR: 1})
	if clk.Waiters() != 1 {
		t.Fatalf("等待者 = %d", clk.Waiters())
	}
	clk.Advance(1900 * time.Millisecond)
	if len(rec.list()) != 0 || tr.Current() != nil || tr.settled.Load() != 0 {
		t.Fatal("不足 2 秒不应采信")
	}
	clk.Advance(100 * time.Millisecond)
	waitSettled(t, tr, 1)
	if got := rec.list(); len(got) != 1 || got[0].W != 1024 {
		t.Fatalf("回调 = %+v", got)
	}
	if c := tr.Current(); c == nil || c.H != 600 {
		t.Fatalf("Current = %+v", c)
	}
}

func TestViewport_尺寸变化重新计时(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	tr.Report(model.Viewport{W: 800, H: 480, DPR: 1})
	clk.Advance(1500 * time.Millisecond)
	tr.Report(model.Viewport{W: 1024, H: 600, DPR: 1})
	clk.Advance(1500 * time.Millisecond) // 旧尺寸的计时到期，但已被新上报取代
	waitSettled(t, tr, 1)
	if len(rec.list()) != 0 {
		t.Fatalf("旧尺寸的计时不应采信: %+v", rec.list())
	}
	clk.Advance(600 * time.Millisecond)
	waitSettled(t, tr, 2)
	if got := rec.list(); len(got) != 1 || got[0].W != 1024 {
		t.Fatalf("回调 = %+v", got)
	}
}

func TestViewport_相同尺寸重复上报不重复回调(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	v := model.Viewport{W: 1024, H: 600, DPR: 1}
	tr.Report(v)
	clk.Advance(2 * time.Second)
	waitSettled(t, tr, 1)
	tr.Report(v)
	if clk.Waiters() != 0 {
		t.Fatal("与已采信尺寸相同的上报不应重新计时")
	}
	clk.Advance(3 * time.Second)
	if n := len(rec.list()); n != 1 {
		t.Fatalf("回调次数 = %d", n)
	}
}

func TestViewport_关屏期间忽略(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	tr.SetScreenOn(false)
	tr.Report(model.Viewport{W: 1920, H: 1080, DPR: 1}) // 关屏时的兜底尺寸
	clk.Advance(10 * time.Second)
	if clk.Waiters() != 0 || len(rec.list()) != 0 || tr.Current() != nil {
		t.Fatal("关屏期间的上报应被忽略")
	}
}

func TestViewport_待采信期间关屏则放弃(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	tr.Report(model.Viewport{W: 1024, H: 600, DPR: 1})
	tr.SetScreenOn(false)
	clk.Advance(2 * time.Second)
	waitSettled(t, tr, 1)
	if len(rec.list()) != 0 || tr.Current() != nil {
		t.Fatal("等待期间关屏，不应采信")
	}
}

func TestViewport_唤醒后5秒内忽略(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	tr.SetScreenOn(false)
	clk.Advance(time.Minute)
	tr.SetScreenOn(true) // 唤醒
	clk.Advance(2 * time.Second)
	tr.Report(model.Viewport{W: 1920, H: 1080, DPR: 1}) // 唤醒后 2 秒：忽略
	clk.Advance(2 * time.Second)
	if clk.Waiters() != 0 || len(rec.list()) != 0 {
		t.Fatal("唤醒后 5 秒内的上报应被忽略")
	}
	clk.Advance(time.Second) // 距唤醒 5 秒整
	tr.Report(model.Viewport{W: 1024, H: 600, DPR: 1})
	clk.Advance(2 * time.Second)
	waitSettled(t, tr, 1)
	if got := rec.list(); len(got) != 1 || got[0].W != 1024 {
		t.Fatalf("回调 = %+v", got)
	}
}

func TestViewport_非法尺寸忽略(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	tr, rec := newTracker(clk)
	tr.Report(model.Viewport{W: 0, H: 600, DPR: 1})
	tr.Report(model.Viewport{W: 1024, H: -1, DPR: 1})
	clk.Advance(5 * time.Second)
	if len(rec.list()) != 0 || clk.Waiters() != 0 {
		t.Fatal("非法尺寸不应进入待采信")
	}
}
