package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// DefaultMaxConcurrent 是全局并发运行数的默认上限（树莓派同样取 3）。
const DefaultMaxConcurrent = 3

// maxDefaultJitter 是默认首次运行抖动的上限。
const maxDefaultJitter = 30 * time.Second

// 调度器返回的哨兵错误。
var (
	// ErrNotScheduled 表示任务不存在（从未加入或已移除）。
	ErrNotScheduled = errors.New("任务未在调度中")
	// ErrBusy 表示该任务正在运行，本次触发被跳过。
	ErrBusy = errors.New("任务正在运行")
)

// RunFunc 是一次采集的执行函数，由调用方（B6）闭包出实例的输入与 Source。
// ctx 已带超时；返回的错误由调度器归类为 ErrTimeout / ErrFailed。
type RunFunc func(ctx context.Context) (*report.Report, error)

// Task 是调度器看到的抽象任务，不含任何实例概念。
type Task struct {
	// ID 是任务标识（实例 id）。
	ID string
	// Interval 是正常运行间隔；<= 0 时用 manifest.DefaultInterval。
	Interval time.Duration
	// Timeout 是单次运行超时；<= 0 表示不加超时。
	Timeout time.Duration
	// ConfigHash 标识配置版本：Upsert 同 ID 任务时只有它变了才重排。
	// 调用方应把影响运行的一切（配置、密钥、代理、间隔、超时）折进该 hash。
	ConfigHash string
	// Run 是执行函数。
	Run RunFunc
}

// TaskResult 是一次运行的结果，交给结果回调，由调用方持久化
// （插件私有 state 在 Report.State 里）。
type TaskResult struct {
	ID string
	// Report 成功时非 nil。
	Report *report.Report
	// Err 失败时非 nil，可用 errors.Is 判断 ErrTimeout / ErrFailed。
	Err error
	// Manual 表示由 RunNow 触发。
	Manual bool
	// Failures 是本次运行之后的连续失败次数，成功为 0。
	Failures int
	// StartedAt 与 Duration 取自调度器的 Clock。
	StartedAt time.Time
	Duration  time.Duration
}

// SchedulerOptions 是调度器配置。
type SchedulerOptions struct {
	// Clock 缺省为真实时钟。
	Clock clock.Clock
	// MaxConcurrent 是全局并发上限，<= 0 时取 DefaultMaxConcurrent。
	MaxConcurrent int
	// Jitter 返回首次运行的随机延迟，参数为任务间隔；缺省为 [0, min(间隔, 30s)) 均匀随机。
	// 测试可注入确定值。
	Jitter func(interval time.Duration) time.Duration
	// Logger 缺省丢弃日志。
	Logger *slog.Logger
}

// Scheduler 为每个任务维护一个 Clock 驱动的定时计划。
//
// 语义要点：
//   - 间隔从上一次运行结束起算，因此定时触发永远不会与同一任务的运行重叠；
//     仅 RunNow 可能撞上运行中的任务，此时跳过并记录，返回 ErrBusy。
//   - 连续失败按 BackoffInterval 延长下次间隔，成功后恢复；RunNow 不受退避影响，
//     但它的结果同样计入连续失败数。
//   - 被 Upsert 取代或 Remove 的任务，其运行中的 ctx 被取消，结果不再回调。
type Scheduler struct {
	ctx      context.Context
	cancel   context.CancelFunc
	clk      clock.Clock
	log      *slog.Logger
	jitter   func(time.Duration) time.Duration
	sem      chan struct{}
	onResult func(TaskResult)

	mu      sync.Mutex
	tasks   map[string]*entry
	stopped bool
	wg      sync.WaitGroup
}

// entry 是一个任务的运行态。failures 与 lastErr 只在该任务自己的 goroutine 里读写。
type entry struct {
	task    Task
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	prev    <-chan struct{} // 被取代的旧 goroutine 的 done，新的等它退出再开始
	initial time.Duration
	trigger chan struct{}
	running atomic.Bool

	failures int
	lastErr  string
}

// NewScheduler 创建调度器；parent 被取消等价于 Stop。onResult 在任务 goroutine 里同步调用，
// 应尽快返回（可在其中调用 Upsert / Remove / RunNow）。
func NewScheduler(parent context.Context, opts SchedulerOptions, onResult func(TaskResult)) *Scheduler {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
	}
	if opts.Jitter == nil {
		opts.Jitter = defaultJitter
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(parent)
	return &Scheduler{
		ctx:      ctx,
		cancel:   cancel,
		clk:      opts.Clock,
		log:      opts.Logger,
		jitter:   opts.Jitter,
		sem:      make(chan struct{}, opts.MaxConcurrent),
		onResult: onResult,
		tasks:    map[string]*entry{},
	}
}

func defaultJitter(interval time.Duration) time.Duration {
	limit := min(interval, maxDefaultJitter)
	if limit <= 0 {
		return 0
	}
	return rand.N(limit)
}

// Upsert 加入或更新任务，返回是否发生了（重新）排程。
// 新任务首次运行带抖动；同 ID 且 ConfigHash 不变则什么都不做；
// hash 变了则取消旧计划（含运行中的一次）并立即按新配置重排。
func (s *Scheduler) Upsert(t Task) bool {
	if t.Interval <= 0 {
		t.Interval = manifest.DefaultInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	old := s.tasks[t.ID]
	if old != nil && old.task.ConfigHash == t.ConfigHash {
		return false
	}
	e := &entry{task: t, done: make(chan struct{}), trigger: make(chan struct{}, 1)}
	e.ctx, e.cancel = context.WithCancel(s.ctx)
	if old != nil {
		old.cancel()
		e.prev = old.done
	} else {
		e.initial = s.jitter(t.Interval)
	}
	s.tasks[t.ID] = e
	s.wg.Add(1)
	go s.loop(e)
	return true
}

// Remove 取消任务的计划与运行中的一次，返回任务是否存在。
func (s *Scheduler) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.tasks[id]
	if e == nil {
		return false
	}
	delete(s.tasks, id)
	e.cancel()
	return true
}

// RunNow 立即触发一次运行，不受退避影响。
// 任务正在运行（或已有一次待执行的触发）时跳过并记录，返回 ErrBusy；任务不存在返回 ErrNotScheduled。
func (s *Scheduler) RunNow(id string) error {
	s.mu.Lock()
	e := s.tasks[id]
	s.mu.Unlock()
	if e == nil {
		return ErrNotScheduled
	}
	if e.running.Load() {
		s.log.Info("任务正在运行，跳过手动触发", "task", id)
		return ErrBusy
	}
	select {
	case e.trigger <- struct{}{}:
		return nil
	default:
		s.log.Info("已有待执行的手动触发，跳过", "task", id)
		return ErrBusy
	}
}

// Stop 取消所有任务并等待 goroutine 退出，可重复调用；之后 Upsert 被忽略。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *Scheduler) loop(e *entry) {
	defer s.wg.Done()
	defer close(e.done)
	if e.prev != nil {
		select {
		case <-e.prev:
		case <-e.ctx.Done():
			return
		}
	}
	delay := e.initial
	for {
		manual := false
		select {
		case <-s.clk.After(delay):
		case <-e.trigger:
			manual = true
		case <-e.ctx.Done():
			return
		}
		e.running.Store(true)
		res, ok := s.execute(e, manual)
		e.running.Store(false)
		if !ok {
			return
		}
		select { // 丢弃运行期间积压的重复触发
		case <-e.trigger:
		default:
		}
		s.onResult(res)
		delay = BackoffInterval(e.task.Interval, e.failures)
	}
}

// execute 在并发许可内运行一次；任务在运行中被取消时 ok 为 false，结果不回调。
func (s *Scheduler) execute(e *entry, manual bool) (res TaskResult, ok bool) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-e.ctx.Done():
		return res, false
	}
	start := s.clk.Now()
	runCtx := e.ctx
	if e.task.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(e.ctx, e.task.Timeout)
		defer cancel()
	}
	rep, err := safeRun(runCtx, e.task.Run)
	if e.ctx.Err() != nil {
		return res, false
	}
	err = classify(e.ctx, runCtx, err)
	if err != nil {
		rep = nil
		e.failures++
		s.logFailure(e, err)
	} else {
		e.failures = 0
		if e.lastErr != "" {
			s.log.Info("采集已恢复", "task", e.task.ID)
			e.lastErr = ""
		}
	}
	return TaskResult{
		ID:        e.task.ID,
		Report:    rep,
		Err:       err,
		Manual:    manual,
		Failures:  e.failures,
		StartedAt: start,
		Duration:  s.clk.Now().Sub(start),
	}, true
}

// logFailure 相同错误文字只记第一次。
func (s *Scheduler) logFailure(e *entry, err error) {
	if msg := err.Error(); msg != e.lastErr {
		e.lastErr = msg
		s.log.Warn("采集失败", "task", e.task.ID, "err", err)
	}
}

// safeRun 执行 run 并把 panic 与空结果转成错误，避免拖垮调度 goroutine。
func safeRun(ctx context.Context, run RunFunc) (rep *report.Report, err error) {
	defer func() {
		if r := recover(); r != nil {
			rep, err = nil, fmt.Errorf("%w: 插件 panic: %v", ErrFailed, r)
		}
	}()
	rep, err = run(ctx)
	if err == nil && rep == nil {
		err = fmt.Errorf("%w: 采集未返回报告", ErrFailed)
	}
	return rep, err
}
