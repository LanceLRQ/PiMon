package runner

import (
	"context"
	"strings"
	"sync"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// Binding 是运行器在一个会话上的实例集合与调度。Assign 与 RunNow 由会话 Handler 同步调用；
// 会话结束时自动 Close。
//
// 锁顺序：b.mu → 调度器/Streamer 管理器内部锁 → r.mu；运行协程只取 seqMu 与 r.mu，从不取 b.mu，
// 所以 Close 可以在释放 b.mu 后等待它们退出。
type Binding struct {
	r      *Runner
	s      Sender
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	// wg 跟踪 run_now 协程与会话监视协程。
	wg sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	sched   *runtime.Scheduler
	streams *runtime.StreamManager
	// specs 是当前 assign 的逐实例条目（含密钥），Close 时清空。
	specs map[string]*spec

	// seqMu 保护 seqs：每实例报告序号，会话内单调递增，跨重新下发不重置。
	seqMu sync.Mutex
	seqs  map[string]int64
}

// Attach 为会话 s 创建 Binding：新建调度器与 Streamer 管理器，并在 s.Done() 后自动 Close。
func (r *Runner) Attach(s Sender) *Binding {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Binding{
		r: r, s: s, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		specs: map[string]*spec{}, seqs: map[string]int64{},
	}
	noop := func(runtime.TaskResult) {} // 结果在运行闭包里发出，见 run.go
	b.sched = runtime.NewScheduler(ctx, runtime.SchedulerOptions{
		Clock: r.cfg.Clock, MaxConcurrent: r.cfg.MaxConcurrent, Jitter: r.cfg.Jitter, Logger: r.cfg.Log,
	}, noop)
	b.streams = runtime.NewStreamManager(ctx, runtime.StreamOptions{Clock: r.cfg.Clock, Logger: r.cfg.Log},
		func(string, *report.Report) {})
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		select {
		case <-s.Done():
			go b.Close() // Close 会等 wg，不能在 wg 内同步调用
		case <-ctx.Done():
		}
	}()
	return b
}

// Done 在 Close 完成（全部运行协程已退出）后关闭。
func (b *Binding) Done() <-chan struct{} { return b.done }

// Close 停止全部实例、等待运行协程退出，并清空内存中的配置与密钥。可重复调用，
// 不要在会话 Handler 里调用（会等待插件退出）。
func (b *Binding) Close() {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		sched, streams := b.sched, b.streams
		b.sched, b.streams, b.specs = nil, nil, nil
		b.mu.Unlock()

		b.cancel()
		sched.Stop()
		streams.Stop()
		b.wg.Wait()
		b.r.setAssigned(b, nil)
		b.seqMu.Lock()
		b.seqs = nil
		b.seqMu.Unlock()
		close(b.done)
	})
}

// nextSeq 返回实例的下一个报告序号（从 1 起）；Binding 已关闭时返回 0。
func (b *Binding) nextSeq(id string) int64 {
	b.seqMu.Lock()
	defer b.seqMu.Unlock()
	if b.seqs == nil {
		return 0
	}
	b.seqs[id]++
	return b.seqs[id]
}

// send 经会话发出消息；失败（会话已断开或发送队列满）只记日志，会话随后会关闭。
func (b *Binding) send(m agent.Message) {
	if err := b.s.Send(m); err != nil {
		b.r.cfg.Log.Debug("发送失败，会话即将关闭", "type", string(m.MsgType()), "err", err)
	}
}

// sendReport 发出一份周期报告；Binding 已关闭时丢弃。
func (b *Binding) sendReport(id string, o agent.RunOutcome) {
	seq := b.nextSeq(id)
	if seq == 0 || b.ctx.Err() != nil {
		return
	}
	b.send(&agent.Report{InstanceID: id, Seq: seq, RunOutcome: o})
}

// clipLog 把对端给出的字符串截到 64 字节再写日志。
func clipLog(v string) string {
	if len(v) <= 64 {
		return v
	}
	return strings.ToValidUTF8(v[:64], "") + "…"
}
