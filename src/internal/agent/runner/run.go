package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// job 是一次下发里某实例的不可变运行快照。in 含密钥，只在内存里，随 spec 与调度任务一起释放。
type job struct {
	id, pluginID string
	// hash 是下发摘要（AssignedInstance.ConfigHash），产生的结果都带它，hub 据此丢弃旧配置的结果。
	hash     string
	src      runtime.Source
	in       runtime.Input // Config、Secrets、Proxy、Clock；Last 与 State 每次运行时填
	interval time.Duration
	// timeout 是下发的 timeout_ms，> 0 时优先于 manifest 的超时。
	timeout time.Duration
	// contentHash 是下发的内容摘要（空表示未知），随 state 一起写库。
	contentHash string
	// ctx 从 Binding 的 ctx 派生：该 job 被替换、移出、回执失败或断线时取消。周期运行与 run_now 都受它约束，
	// commit 据它拒绝旧配置的结果写回。由 Assign 在登记前设置。
	ctx    context.Context
	cancel context.CancelFunc
}

// withJob 返回一个在 ctx 或 j.ctx 任一结束时都结束的 ctx。
func withJob(ctx context.Context, j *job) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(j.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// errReadState 是读本地 state 失败时的错误（固定文字，不带库路径等细节）。
var errReadState = fmt.Errorf("%w: 读取本地 state 失败 / failed to read local state", runtime.ErrFailed)

// result 是一次采集的结果；canceled 为真表示运行被取消（停止、重排、断线），不发结果、不写 state。
type result struct {
	rep      *report.Report
	err      error
	start    time.Time
	dur      time.Duration
	canceled bool
}

// collect 执行一次采集（调用方已持有实例运行锁）：读上次报告与本地 state，按下发超时加截止时间，
// 经 CollectWithTimeout 运行（错误已归类并脱敏）；成功时经 commit 写 state（连同内容摘要）并记下上次报告。
func (b *Binding) collect(ctx context.Context, j *job) result {
	clk := b.r.cfg.Clock
	start := clk.Now()
	in := j.in
	in.Last = b.r.lastFor(j.id)
	old, err := b.r.cfg.Store.State(ctx, j.id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		if ctx.Err() != nil {
			return result{canceled: true}
		}
		b.r.cfg.Log.Warn("读取本地 state 失败", "instance_id", clipLog(j.id), "err", err)
		return result{err: errReadState, start: start, dur: clk.Now().Sub(start)}
	}
	in.State = old

	runCtx := ctx
	if j.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, j.timeout)
		defer cancel()
	}
	rep, err := runtime.CollectWithTimeout(runCtx, j.src, in)
	if ctx.Err() != nil {
		return result{canceled: true}
	}
	res := result{rep: rep, err: err, start: start, dur: clk.Now().Sub(start)}
	if err != nil {
		return res
	}
	if !b.r.commit(ctx, j, rep) {
		return result{canceled: true}
	}
	return res
}

// outcome 把采集结果转换为协议的运行结果：报告去掉 state；错误按 ErrTimeout → timeout、其余 → failed。
func (res result) outcome(hash string) agent.RunOutcome {
	o := agent.RunOutcome{DurationMs: res.dur.Milliseconds(), CollectedAt: res.start, ConfigHash: hash}
	if res.err != nil {
		o.Error = res.err.Error()
		o.ErrorKind = agent.ErrorKindFailed
		if errors.Is(res.err, runtime.ErrTimeout) {
			o.ErrorKind = agent.ErrorKindTimeout
		}
		return o
	}
	c := *res.rep
	c.State = ""
	o.Report = &c
	return o
}

// periodicRun 是调度器的执行函数：阻塞等实例运行锁（受 ctx 约束），采集后发出 report（失败也发）。
// 被取消时不发结果，返回 ctx 的错误让调度器丢弃本次。
func (b *Binding) periodicRun(j *job) runtime.RunFunc {
	return func(ctx context.Context) (*report.Report, error) {
		ctx, cancel := withJob(ctx, j)
		defer cancel()
		release, err := b.r.acquire(ctx, j.id)
		if err != nil {
			return nil, err
		}
		defer release()
		res := b.collect(ctx, j)
		if res.canceled {
			return nil, context.Canceled
		}
		b.sendReport(j.id, res.outcome(j.hash))
		return res.rep, res.err
	}
}

// streamRun 包装事件驱动型插件：每次（重新）启动时读上次报告与本地 state，每份报告写 state、
// 带序号与下发摘要直接发出 report（不经管理器的 emit 回调，它拿不到产生报告时的下发摘要）。
type streamRun struct {
	b     *Binding
	j     *job
	inner runtime.Streamer
}

func (s *streamRun) Run(ctx context.Context, in runtime.Input, _ func(*report.Report)) error {
	b, j := s.b, s.j
	ctx, cancel := withJob(ctx, j)
	defer cancel()
	in.Last = b.r.lastFor(j.id)
	cur, err := b.r.cfg.Store.State(ctx, j.id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return errReadState
	}
	in.State = cur
	return s.inner.Run(ctx, in, func(rep *report.Report) {
		if ctx.Err() != nil || rep == nil {
			return
		}
		if !b.r.commit(ctx, j, rep) {
			return
		}
		now := b.r.cfg.Clock.Now()
		b.sendReport(j.id, result{rep: rep, start: now}.outcome(j.hash))
	})
}
