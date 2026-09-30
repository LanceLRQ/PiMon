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
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/sdnotify"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/settings"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
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
	a.handler = api.New(api.Deps{
		Settings:     st,
		Hasher:       auth.Hasher{Params: o.params},
		Limiter:      auth.NewLimiter(o.clk, loginMaxFailures, loginLockTime),
		SetupCodes:   a.setupCodes,
		Admins:       a.admins,
		Sessions:     a.sessions,
		ScreenTokens: a.screen,
		Backups:      a.backups,
		Proxies: proxies.New(proxies.Config{
			DB: a.db, Box: a.box, Clock: o.clk,
			// B6 由实例仓库替换：此前没有实例，代理恒无引用。
			Referrers: noReferrers{},
		}),
	})
	return nil
}

// noReferrers 是"无引用"的空实现：实例仓库落地前没有任何实例会引用代理。
type noReferrers struct{}

func (noReferrers) ListByProxy(context.Context, string) ([]model.ProxyReferrer, error) {
	return nil, nil
}

func (noReferrers) ResetToDirect(context.Context, string) error { return nil }

// Handler 返回完整的 HTTP 处理器，测试用 httptest 直接挂载。
func (a *App) Handler() http.Handler { return a.handler }

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
