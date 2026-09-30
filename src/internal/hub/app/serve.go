package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"

	"github.com/LanceLRQ/PiMon/src/internal/hub/sdnotify"
	"github.com/LanceLRQ/PiMon/src/internal/hub/server"
	"github.com/LanceLRQ/PiMon/src/internal/hub/tlscert"
)

// Serve 运行服务直到 ctx 结束。优雅关闭超时只记日志，不视为失败；
// 监听失败、证书失败等启动错误会返回。
func (a *App) Serve(ctx context.Context) error {
	if err := a.screen.EnsureExists(ctx); err != nil {
		return fmt.Errorf("准备屏幕令牌: %w", err)
	}
	if err := a.ensureSetupCode(ctx); err != nil {
		return err
	}

	var cert *tls.Certificate
	if a.settings.Get().HTTPSEnabled {
		c, err := tlscert.LoadOrCreate(a.cfg.CertPath(), a.cfg.KeyPath(), a.opts.clk.Now(), a.opts.tlsEnv)
		if err != nil {
			return fmt.Errorf("准备 HTTPS 证书: %w", err)
		}
		cert = &c
		slog.Info("HTTPS 已启用", "fingerprint", tlscert.Fingerprint(c))
	}
	ln, err := server.Listen(a.cfg.Addr)
	if err != nil {
		return err
	}

	bg, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		stop()
		wg.Wait()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.backups.RunDaily(bg)
	}()
	watchDone, err := a.plugins.Watch(bg)
	if err != nil {
		return fmt.Errorf("监视插件目录: %w", err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-watchDone
	}()
	// 调度器与 30 秒落盘循环：bg 结束后先停调度、再做最后一次落盘，wg 等它们做完。
	instDone := a.instances.Start(bg)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-instDone
	}()
	if iv, ok := a.notifier.WatchdogInterval(); ok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sdnotify.RunWatchdog(bg, a.opts.clk, iv, a.notifier.Notify)
		}()
	}

	onReady := func() {
		slog.Info("服务已就绪", "addr", ln.Addr().String(), "https", cert != nil)
		_ = a.notifier.Ready()
		if a.opts.onListen != nil {
			a.opts.onListen(ln.Addr().String())
		}
	}
	if err := server.Run(ctx, ln, a.handler, cert, onReady); err != nil {
		if ctx.Err() != nil {
			slog.Warn("优雅关闭未在限期内完成，已强制断开连接", "err", err)
			return nil
		}
		return err
	}
	return nil
}

// ensureSetupCode 在没有管理员且没有有效设置码时生成一个，
// 打印到 stderr 并记 warn 日志（设置码按设计允许进日志）。
func (a *App) ensureSetupCode(ctx context.Context) error {
	exists, err := a.admins.Exists(ctx)
	if err != nil || exists {
		return err
	}
	active, until, err := a.setupCodes.Active(ctx)
	if err != nil {
		return err
	}
	if active {
		_, _ = fmt.Fprintf(a.opts.stderr, "尚未设置管理员；已有未过期的设置码（有效期至 %s），如遗失可运行 pimon-hub setup-code 重新生成\n", until.Local().Format("2006-01-02 15:04:05"))
		slog.Info("尚未设置管理员；已有未过期的设置码，如遗失可运行 pimon-hub setup-code 重新生成", "expires_at", until)
		return nil
	}
	code, exp, err := a.setupCodes.Generate(ctx)
	if err != nil {
		return fmt.Errorf("生成设置码: %w", err)
	}
	_, _ = fmt.Fprintf(a.opts.stderr, "首次设置码: %s（有效期至 %s）\n", code, exp.Local().Format("2006-01-02 15:04:05"))
	slog.Warn("尚未设置管理员，已生成首次设置码", "setup_code", code, "expires_at", exp)
	return nil
}
