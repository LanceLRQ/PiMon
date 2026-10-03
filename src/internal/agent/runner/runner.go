// Package runner 把 hub 经 agent 通道下发的 assign 应用为本机插件调度，
// 并回传 report、assign_result 与 run_result。
//
// 生命周期按会话划分：每个会话 Attach 一个 Binding，会话结束（Sender.Done）时自动 Close，
// 停止全部实例并清空内存中的配置与密钥；重连后等新的 assign 再恢复。
// 跨会话保留的只有：每实例运行锁、上次成功报告（内存）与插件私有 state（本地 store）。
package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// StateRetention 是插件私有 state 的保留期：超过这么久没有出现在任何 assign 里的 state 由 GC 删除。
const StateRetention = 30 * 24 * time.Hour

// Registry 是运行器需要的插件注册表能力（由 *plugins.Registry 实现）。
type Registry interface {
	Get(id string) (plugins.Plugin, bool)
}

// Store 是运行器需要的本地 state 存储能力（由 *store.Store 实现）。
// State 在没有记录时返回 sql.ErrNoRows。
type Store interface {
	State(ctx context.Context, id string) (string, error)
	PutState(ctx context.Context, id, pluginID, state string) error
	Touch(ctx context.Context, ids []string) error
	GC(ctx context.Context, before time.Time) (int64, error)
}

// Sender 是运行器对会话的最小依赖（*agentlink.Session 实现）：Send 不阻塞，Done 在会话结束时关闭。
type Sender interface {
	Send(agent.Message) error
	Done() <-chan struct{}
}

// Config 是运行器的依赖。
type Config struct {
	Registry Registry
	Store    Store
	// Clock 缺省为真实时钟。
	Clock clock.Clock
	// Log 缺省丢弃日志；不修改 slog.Default。
	Log *slog.Logger
	// Jitter 是周期任务首次运行的抖动，参数为间隔；为 nil 时用调度器的默认抖动。
	Jitter func(time.Duration) time.Duration
	// MaxConcurrent 是周期运行的并发上限，<= 0 时取调度器默认值。
	MaxConcurrent int
}

// Runner 持有跨会话的运行态，为每个会话创建 Binding。
type Runner struct {
	cfg Config

	mu sync.Mutex
	// locks 是每实例运行锁（容量 1 的 channel），跨 Binding 共享：会话被顶替时旧 Binding 的
	// 收尾运行与新 Binding 的运行不会并发写同一实例的 state。条目数以实例 id 为上限，不回收。
	locks map[string]chan struct{}
	// last 是每实例上次成功的报告，跨会话保留，实例不再出现在任何活跃 Binding 的 assign 里时删除。
	last map[string]*report.Report
	// assigned 是每个活跃 Binding 当前 assign 里的实例 id。
	assigned map[*Binding]map[string]bool
}

// New 创建运行器。Registry 与 Store 必填。
func New(cfg Config) *Runner {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Runner{
		cfg:      cfg,
		locks:    map[string]chan struct{}{},
		last:     map[string]*report.Report{},
		assigned: map[*Binding]map[string]bool{},
	}
}

// GC 先刷新当前仍被分配的实例的分配时间，再删除 StateRetention 内没有出现在任何 assign 里的 state，
// 返回删除条数。由守护进程每天调用一次。
func (r *Runner) GC(ctx context.Context) (int64, error) {
	r.mu.Lock()
	var ids []string
	seen := map[string]bool{}
	for _, set := range r.assigned {
		for id := range set {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	r.mu.Unlock()
	if err := r.cfg.Store.Touch(ctx, ids); err != nil {
		return 0, err
	}
	return r.cfg.Store.GC(ctx, r.cfg.Clock.Now().Add(-StateRetention))
}

// lock 返回实例的运行锁。
func (r *Runner) lock(id string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.locks[id]
	if l == nil {
		l = make(chan struct{}, 1)
		r.locks[id] = l
	}
	return l
}

// acquire 阻塞等实例运行锁（受 ctx 约束），供周期任务使用。
func (r *Runner) acquire(ctx context.Context, id string) (release func(), err error) {
	l := r.lock(id)
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// tryAcquire 非阻塞尝试拿实例运行锁，供 run_now 使用。
func (r *Runner) tryAcquire(id string) (release func(), ok bool) {
	l := r.lock(id)
	select {
	case l <- struct{}{}:
		return func() { <-l }, true
	default:
		return nil, false
	}
}

func (r *Runner) lastFor(id string) *report.Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last[id]
}

// setLast 记录实例上次成功的报告；实例已不在任何活跃 assign 里时不记（结果来自已移除的配置）。
func (r *Runner) setLast(id string, rep *report.Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, set := range r.assigned {
		if set[id] {
			r.last[id] = rep
			return
		}
	}
}

// setAssigned 更新 b 当前分配的实例集合，并丢弃不再被任何活跃 Binding 分配的实例的上次报告。
// ids 为 nil 表示 b 已关闭。
func (r *Runner) setAssigned(b *Binding, ids map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ids == nil {
		// 断线不算移出 assign：上次报告跨会话保留，等下一次 assign 再按需丢弃。
		delete(r.assigned, b)
		return
	}
	r.assigned[b] = ids
	for id := range r.last {
		kept := false
		for _, set := range r.assigned {
			if set[id] {
				kept = true
				break
			}
		}
		if !kept {
			delete(r.last, id)
		}
	}
}
