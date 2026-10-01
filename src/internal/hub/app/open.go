// Package app 组装中枢各服务，并实现 serve、setup-code、reset-password、restore 子命令。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/api"
	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/internal/hub/history"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/sdnotify"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/settings"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
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

	db         *store.DB
	box        *secret.Box
	settings   *settings.Service
	admins     *auth.Admins
	setupCodes *auth.SetupCodes
	sessions   *auth.Sessions
	screen     *auth.ScreenTokens
	backups    *backup.Service
	plugins    *plugins.Registry
	instances  *instances.Service
	history    *history.Service
	ws         *ws.Hub
	notifier   *sdnotify.Notifier
	handler    http.Handler
	closed     bool
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
	a.setupCodes = auth.NewSetupCodes(a.db, o.clk)
	a.sessions = auth.NewSessions(a.db, o.clk)
	a.screen = auth.NewScreenTokens(a.db, o.clk, a.cfg.ScreenTokenPath())
	a.backups = backups(st.Get)
	a.notifier = sdnotify.New(o.getenv)
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
		// 代理被修改或删除后，引用它的实例要按新内容重新排程。
		OnChange: a.instances.Refresh,
	})
	a.instances.UseProxies(proxyStore)
	// hub-self 的统计来源在 instances 与 history 就绪后才能绑定，先于 Load 以便首次采集就有数据。
	hubself.Bind(hubStats{inst: a.instances, hist: a.history, started: o.clk.Now(), dataDir: a.cfg.DataDir})
	if err := a.instances.Load(ctx); err != nil {
		return fmt.Errorf("恢复实例状态: %w", err)
	}
	// 实例与设置的变化经广播中心合并后推给 UI WebSocket 的订阅者。
	a.ws = ws.New(ws.Config{
		Clock: o.clk, Build: o.version, Instances: a.instances, Settings: st, Sessions: a.sessions,
	})
	a.instances.OnChange(a.ws.NotifyInstance)
	a.sessions.OnRevoke(a.ws.RecheckSessions)
	st.OnChange(a.ws.NotifySettings)
	a.handler = api.New(api.Deps{
		Plugins:      a.plugins,
		Instances:    a.instances,
		History:      a.history,
		Settings:     st,
		Hasher:       auth.Hasher{Params: o.params},
		Limiter:      auth.NewLimiter(o.clk, loginMaxFailures, loginLockTime),
		SetupCodes:   a.setupCodes,
		Admins:       a.admins,
		Sessions:     a.sessions,
		ScreenTokens: a.screen,
		Backups:      a.backups,
		Proxies:      proxyStore,
		Web:          webui.New(webui.Embedded(), o.version),
		WS:           a.ws.Handler(),
	})
	return nil
}

// Handler 返回完整的 HTTP 处理器，测试用 httptest 直接挂载。
func (a *App) Handler() http.Handler { return a.handler }

// HistoryWriteErrors 返回数值历史写盘失败的累计次数；
// hub-self 把它与实例当前状态落盘失败数（instances.Service.WriteErrors）相加作为「写库错误」。
func (a *App) HistoryWriteErrors() int64 { return a.history.WriteErrors() }

// Close 关闭数据库；可重复调用。
func (a *App) Close() error {
	if a.closed {
		return nil
	}
	a.closed = true
	return a.db.Close()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}
