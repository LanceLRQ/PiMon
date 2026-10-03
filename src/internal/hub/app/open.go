// Package app 组装中枢各服务，并实现 serve、setup-code、reset-password、restore 子命令。
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/agentdist"
	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/internal/hub/api"
	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/history"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screenstate"
	"github.com/LanceLRQ/PiMon/src/internal/hub/sdnotify"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/seed"
	"github.com/LanceLRQ/PiMon/src/internal/hub/settings"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/internal/hub/system"
	"github.com/LanceLRQ/PiMon/src/internal/hub/tlscert"
	"github.com/LanceLRQ/PiMon/src/internal/hub/webui"
	"github.com/LanceLRQ/PiMon/src/internal/hub/ws"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/hubself"
)

const (
	// 登录失败限流：10 次、锁 15 分钟（D44）。
	loginMaxFailures = 10
	loginLockTime    = 15 * time.Minute
)

// App 是装配完成的中枢。
type App struct {
	cfg  config.Config
	opts options

	db          *store.DB
	box         *secret.Box
	settings    *settings.Service
	admins      *auth.Admins
	setupCodes  *auth.SetupCodes
	sessions    *auth.Sessions
	screen      *auth.ScreenTokens
	backups     *backup.Service
	plugins     *plugins.Registry
	instances   *instances.Service
	screens     *screens.Service
	screenState *screenstate.Service
	devices     *devices.Store
	presence    *devices.Presence
	// deviceViews 是设备的对外视图，REST 与 WS 共用。
	deviceViews *devices.Viewer
	// machineNotes 把设备状态变化转成其上实例的变化通知。
	machineNotes *machineNotifier
	agents       *agentsrv.Server
	// dialout 是拨出管理器（hub 主动连公网 VPS 上的 agent），Serve 时启动。
	dialout *agentsrv.DialOut
	// dispatcher 是远程实例的下发器：既是实例服务的 Remote，也是 agent 通道的 Router。
	dispatcher *agentsrv.Dispatcher
	// cert 是网页 HTTPS 与 agent 通道共用的中枢证书，spki 是其公钥指纹。
	cert     tls.Certificate
	spki     certs.SPKI
	history  *history.Service
	ws       *ws.Hub
	notifier *sdnotify.Notifier
	handler  http.Handler
	// listenAddr 是 Serve 实际监听的地址（host:port），设置码页据此拼手机访问地址。
	listenAddr atomic.Value
	// agentAddr 是 agent 通道实际监听的地址，未启用时为空。
	agentAddr atomic.Value
	closed    bool
}

// Open 按固定顺序启动：建数据目录 → 密钥 → 打开数据库 → 版本变化时升级前备份 →
// 迁移并记录版本 → 加载设置 → 装配服务。任何一步失败都会释放已打开的资源。
func Open(ctx context.Context, cfg config.Config, opt ...Option) (*App, error) {
	o := defaultOptions()
	for _, f := range opt {
		f(&o)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}
	box, err := secret.LoadOrCreate(cfg.SecretKeyPath())
	if err != nil {
		return nil, fmt.Errorf("准备加密密钥: %w", err)
	}
	dbExisted := fileExists(cfg.DBPath())
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, opts: o, db: db, box: box}
	if err := a.assemble(ctx, dbExisted); err != nil {
		a.stopAgents()
		_ = db.Close()
		return nil, err
	}
	return a, nil
}

func (a *App) assemble(ctx context.Context, dbExisted bool) error {
	o := a.opts
	backups := func(set func() model.Settings) *backup.Service {
		return backup.New(backup.Config{
			DB: a.db, Clock: o.clk, SecretPath: a.cfg.SecretKeyPath(),
			Dir: a.cfg.BackupDir(), Settings: set,
		})
	}

	// 设置加载在迁移之后才可用，升级前备份用默认设置即可（只需要备份目录与保留数）。
	lastVersion := ""
	if dbExisted {
		v, err := a.db.AppVersion(ctx)
		if err != nil {
			return fmt.Errorf("读取上次运行的版本: %w", err)
		}
		lastVersion = v
	}
	if dbExisted {
		// 降级检测必须在升级前备份之前，否则旧二进制打开新库会先多生成一份备份再报错。
		if err := a.db.CheckNotNewer(ctx); err != nil {
			return fmt.Errorf("数据库迁移: %w", err)
		}
	}
	if lastVersion != "" && lastVersion != o.version {
		pre := backups(func() model.Settings { return settings.Defaults("UTC") })
		if _, err := pre.Create(ctx, backup.ReasonPreUpgrade); err != nil {
			return fmt.Errorf("升级前备份失败，已中止启动: %w", err)
		}
		slog.Info("已生成升级前备份", "from", lastVersion, "to", o.version)
	}

	if err := a.db.Migrate(ctx); err != nil {
		return fmt.Errorf("数据库迁移: %w", err)
	}
	if err := a.db.SetAppVersion(ctx, o.version); err != nil {
		return fmt.Errorf("记录版本: %w", err)
	}

	st, err := settings.Load(ctx, a.db, settings.WithGetenv(o.getenv))
	if err != nil {
		return fmt.Errorf("加载全局设置失败，请检查数据库中的设置内容（或从备份恢复）: %w", err)
	}
	a.settings = st
	a.admins = auth.NewAdmins(a.db, o.clk)
	a.setupCodes = auth.NewSetupCodes(a.db, o.clk, a.box)
	a.sessions = auth.NewSessions(a.db, o.clk)
	a.screen = auth.NewScreenTokens(a.db, o.clk, a.cfg.ScreenTokenPath())
	a.backups = backups(st.Get)
	a.notifier = sdnotify.New(o.getenv, os.Getpid)
	a.plugins = plugins.New(plugins.Config{
		Dir: a.cfg.PluginDir(), DB: a.db, Clock: o.clk, Builtins: runtime.Builtins(),
	})
	if _, err := a.plugins.Scan(ctx); err != nil {
		return fmt.Errorf("扫描插件: %w", err)
	}
	a.history = history.New(history.Config{
		DB: a.db, Clock: o.clk,
		// 保留期每次清理与查询时现取，设置在运行时修改后立即生效。
		Retention: func() model.RetentionSettings { return st.Get().Retention },
	})
	a.instances = instances.New(instances.Config{
		DB: a.db, Box: a.box, Clock: o.clk, Plugins: a.plugins, History: a.history,
	})
	proxyStore := proxies.New(proxies.Config{
		DB: a.db, Box: a.box, Clock: o.clk,
		Referrers: a.instances,
		// 代理被修改或删除后，引用它的实例要按新内容重新排程，拨出设备按新代理重连（代理已删除则停止拨号）。
		OnChange: a.onProxiesChanged,
	})
	a.instances.UseProxies(proxyStore)
	a.screens = screens.New(screens.Config{DB: a.db, Clock: o.clk, Plugins: a.plugins, Instances: a.instances})
	a.instances.UseScreenRefs(a.screens)
	a.screenState = screenstate.New(screenstate.Config{
		DB: a.db, Clock: o.clk,
		// 时区在运行时可改，每次计算时现取。
		Timezone: func() string { return st.Get().Timezone },
		// 每日重启设置同样现取，KioskStatus.NextRestart 随设置变化即时重算。
		DailyRestart: func() model.DailyRestartSettings { return st.Get().Screen.DailyRestart },
	})
	if err := a.screenState.Load(ctx); err != nil {
		return fmt.Errorf("加载屏幕时段计划: %w", err)
	}
	a.devices = devices.New(devices.Config{DB: a.db, Box: a.box, Clock: o.clk})
	known, err := a.devices.List(ctx)
	if err != nil {
		return fmt.Errorf("读取设备列表: %w", err)
	}
	knownIDs := make([]string, len(known))
	for i, d := range known {
		knownIDs[i] = d.ID
	}
	// 已知设备在 hub 启动后的宽限期内为 pending，之后仍未连上才算 offline。
	a.presence = devices.NewPresence(o.clk, o.clk.Now(), knownIDs)
	// 实例按运行位置校验、展示时查设备属性与在线状态；远程下发器（Remote）在 agent 通道服务建好后接入。
	a.instances.UseDevices(newDeviceStates(a.devices, a.presence))
	// 设备在线状态或属性变化后，刷新其上全部实例的展示；回调只登记设备，不读库、不阻塞。
	a.machineNotes = newMachineNotifier(a.instances.NotifyMachine)
	a.presence.OnChange(a.machineNotes.Submit)
	a.devices.OnChange(func(id string, _ bool) { a.machineNotes.Submit(id) })
	// 证书总是加载：agent 通道无论网页是否启用 HTTPS 都要用它，指纹也供配对命令展示。
	if a.cert, err = tlscert.LoadOrCreate(a.cfg.CertPath(), a.cfg.KeyPath(), o.clk.Now(), o.tlsEnv); err != nil {
		return fmt.Errorf("准备中枢证书: %w", err)
	}
	if a.spki, err = certs.SPKIOf(a.cert); err != nil {
		return fmt.Errorf("计算证书指纹: %w", err)
	}
	if a.agents, err = agentsrv.New(agentsrv.Config{
		Devices: a.devices, Presence: a.presence, Plugins: a.plugins, Clock: o.clk, HubVersion: o.version,
	}); err != nil {
		return fmt.Errorf("创建 agent 通道服务: %w", err)
	}
	// 下发器同时接入实例服务（实例变化 → 合并下发）与 agent 通道（设备就绪 → 全量下发、回执）。
	if a.dispatcher, err = agentsrv.NewDispatcher(agentsrv.DispatcherConfig{
		Server: a.agents, Instances: a.instances, Devices: a.devices, Plugins: a.plugins, Clock: o.clk,
		HubVersion: o.version,
		// 网页端口取 Serve 实际监听的地址；Serve 先于 agent 通道接受连接写入它。
		WebPort: func() int {
			v, _ := a.listenAddr.Load().(string)
			return agentsrv.WebPortFrom(v)
		},
	}); err != nil {
		return fmt.Errorf("创建下发器: %w", err)
	}
	a.instances.UseRemote(a.dispatcher)
	a.agents.UseRouter(a.dispatcher)
	if a.dialout, err = agentsrv.NewDialOut(agentsrv.DialOutConfig{Server: a.agents, Proxies: proxyStore}); err != nil {
		return fmt.Errorf("创建拨出管理器: %w", err)
	}
	// 设备视图在 REST 与 WS 里共用：在线状态读内存，连接错误来自拨出管理器，时钟偏移来自会话。
	a.deviceViews = devices.NewViewer(devices.ViewerConfig{
		Store: a.devices, Presence: a.presence, ConnError: a.dialout.ConnError, ClockSkew: a.agents.ClockSkew,
	})
	// hub-self 的统计来源在 instances 与 history 就绪后才能绑定，先于 Load 以便首次采集就有数据。
	hubself.Bind(hubStats{
		inst: a.instances, hist: a.history, started: o.clk.Now(), dataDir: a.cfg.DataDir,
		// 有屏幕会话的 WebSocket 连着即在线；来源已接入，未连过也是离线而非未知。
		screenOnline: func() (bool, bool) {
			online, _ := a.screenState.Online()
			return online, true
		},
		screenStatus: a.screenState.Status,
		onlineAgents: a.presence.Online,
	})
	if err := a.instances.Load(ctx); err != nil {
		return fmt.Errorf("恢复实例状态: %w", err)
	}
	// 种子数据：布局从无到有时创建默认实例与默认首页；三份种子布局同时供自动选择网格取用。
	seeder := seed.New(seed.Config{
		Instances: a.instances, Layouts: a.screens,
		Language: func() string { return st.Get().Language },
	})
	a.screens.UseSeedLayouts(seeder.Layout)
	// 种子只是辅助数据且幂等：失败不阻止启动（管理端仍可进入修复），下次启动会重试。
	if err := seeder.Run(ctx); err != nil {
		slog.Warn("写入种子数据失败，已跳过，下次启动重试", "err", err)
	}
	// 屏幕令牌的失败限流由 /screen/auth 与 kiosk 的 WebSocket 握手共用。
	limiter := auth.NewLimiter(o.clk, loginMaxFailures, loginLockTime)
	// 实例与设置的变化经广播中心合并后推给 UI WebSocket 的订阅者。
	a.ws = ws.New(ws.Config{
		Clock: o.clk, Build: o.version, Instances: a.instances, Devices: a.deviceViews, Settings: st, Sessions: a.sessions,
		Layouts: a.screens, ScreenState: a.screenState, ScreenData: a.instances, Sink: a.screenState,
		KioskAuth: &kioskGate{tokens: a.screen, limiter: limiter}, Kiosk: a.screenState,
	})
	a.instances.OnChange(a.ws.NotifyInstance)
	// 设备的在线状态、属性与连接错误变化经广播中心合并后推给订阅 devices 的管理员。
	// 这三个回调可能在 agentsrv 的会话流程锁内触发，NotifyDevice 只做内存登记，读库与拼视图都在广播中心自己的协程里。
	a.presence.OnChange(a.ws.NotifyDevice)
	a.devices.OnChange(func(id string, _ bool) { a.ws.NotifyDevice(id) })
	a.dialout.OnConnErrChange(a.ws.NotifyDevice)
	// 屏幕布局、状态、一次性指令不进合并窗口，变化时立即推送。
	a.screens.OnChange(a.ws.NotifyLayout)
	a.screenState.OnChange(a.ws.NotifyScreenState)
	a.screenState.OnCommand(a.deliverScreenCommand)
	a.screenState.OnViewport(a.onViewportAdopted)
	a.sessions.OnRevoke(a.ws.RecheckSessions)
	st.OnChange(func() {
		a.ws.NotifySettings()
		// 时区变化后时段计划要按新时区重算。
		a.screenState.Refresh()
	})
	a.handler = api.New(api.Deps{
		AgentDist: agentdist.Embedded(),
		DataDir:   a.cfg.DataDir,
		ListenAddr: func() string {
			v, _ := a.listenAddr.Load().(string)
			return v
		},
		Plugins:     a.plugins,
		Instances:   a.instances,
		History:     a.history,
		Screens:     a.screens,
		ScreenState: a.screenState,
		System: system.New(system.Config{
			Clock: o.clk, Version: o.version, DataDir: a.cfg.DataDir, Plugins: a.plugins, Ring: o.logRing,
		}),
		Settings:       st,
		Hasher:         auth.Hasher{Params: o.params},
		Limiter:        limiter,
		SetupCodes:     a.setupCodes,
		Admins:         a.admins,
		Sessions:       a.sessions,
		ScreenTokens:   a.screen,
		Backups:        a.backups,
		Proxies:        proxyStore,
		Devices:        a.devices,
		DeviceLogs:     a.dispatcher,
		DialOut:        a.dialout,
		DeviceViews:    a.deviceViews,
		DeviceSessions: a.agents,
		AgentInfo: func() api.AgentChannel {
			return api.AgentChannel{Addr: a.AgentAddr(), Fingerprint: a.spki.String(), HubVersion: o.version}
		},
		Web: webui.New(webui.Embedded(), o.version),
		WS:  a.ws.Handler(),
	})
	return nil
}

// onProxiesChanged 在代理被修改或删除后扇出给实例调度与拨出管理器。
func (a *App) onProxiesChanged() {
	a.instances.Refresh()
	if a.dialout != nil {
		a.dialout.Refresh()
	}
}

// deliverScreenCommand 把一次性屏幕指令（refresh、switch）发给在线的屏幕会话；
// 至少一个屏幕收到即记为已送达，没有屏幕在线则保持未送达。
func (a *App) deliverScreenCommand(cmd screenstate.Command) {
	if a.ws.SendScreenControl(cmd.OpID, cmd.Action, cmd.ScreenID) == 0 {
		return
	}
	if err := a.screenState.MarkDelivered(context.Background(), cmd.OpID); err != nil {
		slog.Warn("标记屏幕指令已送达失败", "op", cmd.OpID, "err", err)
	}
}

// onViewportAdopted 在显示器 viewport 被采信后调用：布局仍是种子原版时按推荐网格自动换成对应的种子布局。
func (a *App) onViewportAdopted(v model.Viewport) {
	applied, err := a.screens.AutoSelectGrid(context.Background(), v)
	if err != nil {
		slog.Warn("按显示器自动选择网格失败", "err", err)
		return
	}
	if applied {
		slog.Info("已按显示器自动选择网格", "viewport", fmt.Sprintf("%dx%d", v.W, v.H))
	}
}

// Handler 返回完整的 HTTP 处理器，测试用 httptest 直接挂载。
func (a *App) Handler() http.Handler { return a.handler }

// HistoryWriteErrors 返回数值历史写盘失败的累计次数；
// hub-self 把它与实例当前状态落盘失败数（instances.Service.WriteErrors）相加作为「写库错误」。
func (a *App) HistoryWriteErrors() int64 { return a.history.WriteErrors() }

// AgentAddr 返回 agent 通道实际监听的地址（host:port）；未启用或尚未监听时为空。
func (a *App) AgentAddr() string {
	v, _ := a.agentAddr.Load().(string)
	return v
}

// AgentSPKI 返回中枢证书的 SPKI 指纹，agent 配对时用它钉扎。
func (a *App) AgentSPKI() certs.SPKI { return a.spki }

// stopAgents 按「拨出管理器、下发器、会话服务、在线状态、设备通知、广播中心」的顺序释放 agent 通道相关资源，须在关闭数据库之前调用。
// 调用前监听必须已停止（Serve 返回），否则新连接会在关闭后到达；拨出管理器先停，关停期间不再新建拨出会话；
// 下发器停止后会话的回调一律忽略，不再读库。
func (a *App) stopAgents() {
	if a.dialout != nil {
		a.dialout.Close()
	}
	if a.dispatcher != nil {
		a.dispatcher.Close()
	}
	if a.agents != nil {
		a.agents.Close()
	}
	if a.presence != nil {
		a.presence.Close()
	}
	if a.machineNotes != nil {
		a.machineNotes.Close()
	}
	// 广播中心最后关：前面各步触发的设备变化通知在它关闭后一律作废，之后不会再读库推送。
	if a.ws != nil {
		a.ws.Close()
	}
}

// Close 依次停止 agent 通道服务与在线状态计时协程，再关闭数据库；可重复调用。
func (a *App) Close() error {
	if a.closed {
		return nil
	}
	a.closed = true
	a.stopAgents()
	return a.db.Close()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}
