package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 重启退避：1s 起每次翻倍，上限 60s；单次运行持续超过上限则视为稳定，退避复位。
const (
	streamBackoffMin = time.Second
	streamBackoffMax = 60 * time.Second
)

// StreamBackoff 返回第 attempt 次（从 0 起）重启前的等待：1s、2s、4s……上限 60s。
func StreamBackoff(attempt int) time.Duration {
	d := streamBackoffMin
	for i := 0; i < attempt && d < streamBackoffMax; i++ {
		d *= 2
	}
	return min(d, streamBackoffMax)
}

// StreamTask 是一个事件驱动型插件的运行单元。
type StreamTask struct {
	// ID 是任务标识（实例 id）。
	ID string
	// ConfigHash 不变时 Upsert 不会重启它。
	ConfigHash string
	Streamer   Streamer
	// Input 是每次（重）启动时传给 Run 的输入；Clock 为 nil 时用管理器的时钟。
	Input Input
}

// StreamOptions 是 Streamer 管理器配置。
type StreamOptions struct {
	// Clock 缺省为真实时钟。
	Clock clock.Clock
	// Logger 缺省丢弃日志。
	Logger *slog.Logger
}

// StreamManager 管理 Streamer 的生命周期：Run 退出后（无论成败）按 1s→60s 退避重启，
// 配置 hash 不变不重启，变了停旧起新，Remove / Stop 时干净退出。
type StreamManager struct {
	ctx      context.Context
	cancel   context.CancelFunc
	clk      clock.Clock
	log      *slog.Logger
	onReport func(id string, rep *report.Report)

	mu      sync.Mutex
	tasks   map[string]*streamEntry
	stopped bool
	wg      sync.WaitGroup
}

type streamEntry struct {
	task   StreamTask
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	prev   <-chan struct{}
}

// NewStreamManager 创建管理器；parent 被取消等价于 Stop。
// onReport 在 Streamer 的 goroutine 里同步调用；任务被取消后 emit 的报告会被丢弃。
func NewStreamManager(parent context.Context, opts StreamOptions, onReport func(id string, rep *report.Report)) *StreamManager {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(parent)
	return &StreamManager{
		ctx: ctx, cancel: cancel, clk: opts.Clock, log: opts.Logger,
		onReport: onReport, tasks: map[string]*streamEntry{},
	}
}

// Upsert 加入或更新 Streamer，返回是否（重新）启动了。
// 同 ID 且 ConfigHash 不变什么都不做；变了则取消旧的，等它退出后立即起新的。
func (m *StreamManager) Upsert(t StreamTask) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return false
	}
	old := m.tasks[t.ID]
	if old != nil && old.task.ConfigHash == t.ConfigHash {
		return false
	}
	if t.Input.Clock == nil {
		t.Input.Clock = m.clk
	}
	e := &streamEntry{task: t, done: make(chan struct{})}
	e.ctx, e.cancel = context.WithCancel(m.ctx)
	if old != nil {
		old.cancel()
		e.prev = old.done
	}
	m.tasks[t.ID] = e
	m.wg.Add(1)
	go m.loop(e)
	return true
}

// Remove 停掉 Streamer，返回它是否存在。
func (m *StreamManager) Remove(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.tasks[id]
	if e == nil {
		return false
	}
	delete(m.tasks, id)
	e.cancel()
	return true
}

// Stop 停掉所有 Streamer 并等待退出，可重复调用；之后 Upsert 被忽略。
func (m *StreamManager) Stop() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
}

func (m *StreamManager) loop(e *streamEntry) {
	defer m.wg.Done()
	defer close(e.done)
	if e.prev != nil {
		select {
		case <-e.prev:
		case <-e.ctx.Done():
			return
		}
	}
	emit := func(rep *report.Report) {
		if e.ctx.Err() == nil {
			m.onReport(e.task.ID, rep)
		}
	}
	attempt := 0
	lastErr := ""
	for {
		start := m.clk.Now()
		err := runStreamer(e.ctx, e.task.Streamer, e.task.Input, emit)
		if e.ctx.Err() != nil {
			return
		}
		if m.clk.Now().Sub(start) >= streamBackoffMax {
			attempt = 0
		}
		m.logExit(e, err, &lastErr)
		delay := StreamBackoff(attempt)
		attempt++
		select {
		case <-m.clk.After(delay):
		case <-e.ctx.Done():
			return
		}
	}
}

// runStreamer 运行 Streamer 并把 panic 转成错误（文字只保留类型，不带可能含密钥的值）。
func runStreamer(ctx context.Context, st Streamer, in Input, emit func(*report.Report)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: Streamer panic（%T）", ErrFailed, r)
		}
	}()
	return st.Run(ctx, in, emit)
}

// logExit 相同的退出原因只记第一次；日志里的错误文字按该任务的输入脱敏。
func (m *StreamManager) logExit(e *streamEntry, err error, last *string) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg == *last {
		return
	}
	*last = msg
	if err != nil {
		m.log.Warn("Streamer 异常退出，将退避重启", "task", e.task.ID, "err", redactError(err, e.task.Input))
	} else {
		m.log.Info("Streamer 正常退出，将退避重启", "task", e.task.ID)
	}
}
