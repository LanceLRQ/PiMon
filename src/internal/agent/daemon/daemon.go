// Package daemon 是 agent 守护进程的装配：按本地配置拨入（凭据重连 + mDNS 兜底）或拨出（监听 + 配对窗口），
// 管理唯一的当前会话（hello、hello_ok、incompatible、吊销、顶替），把运行器挂到会话上，
// 维护日志环与 logs_get，并为控制套接字（C4c）提供状态与操作。
//
// 守护进程只在 ctx 结束时退出：吊销、凭据失效、配置错误时保持空闲，等 repin、reload 或重新安装，
// 避免服务管理器反复拉起形成循环。凭据、凭据哈希与配对码不进日志与状态。
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/agent/runner"
	"github.com/LanceLRQ/PiMon/src/internal/agent/store"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/mdns"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

const (
	// RingCapacity 是日志环的建议容量（由 cmd 创建并注入）。
	RingCapacity = 2000
	// GCInterval 是清理长期未分配实例 state 的间隔（走 Clock）。
	GCInterval = 24 * time.Hour
)

// Config 是守护进程的依赖与注入点（C8 在同一进程、假时钟下运行完整守护进程）。
type Config struct {
	// Paths 是本模式的固定路径（配置、数据库、证书、插件目录）。
	Paths config.Paths
	// Clock 缺省 clock.Real{}；会话心跳、重连退避、配对窗口、GC、mDNS 窗口都走它。
	Clock clock.Clock
	// Log 缺省 slog.Default()；守护进程不修改 slog.Default。
	Log *slog.Logger
	// Ring 是 logs_get 的数据源（cmd 用 logging.NewWithRing 让 Log 同时写进它）；为空时 logs_get 返回空。
	Ring *logging.Ring
	// Builtins 是注入的内置插件（按 runs_on 含 agent 过滤），不读全局注册表。
	Builtins []runtime.Source
	// Jitter 是周期任务首次运行的抖动，为空用调度器默认。
	Jitter func(time.Duration) time.Duration
	// Rand 是重连退避抖动的随机源，返回 [0,1)；为空用 math/rand/v2。
	Rand func() float64
	// Listen 非空时覆盖配置里的拨出监听地址（测试用 127.0.0.1:0）。
	Listen string
	// OnListen 在拨出监听就绪后以实际地址调用（测试用）。
	OnListen func(addr string)
	// CtlSock 是控制套接字路径，空表示不开；已有进程在监听时 Run 返回错误（防止两个守护进程同时运行）。
	CtlSock string
	// Hostname 缺省 os.Hostname，用于 hello 与拨出 pair_ok 的 listener_info。
	Hostname func() (string, error)
	// MDNS 是拨入直拨失败时的兜底解析，缺省用 mdns.Lookup（窗口走 Clock）。
	MDNS func(ctx context.Context, name string) ([]netip.Addr, error)
}

// 错误。
var (
	// ErrNotConnected 表示当前没有已就绪（收到 hello_ok）的会话。
	ErrNotConnected = errors.New("未连接 hub")
	// ErrNotDialOut 表示操作只在拨出模式有效（如配对窗口）。
	ErrNotDialOut = errors.New("该操作只在拨出模式下可用")
	// ErrNotDialIn 表示操作只在拨入模式有效（如 repin）。
	ErrNotDialIn = errors.New("该操作只在拨入模式下可用")
	// ErrNotListening 表示拨出监听尚未就绪。
	ErrNotListening = errors.New("拨出监听尚未就绪")
)

// Daemon 是 agent 守护进程。用 New 创建，Run 运行；Status 与各操作可在 Run 期间并发调用。
//
// 锁的约定：cfgMu 串行化配置文件的「读取—修改—落盘」；mu 保护内存状态，持有 mu 时不做磁盘与网络操作、
// 不等待会话关闭（例外：runner.Attach 只创建调度器，在 mu 下调用以保证与会话结束互斥）。
type Daemon struct {
	cfg     Config
	clk     clock.Clock
	log     *slog.Logger
	reg     *plugins.Registry
	started atomic.Bool

	cfgMu sync.Mutex

	mu        sync.Mutex
	conf      config.Config // 最近一次读取或落盘的配置
	state     string
	lastErr   string
	incompat  *Incompatible
	hubVer    string
	since     time.Time
	cur       *conn                        // 已收到 hello_ok 的当前会话
	conns     map[*agentlink.Session]*conn // 全部未结束的会话
	window    *pairWindow                  // 拨出配对窗口
	listen    *listenInfo                  // 拨出监听信息
	runner    *runner.Runner               // Run 中设置
	runCancel context.CancelFunc           // 当前连接循环的取消函数；空表示空闲
	restart   bool                         // 已请求重建连接循环
	wake      chan struct{}                // 唤醒空闲等待
	closing   bool                         // Run 正在退出，不再起新协程
	wg        sync.WaitGroup               // spawn 起的协程

	// testHookLoaded 在连接循环每轮读完配置后调用（只供测试复现竞争窗口）。
	testHookLoaded func()
}

// New 按 cfg 创建守护进程，填好缺省依赖；不触碰磁盘与网络。
func New(cfg Config) *Daemon {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	if cfg.Hostname == nil {
		cfg.Hostname = os.Hostname
	}
	if cfg.MDNS == nil {
		clk := cfg.Clock
		cfg.MDNS = func(ctx context.Context, name string) ([]netip.Addr, error) {
			return mdns.Lookup(ctx, name, mdns.Options{Clock: clk})
		}
	}
	d := &Daemon{
		cfg: cfg, clk: cfg.Clock, log: cfg.Log,
		state: StateIdle,
		conns: make(map[*agentlink.Session]*conn),
		wake:  make(chan struct{}, 1),
	}
	d.reg = plugins.New(plugins.Config{Dir: cfg.Paths.PluginDir, Builtins: cfg.Builtins, Clock: cfg.Clock, Log: cfg.Log})
	d.reg.OnChange(d.pluginsChanged)
	return d
}

// Run 运行守护进程直到 ctx 结束，返回 nil；只能调用一次。数据目录、本地数据库打不开时返回错误。
// 退出前关闭全部会话并等待运行器、插件监视与后台协程结束。
func (d *Daemon) Run(ctx context.Context) error {
	if !d.started.CompareAndSwap(false, true) {
		return errors.New("守护进程已在运行或已结束")
	}
	if err := config.EnsureDataDir(d.cfg.Paths); err != nil {
		return fmt.Errorf("准备数据目录: %w", err)
	}
	// 控制套接字最先占位：第二个实例在打开库、迁移、扫描插件之前就因套接字被占用而失败。
	// 监听期间到达的连接留在 backlog，Serve 在运行器就绪后才开始处理；
	// 即使提前处理，Status/Repin/Reload/OpenPairWindow/UpgradeInfo 也不依赖运行器。
	var srv *ctl.Server
	if d.cfg.CtlSock != "" {
		var err error
		if srv, err = ctl.Listen(ctl.ServerConfig{Path: d.cfg.CtlSock, Backend: d, Log: d.log}); err != nil {
			return fmt.Errorf("打开控制套接字: %w", err)
		}
		defer func() { _ = srv.Close() }()
	}
	st, err := store.Open(d.cfg.Paths.DB, store.WithClock(d.clk))
	if err != nil {
		return fmt.Errorf("打开本地数据库: %w", err)
	}
	defer func() { _ = st.Close() }()

	r := runner.New(runner.Config{Registry: d.reg, Store: st, Clock: d.clk, Log: d.log, Jitter: d.cfg.Jitter})
	d.mu.Lock()
	d.runner = r
	d.mu.Unlock()
	d.reg.Scan() // 首次扫描也会通知变化：此时没有就绪会话，丢弃即可

	bg, stopBG := context.WithCancel(ctx)
	defer stopBG()
	ctlDone := make(chan struct{})
	go func() {
		defer close(ctlDone)
		if srv != nil {
			_ = srv.Serve(bg)
		}
	}()
	watchDone, err := d.reg.Watch(bg)
	if err != nil {
		d.log.Warn("监视插件目录失败，目录变化需 reload 才生效", "dir", d.cfg.Paths.PluginDir, "err", err)
	}
	gcTick := d.clk.After(GCInterval) // 计时器先于协程登记，测试据此同步假时钟
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		d.gcLoop(bg, r, gcTick)
	}()

	d.connLoop(ctx)

	d.mu.Lock()
	d.closing = true
	d.mu.Unlock()
	d.wg.Wait()
	stopBG()
	<-ctlDone // 控制套接字随 ctx 与连接循环同时开始关停，这里等它取消完在途请求并删除套接字文件
	<-gcDone
	if watchDone != nil {
		<-watchDone
	}
	d.log.Info("agent 守护进程已退出")
	return nil
}

// connLoop 按配置运行连接（拨入或拨出），直到 ctx 结束。连接停止（吊销、凭据失效、配置错误）后保持空闲，
// 等 Reload/Repin 唤醒；Reload/Repin 期间请求的重建会取消当前连接并立即按新配置重来。
func (d *Daemon) connLoop(ctx context.Context) {
	for ctx.Err() == nil {
		select { // 丢弃过期的唤醒
		case <-d.wake:
		default:
		}
		// 先清重建请求并登记本轮的取消函数，再读配置：读配置之后到达的 Repin/Reload 会取消本轮并保留 restart，
		// 不会被清掉而让本轮用旧配置一直跑下去。
		runCtx, cancel := context.WithCancel(ctx)
		d.mu.Lock()
		d.runCancel, d.restart = cancel, false
		d.mu.Unlock()
		conf, err := d.loadConfig()
		if err == nil && d.testHookLoaded != nil {
			d.testHookLoaded()
		}
		if err != nil {
			cancel()
			d.mu.Lock()
			d.runCancel = nil
			d.mu.Unlock()
			d.setStopped(StateConfigError, "配置错误："+err.Error())
			d.log.Error("读取 agent 配置失败，等待修正后 reload", "path", d.cfg.Paths.Config, "err", err)
			d.idle(ctx)
			continue
		}
		d.mu.Lock()
		d.state, d.lastErr, d.incompat = StateRunning, "", nil
		d.mu.Unlock()

		var stopped bool
		switch conf.Mode {
		case config.ModeDialIn:
			stopped = d.runDialIn(runCtx, conf)
		case config.ModeDialOut:
			stopped = d.runDialOut(runCtx, conf)
		}
		cancel()

		d.mu.Lock()
		d.runCancel = nil
		restart := d.restart
		d.restart = false
		d.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if restart || !stopped {
			continue
		}
		d.idle(ctx)
	}
}

// idle 等待 Reload/Repin 唤醒或 ctx 结束。
func (d *Daemon) idle(ctx context.Context) {
	select {
	case <-d.wake:
	case <-ctx.Done():
	}
}

// requestRestart 请求按磁盘上的配置重建连接循环：取消当前连接（若有），并唤醒空闲等待。
// 不等待旧连接结束，可在任何协程调用（但会话回调与重连 Policy 里不应调用 Reconnector.Stop，这里也不调用）。
func (d *Daemon) requestRestart() {
	d.mu.Lock()
	d.restart = true
	if d.runCancel != nil {
		d.runCancel()
	}
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// setStopped 记录连接停止后的状态与原因。
func (d *Daemon) setStopped(state, msg string) {
	d.mu.Lock()
	d.state, d.lastErr = state, msg
	d.mu.Unlock()
}

// setLastErr 记录最近错误（不改变状态）。
func (d *Daemon) setLastErr(msg string) {
	d.mu.Lock()
	d.lastErr = msg
	d.mu.Unlock()
}

// spawnLocked 在未退出时起一个受 wg 管理的协程；调用方持有 mu。
func (d *Daemon) spawnLocked(fn func()) bool {
	if d.closing {
		return false
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		fn()
	}()
	return true
}

func (d *Daemon) gcLoop(ctx context.Context, r *runner.Runner, tick <-chan time.Time) {
	for {
		select {
		case <-tick:
		case <-ctx.Done():
			return
		}
		tick = d.clk.After(GCInterval)
		n, err := r.GC(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			d.log.Warn("清理长期未分配实例的本地 state 失败", "err", err)
		case n > 0:
			d.log.Info("已清理长期未分配实例的本地 state", "count", n)
		}
	}
}

// loadConfig 从磁盘读取配置并记为当前配置。
func (d *Daemon) loadConfig() (config.Config, error) {
	c, err := config.Load(d.cfg.Paths.Config, euid())
	if err != nil {
		return config.Config{}, err
	}
	d.mu.Lock()
	d.conf = c
	d.mu.Unlock()
	return c, nil
}

// updateConfig 以磁盘上的配置为底（读不到时用内存中的），调用 fn 修改；fn 返回 false 表示无需改动。
// 有改动时原子落盘，成功后才更新内存中的配置。fn 应只在前提成立时修改（如凭据仍是被吊销的那个），
// 避免覆盖外部（install）刚写入的新值。
func (d *Daemon) updateConfig(fn func(*config.Config) bool) error {
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	c, err := config.Load(d.cfg.Paths.Config, euid())
	if err != nil {
		d.mu.Lock()
		c = d.conf
		d.mu.Unlock()
	}
	if !fn(&c) {
		return nil
	}
	if err := config.Save(d.cfg.Paths.Config, c); err != nil {
		return fmt.Errorf("保存 agent 配置: %w", err)
	}
	d.mu.Lock()
	d.conf = c
	d.mu.Unlock()
	return nil
}

func euid() int { return os.Geteuid() }
