package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// recClock 包装假时钟，记录每次 After 的等待时长。
type recClock struct {
	*clock.Fake
	mu   sync.Mutex
	durs []time.Duration
}

func newRecClock() *recClock { return &recClock{Fake: clock.NewFake(t0)} }

func (c *recClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.durs = append(c.durs, d)
	c.mu.Unlock()
	return c.Fake.After(d)
}

// waitDurs 等到记录满 n 条并返回副本。
func (c *recClock) waitDurs(t *testing.T, n int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		if len(c.durs) >= n {
			out := append([]time.Duration(nil), c.durs...)
			c.mu.Unlock()
			return out
		}
		c.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("等待 %d 次 After 超时", n)
		}
		time.Sleep(time.Millisecond)
	}
}

func zeroJitter(time.Duration) time.Duration { return 0 }

type schedHarness struct {
	clk     *recClock
	s       *Scheduler
	results chan TaskResult
}

func newSchedHarness(t *testing.T, opts SchedulerOptions) *schedHarness {
	t.Helper()
	h := &schedHarness{clk: newRecClock(), results: make(chan TaskResult, 64)}
	opts.Clock = h.clk
	if opts.Jitter == nil {
		opts.Jitter = zeroJitter
	}
	h.s = NewScheduler(context.Background(), opts, func(r TaskResult) { h.results <- r })
	t.Cleanup(h.s.Stop)
	return h
}

func (h *schedHarness) next(t *testing.T) TaskResult {
	t.Helper()
	select {
	case r := <-h.results:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("等待结果回调超时")
		return TaskResult{}
	}
}

func okRun(state string) RunFunc {
	return func(context.Context) (*report.Report, error) {
		return &report.Report{Status: report.StatusOK, State: state}, nil
	}
}

func TestBackoffInterval(t *testing.T) {
	base := 10 * time.Second
	want := []time.Duration{10, 20, 40, 80, 100, 100, 100}
	for i, w := range want {
		if got := BackoffInterval(base, i); got != w*time.Second {
			t.Errorf("连续失败 %d 次: 得 %v，要 %v", i, got, w*time.Second)
		}
	}
	if got := BackoffInterval(base, 1000); got != 100*time.Second {
		t.Errorf("大失败数不应溢出: %v", got)
	}
}

func TestSchedulerFirstRunUsesJitter(t *testing.T) {
	h := newSchedHarness(t, SchedulerOptions{Jitter: func(iv time.Duration) time.Duration {
		if iv != time.Minute {
			t.Errorf("抖动应拿到任务间隔: %v", iv)
		}
		return 7 * time.Second
	}})
	h.s.Upsert(Task{ID: "a", Interval: time.Minute, ConfigHash: "h", Run: okRun("st1")})
	durs := h.clk.waitDurs(t, 1)
	if durs[0] != 7*time.Second {
		t.Fatalf("首次等待应为抖动值: %v", durs[0])
	}
	h.clk.Advance(6900 * time.Millisecond)
	select {
	case r := <-h.results:
		t.Fatalf("抖动未到不应运行: %+v", r)
	default:
	}
	h.clk.Advance(100 * time.Millisecond)
	r := h.next(t)
	if r.ID != "a" || r.Err != nil || r.Report.State != "st1" || r.Manual {
		t.Fatalf("结果不对: %+v", r)
	}
	if d := h.clk.waitDurs(t, 2)[1]; d != time.Minute {
		t.Fatalf("成功后应按原间隔: %v", d)
	}
}

func TestSchedulerBackoffAndRecovery(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	run := func(context.Context) (*report.Report, error) {
		if fail.Load() {
			return nil, ErrFailed
		}
		return &report.Report{Status: report.StatusOK}, nil
	}
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: 10 * time.Second, ConfigHash: "h", Run: run})
	wantWaits := []time.Duration{0, 20, 40, 80, 100, 100}
	for i := 0; i < len(wantWaits); i++ {
		durs := h.clk.waitDurs(t, i+1)
		if want := wantWaits[i] * time.Second; durs[i] != want {
			t.Fatalf("第 %d 次等待: 得 %v，要 %v", i, durs[i], want)
		}
		if i > 0 {
			h.clk.Advance(durs[i])
		}
		r := h.next(t)
		if r.Err == nil || r.Failures != i+1 {
			t.Fatalf("第 %d 次应失败且 Failures=%d: %+v", i, i+1, r)
		}
	}
	fail.Store(false)
	durs := h.clk.waitDurs(t, len(wantWaits)+1)
	h.clk.Advance(durs[len(wantWaits)])
	if r := h.next(t); r.Err != nil || r.Failures != 0 {
		t.Fatalf("应恢复成功: %+v", r)
	}
	if d := h.clk.waitDurs(t, len(wantWaits)+2)[len(wantWaits)+1]; d != 10*time.Second {
		t.Fatalf("成功后应恢复原间隔: %v", d)
	}
}

func TestSchedulerRunNowIgnoresBackoff(t *testing.T) {
	var calls atomic.Int32
	run := func(context.Context) (*report.Report, error) {
		calls.Add(1)
		return nil, ErrFailed
	}
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: time.Minute, ConfigHash: "h", Run: run})
	h.next(t) // 首次
	h.clk.waitDurs(t, 2)
	if err := h.s.RunNow("a"); err != nil {
		t.Fatal(err)
	}
	r := h.next(t)
	if !r.Manual || calls.Load() != 2 {
		t.Fatalf("RunNow 应立即运行一次: %+v calls=%d", r, calls.Load())
	}
	if err := h.s.RunNow("nope"); !errors.Is(err, ErrNotScheduled) {
		t.Fatalf("未知任务: %v", err)
	}
}

func TestSchedulerNoReentry(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var calls atomic.Int32
	run := func(ctx context.Context) (*report.Report, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return &report.Report{Status: report.StatusOK}, nil
	}
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: time.Second, ConfigHash: "h", Run: run})
	<-started
	if err := h.s.RunNow("a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("运行中触发应返回 ErrBusy: %v", err)
	}
	h.clk.Advance(time.Hour)
	close(release)
	h.next(t)
	if calls.Load() != 1 {
		t.Fatalf("运行期间不得重入: calls=%d", calls.Load())
	}
}

func TestSchedulerGlobalConcurrencyLimit(t *testing.T) {
	var cur, peak atomic.Int32
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	run := func(context.Context) (*report.Report, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- struct{}{}
		<-release
		cur.Add(-1)
		return &report.Report{Status: report.StatusOK}, nil
	}
	h := newSchedHarness(t, SchedulerOptions{}) // 默认并发 3
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		h.s.Upsert(Task{ID: id, Interval: time.Hour, ConfigHash: "h", Run: run})
	}
	for i := 0; i < 3; i++ {
		<-started
	}
	for i := 0; i < 6; i++ {
		release <- struct{}{}
		if i < 3 {
			<-started
		}
		h.next(t)
	}
	if peak.Load() != 3 {
		t.Fatalf("并发峰值应恰为 3: %d", peak.Load())
	}
}

func TestSchedulerCustomConcurrency(t *testing.T) {
	var cur, peak atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	run := func(context.Context) (*report.Report, error) {
		n := cur.Add(1)
		if n > peak.Load() {
			peak.Store(n)
		}
		started <- struct{}{}
		<-release
		cur.Add(-1)
		return &report.Report{Status: report.StatusOK}, nil
	}
	h := newSchedHarness(t, SchedulerOptions{MaxConcurrent: 1})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: run})
	h.s.Upsert(Task{ID: "b", Interval: time.Hour, ConfigHash: "h", Run: run})
	<-started
	release <- struct{}{}
	h.next(t)
	<-started
	release <- struct{}{}
	h.next(t)
	if peak.Load() != 1 {
		t.Fatalf("并发上限 1: peak=%d", peak.Load())
	}
}

func TestSchedulerUpsertReschedulesOnlyWhenHashChanges(t *testing.T) {
	var callsA, callsB atomic.Int32
	runA := func(ctx context.Context) (*report.Report, error) {
		callsA.Add(1)
		return &report.Report{Status: report.StatusOK}, nil
	}
	runB := func(ctx context.Context) (*report.Report, error) {
		callsB.Add(1)
		return &report.Report{Status: report.StatusOK, State: "b"}, nil
	}
	h := newSchedHarness(t, SchedulerOptions{})
	if !h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h1", Run: runA}) {
		t.Fatal("新任务应返回 true")
	}
	h.next(t)
	if h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h1", Run: runB}) {
		t.Fatal("hash 不变应返回 false")
	}
	if !h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h2", Run: runB}) {
		t.Fatal("hash 变化应返回 true")
	}
	r := h.next(t)
	if r.Report.State != "b" || callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("改配置后应立即用新函数重排: A=%d B=%d %+v", callsA.Load(), callsB.Load(), r)
	}
}

func TestSchedulerUpsertCancelsInFlightRunAndDropsItsResult(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	old := func(ctx context.Context) (*report.Report, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h1", Run: old})
	<-started
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h2", Run: okRun("new")})
	<-canceled
	r := h.next(t)
	if r.Report == nil || r.Report.State != "new" {
		t.Fatalf("被取代的旧运行结果不应回调，首个结果应来自新任务: %+v", r)
	}
}

func TestSchedulerRemoveCancelsSchedule(t *testing.T) {
	h := newSchedHarness(t, SchedulerOptions{Jitter: func(time.Duration) time.Duration { return time.Minute }})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: okRun("")})
	h.clk.waitDurs(t, 1)
	if !h.s.Remove("a") {
		t.Fatal("应返回 true")
	}
	if h.s.Remove("a") {
		t.Fatal("重复删除应返回 false")
	}
	h.clk.Advance(24 * time.Hour)
	h.s.Stop()
	select {
	case r := <-h.results:
		t.Fatalf("删除后不应再运行: %+v", r)
	default:
	}
	if err := h.s.RunNow("a"); !errors.Is(err, ErrNotScheduled) {
		t.Fatalf("删除后 RunNow: %v", err)
	}
}

func TestSchedulerTimeoutMapsToErrTimeout(t *testing.T) {
	run := func(ctx context.Context) (*report.Report, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, Timeout: 30 * time.Millisecond, ConfigHash: "h", Run: run})
	r := h.next(t)
	if !errors.Is(r.Err, ErrTimeout) {
		t.Fatalf("应为 ErrTimeout: %v", r.Err)
	}
}

func TestSchedulerPlainErrorWrappedAsFailed(t *testing.T) {
	cause := errors.New("x")
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: func(context.Context) (*report.Report, error) { return nil, cause }})
	r := h.next(t)
	if !errors.Is(r.Err, ErrFailed) || !errors.Is(r.Err, cause) {
		t.Fatalf("普通错误应包装为 ErrFailed: %v", r.Err)
	}
}

// logRecorder 收集日志记录。
type logRecorder struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	l.recs = append(l.recs, r)
	l.mu.Unlock()
	return nil
}
func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecorder) WithGroup(string) slog.Handler      { return l }
func (l *logRecorder) count(level slog.Level) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.recs {
		if r.Level == level {
			n++
		}
	}
	return n
}

func TestSchedulerDedupsRepeatedErrorLogs(t *testing.T) {
	var msg atomic.Value
	msg.Store("e1")
	run := func(context.Context) (*report.Report, error) { return nil, errors.New(msg.Load().(string)) }
	rec := &logRecorder{}
	h := newSchedHarness(t, SchedulerOptions{Logger: slog.New(rec)})
	h.s.Upsert(Task{ID: "a", Interval: time.Second, ConfigHash: "h", Run: run})
	h.next(t)
	for i := 0; i < 3; i++ {
		h.clk.Advance(h.clk.waitDurs(t, i+2)[i+1]) // 等下一次 After 登记后再推进
		h.next(t)
	}
	if n := rec.count(slog.LevelWarn); n != 1 {
		t.Fatalf("相同错误应只记一次: %d", n)
	}
	msg.Store("e2")
	h.clk.Advance(h.clk.waitDurs(t, 5)[4])
	h.next(t)
	if n := rec.count(slog.LevelWarn); n != 2 {
		t.Fatalf("不同错误应再记一次: %d", n)
	}
}

func TestSchedulerLogsSkippedRunNow(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	run := func(ctx context.Context) (*report.Report, error) {
		close(started)
		<-release
		return &report.Report{Status: report.StatusOK}, nil
	}
	rec := &logRecorder{}
	h := newSchedHarness(t, SchedulerOptions{Logger: slog.New(rec)})
	h.s.Upsert(Task{ID: "a", Interval: time.Second, ConfigHash: "h", Run: run})
	<-started
	_ = h.s.RunNow("a")
	close(release)
	h.next(t)
	if rec.count(slog.LevelInfo) < 1 {
		t.Fatal("跳过应记录日志")
	}
}

func TestSchedulerStopWaitsForRunsAndIsIdempotent(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	run := func(ctx context.Context) (*report.Report, error) {
		close(started)
		<-ctx.Done()
		finished.Store(true)
		return nil, ctx.Err()
	}
	clk := newRecClock()
	s := NewScheduler(context.Background(), SchedulerOptions{Clock: clk, Jitter: zeroJitter}, func(TaskResult) {})
	s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: run})
	<-started
	s.Stop()
	if !finished.Load() {
		t.Fatal("Stop 应等运行中的任务退出")
	}
	s.Stop()
	if s.Upsert(Task{ID: "b", Interval: time.Hour, ConfigHash: "h", Run: okRun("")}) {
		t.Fatal("Stop 后 Upsert 应被忽略")
	}
}

func TestSchedulerParentContextCancelStopsTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clk := newRecClock()
	s := NewScheduler(ctx, SchedulerOptions{Clock: clk, Jitter: func(time.Duration) time.Duration { return time.Minute }}, func(TaskResult) {})
	s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: okRun("")})
	clk.waitDurs(t, 1)
	cancel()
	s.Stop() // 应立即返回
}
