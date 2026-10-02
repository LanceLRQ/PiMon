package instances

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// DefaultFlushInterval 是当前状态落盘的间隔。
const DefaultFlushInterval = 30 * time.Second

// 实例服务返回的哨兵错误。
var (
	// ErrNotFound 表示实例不存在。
	ErrNotFound = errors.New("实例不存在")
	// ErrPluginNotFound 表示实例所属插件不在注册表里。
	ErrPluginNotFound = errors.New("插件不存在")
	// ErrRunBusy 表示实例正在运行（定时采集或另一次保存并测试），等待至多插件超时仍未轮到。
	ErrRunBusy = errors.New("实例正在运行")
)

// ProxyResolver 按代理 id 取解析好的代理（由 proxies.Store 实现）。
// 代理不存在时应返回 proxies.ErrNotFound。
type ProxyResolver interface {
	Resolve(ctx context.Context, id string) (*proxy.Proxy, error)
}

// ScreenReferrers 回答"哪些 screen 引用了某实例"（由 screens.Service 实现）。
// 它只读布局，不会回调实例服务。
type ScreenReferrers interface {
	ScreensUsing(ctx context.Context, instanceID string) ([]model.ScreenRef, error)
}

// Config 是实例服务的依赖。
type Config struct {
	DB      *store.DB
	Box     *secret.Box
	Clock   clock.Clock
	Plugins *plugins.Registry
	// History 缺省为 NopHistory。
	History HistoryRecorder
	// Logger 缺省用 slog.Default()。
	Logger *slog.Logger
	// Jitter、MaxConcurrent 透传给调度器，缺省用调度器默认值（测试可注入零抖动）。
	Jitter        func(time.Duration) time.Duration
	MaxConcurrent int
	// FlushInterval 缺省为 DefaultFlushInterval。
	FlushInterval time.Duration
}

// taskInfo 记录一个实例当前在调度器（或 Streamer 管理器）里的任务。
// content 是排程时实例行的 config_hash，结果按它判断是否已过期。
type taskInfo struct {
	hash    string
	content string
	stream  bool
}

// Service 是实例服务。所有方法可并发使用。
type Service struct {
	db         *store.DB
	box        *secret.Box
	clk        clock.Clock
	reg        *plugins.Registry
	hist       HistoryRecorder
	log        *slog.Logger
	jitter     func(time.Duration) time.Duration
	maxConc    int
	flushEvery time.Duration

	proxies   ProxyResolver
	screens   ScreenReferrers
	cbMu      sync.RWMutex
	onChange  func(id string)
	writeErrs atomic.Int64

	// opMu 串行化会改变实例集合或调度的操作（增删改、暂停恢复、重新排程），
	// 避免删除与重新排程交错后把已删实例的任务又加回去。
	opMu sync.Mutex

	// mu 保护以下字段；持有 mu 时不访问数据库，也不调用调度器的阻塞方法。
	mu         sync.Mutex
	states     map[string]*instState
	tasks      map[string]taskInfo
	syncIssues map[string]*issue
	dropWarned map[string]string
	badWarned  map[string]bool
	// runLocks 是每实例的运行锁（容量 1 的 channel）：定时采集与保存并测试都先取锁，
	// 保证同一实例不重入（Ruling 48）。
	runLocks map[string]chan struct{}
	runCtx   context.Context
	sched    *runtime.Scheduler
	streams  *runtime.StreamManager
}

// New 创建实例服务。需要再调用 UseProxies 接入代理仓库，Load 恢复状态，Start 启动调度。
func New(c Config) *Service {
	s := &Service{
		db: c.DB, box: c.Box, clk: c.Clock, reg: c.Plugins, hist: c.History, log: c.Logger,
		jitter: c.Jitter, maxConc: c.MaxConcurrent, flushEvery: c.FlushInterval,
		states: map[string]*instState{}, tasks: map[string]taskInfo{}, syncIssues: map[string]*issue{}, dropWarned: map[string]string{},
		badWarned: map[string]bool{}, runLocks: map[string]chan struct{}{},
	}
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	if s.hist == nil {
		s.hist = NopHistory{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.flushEvery <= 0 {
		s.flushEvery = DefaultFlushInterval
	}
	return s
}

// UseProxies 接入代理仓库。代理仓库又需要本服务作为 Referrers，所以单独设置，
// 须在 Start 与任何请求之前调用。
func (s *Service) UseProxies(p ProxyResolver) { s.proxies = p }

// UseScreenRefs 接入屏幕布局，使删除实例前能查出引用它的 screen。
// 屏幕布局服务建好后再注入，须在 Start 与任何请求之前调用；不接入时视为没有引用。
func (s *Service) UseScreenRefs(r ScreenReferrers) { s.screens = r }

// Start 启动调度器、Streamer 管理器与每 30 秒一次的落盘循环，并订阅插件注册表的
// 重新扫描通知。ctx 结束后依次停止调度、停止 Streamer、做最后一次落盘，
// 返回的 channel 在这些全部完成后关闭。
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	// 定时采集的结果在任务函数里持运行锁写入状态（见 syncRow），调度器的结果回调无需再处理。
	sched := runtime.NewScheduler(ctx, runtime.SchedulerOptions{
		Clock: s.clk, MaxConcurrent: s.maxConc, Jitter: s.jitter, Logger: s.log,
	}, func(runtime.TaskResult) {})
	streams := runtime.NewStreamManager(ctx, runtime.StreamOptions{Clock: s.clk, Logger: s.log}, s.onStreamed)
	s.mu.Lock()
	s.sched, s.streams, s.runCtx = sched, streams, ctx
	s.mu.Unlock()
	s.reg.OnChange(func() { s.Resync(ctx) })
	s.Resync(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		flushCtx := context.WithoutCancel(ctx)
	loop:
		for {
			select {
			case <-s.clk.After(s.flushEvery):
				if err := s.Flush(flushCtx); err != nil {
					s.log.Warn("实例状态落盘失败", "err", err)
				}
			case <-ctx.Done():
				break loop
			}
		}
		sched.Stop()
		streams.Stop()
		if err := s.Flush(flushCtx); err != nil {
			s.log.Warn("停止时实例状态落盘失败", "err", err)
		}
	}()
	return done
}

// Refresh 用 Start 时的上下文重新对齐全部实例的调度；尚未 Start 时什么也不做。
// 给没有 ctx 的通知回调用（如代理被修改或删除之后）。
func (s *Service) Refresh() {
	s.mu.Lock()
	ctx := s.runCtx
	s.mu.Unlock()
	if ctx != nil {
		s.Resync(ctx)
	}
}

// onStreamed 写入 Streamer 产出的报告；按当前排程记录的配置版本判断是否过期。
func (s *Service) onStreamed(id string, rep *report.Report) {
	s.mu.Lock()
	content := s.tasks[id].content
	s.mu.Unlock()
	s.applyResult(id, content, rep, nil)
}
