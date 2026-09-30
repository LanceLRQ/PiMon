package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

type fakeStreamer struct {
	runs    atomic.Int32
	started chan Input
	run     func(ctx context.Context, n int32, emit func(*report.Report)) error
}

func newFakeStreamer(run func(ctx context.Context, n int32, emit func(*report.Report)) error) *fakeStreamer {
	return &fakeStreamer{started: make(chan Input, 32), run: run}
}

func (f *fakeStreamer) Run(ctx context.Context, in Input, emit func(*report.Report)) error {
	n := f.runs.Add(1)
	f.started <- in
	return f.run(ctx, n, emit)
}

func waitStart(t *testing.T, f *fakeStreamer) Input {
	t.Helper()
	select {
	case in := <-f.started:
		return in
	case <-time.After(5 * time.Second):
		t.Fatal("等待 Streamer 启动超时")
		return Input{}
	}
}

func TestStreamBackoffSequence(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := StreamBackoff(i); got != w*time.Second {
			t.Errorf("第 %d 次重启: 得 %v，要 %v", i, got, w*time.Second)
		}
	}
	if got := StreamBackoff(10000); got != 60*time.Second {
		t.Errorf("大数不应溢出: %v", got)
	}
}

func TestStreamRestartsWithBackoff(t *testing.T) {
	f := newFakeStreamer(func(context.Context, int32, func(*report.Report)) error { return errors.New("断开") })
	clk := newRecClock()
	m := NewStreamManager(context.Background(), StreamOptions{Clock: clk}, func(string, *report.Report) {})
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	waitStart(t, f)
	for i, w := range []time.Duration{1, 2, 4, 8} {
		durs := clk.waitDurs(t, i+1)
		if durs[i] != w*time.Second {
			t.Fatalf("第 %d 次重启等待: 得 %v，要 %v", i, durs[i], w*time.Second)
		}
		clk.Advance(durs[i] - time.Millisecond)
		if int(f.runs.Load()) != i+1 {
			t.Fatalf("未到期不应重启")
		}
		clk.Advance(time.Millisecond)
		waitStart(t, f)
	}
}

func TestStreamCleanExitAlsoRestarts(t *testing.T) {
	f := newFakeStreamer(func(context.Context, int32, func(*report.Report)) error { return nil })
	clk := newRecClock()
	m := NewStreamManager(context.Background(), StreamOptions{Clock: clk}, func(string, *report.Report) {})
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	waitStart(t, f)
	durs := clk.waitDurs(t, 1)
	clk.Advance(durs[0])
	waitStart(t, f)
}

func TestStreamLongRunResetsBackoff(t *testing.T) {
	clk := newRecClock()
	f := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		if n >= 2 {
			clk.Advance(2 * time.Minute) // 模拟这次运行持续了很久
		}
		return errors.New("断开")
	})
	m := NewStreamManager(context.Background(), StreamOptions{Clock: clk}, func(string, *report.Report) {})
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	waitStart(t, f)
	durs := clk.waitDurs(t, 1)
	clk.Advance(durs[0])
	waitStart(t, f)
	durs = clk.waitDurs(t, 2)
	if durs[1] != time.Second {
		t.Fatalf("稳定运行超过 60 秒后应恢复 1s 退避: %v", durs[1])
	}
}

func TestStreamEmitReachesCallback(t *testing.T) {
	got := make(chan string, 4)
	f := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		emit(&report.Report{Status: report.StatusOK, Summary: "one"})
		<-ctx.Done()
		return ctx.Err()
	})
	m := NewStreamManager(context.Background(), StreamOptions{Clock: newRecClock()}, func(id string, r *report.Report) { got <- id + ":" + r.Summary })
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	select {
	case v := <-got:
		if v != "s:one" {
			t.Fatalf("回调内容: %s", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待 emit 回调超时")
	}
}

func TestStreamUpsertSameHashKeepsRunning(t *testing.T) {
	f := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	m := NewStreamManager(context.Background(), StreamOptions{Clock: newRecClock()}, func(string, *report.Report) {})
	defer m.Stop()
	if !m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f}) {
		t.Fatal("新任务应返回 true")
	}
	waitStart(t, f)
	if m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f}) {
		t.Fatal("hash 不变应返回 false")
	}
	m.Stop()
	if f.runs.Load() != 1 {
		t.Fatalf("hash 不变不得重启: %d", f.runs.Load())
	}
}

func TestStreamUpsertChangedHashRestartsWithNewInput(t *testing.T) {
	stopped := make(chan struct{})
	old := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	fresh := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	m := NewStreamManager(context.Background(), StreamOptions{Clock: newRecClock()}, func(string, *report.Report) {})
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h1", Streamer: old, Input: Input{State: "a"}})
	waitStart(t, old)
	if !m.Upsert(StreamTask{ID: "s", ConfigHash: "h2", Streamer: fresh, Input: Input{State: "b"}}) {
		t.Fatal("hash 变化应返回 true")
	}
	<-stopped
	in := waitStart(t, fresh)
	if in.State != "b" {
		t.Fatalf("应使用新输入: %+v", in)
	}
	if old.runs.Load() != 1 {
		t.Fatalf("旧的不应再被启动: %d", old.runs.Load())
	}
}

func TestStreamRemoveStopsAndStopIsClean(t *testing.T) {
	stopped := make(chan struct{})
	f := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	clk := newRecClock()
	m := NewStreamManager(context.Background(), StreamOptions{Clock: clk}, func(string, *report.Report) {})
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	waitStart(t, f)
	if !m.Remove("s") || m.Remove("s") {
		t.Fatal("Remove 返回值不对")
	}
	<-stopped
	clk.Advance(time.Hour)
	m.Stop()
	m.Stop()
	if f.runs.Load() != 1 {
		t.Fatalf("Remove 后不应重启: %d", f.runs.Load())
	}
}

func TestStreamEmitAfterCancelIsDropped(t *testing.T) {
	var got atomic.Int32
	release := make(chan struct{})
	f := newFakeStreamer(func(ctx context.Context, n int32, emit func(*report.Report)) error {
		<-ctx.Done()
		<-release
		emit(&report.Report{Status: report.StatusOK})
		return ctx.Err()
	})
	m := NewStreamManager(context.Background(), StreamOptions{Clock: newRecClock()}, func(string, *report.Report) { got.Add(1) })
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	waitStart(t, f)
	m.Remove("s")
	close(release)
	m.Stop()
	if got.Load() != 0 {
		t.Fatal("取消后的 emit 不应回调")
	}
}
