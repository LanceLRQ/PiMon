package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	watchdogService = "pimon-session-watchdog.service"
	watchdogUnit    = "/etc/systemd/system/" + watchdogService
)

// watchdogUnitContent 生成会话看门狗的 unit：root 运行，需要 loginctl 与 systemctl restart lightdm 的权限；
// 桌面用户名已在 resolveKioskUser 里校验过字符集，不会注入多余的 unit 指令。
func watchdogUnitContent(desktopUser string) string {
	return `[Unit]
Description=PiMon session watchdog
After=lightdm.service

[Service]
ExecStart=` + binPath + ` session-watchdog --user ` + desktopUser + `
User=root
Restart=always
RestartSec=5s

[Install]
WantedBy=graphical.target
`
}

// installWatchdog 写入并启用会话看门狗；unit 变化或二进制升级且服务正在运行时重启它。
func (r *run) installWatchdog(ctx context.Context) error {
	if !r.opts.Kiosk {
		return nil
	}
	out, err := r.systemctl(ctx, "is-active", watchdogService)
	var ee *ExitError
	if err != nil && !errors.As(err, &ee) {
		return err
	}
	wasActive := err == nil && strings.TrimSpace(out) == "active"

	want := watchdogUnitContent(r.kioskUser)
	cur, err := r.FS.ReadFile(watchdogUnit)
	changed := false
	switch {
	case err == nil && string(cur) == want:
		r.rep.existed("systemd 单元 "+watchdogUnit, "内容相同")
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("读取 %s: %w", watchdogUnit, err)
	default:
		if err := r.FS.WriteFile(watchdogUnit, []byte(want), 0o644, nil); err != nil {
			return fmt.Errorf("写入 %s: %w", watchdogUnit, err)
		}
		changed = true
		r.rep.done("写入 systemd 单元 "+watchdogUnit, "")
	}
	if _, err := r.systemctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if _, err := r.systemctl(ctx, "enable", "--now", watchdogService); err != nil {
		return fmt.Errorf("enable --now %s: %w", watchdogService, err)
	}
	r.rep.done("启用并启动 "+watchdogService, "")
	if wasActive && (changed || r.upgraded) {
		if _, err := r.systemctl(ctx, "restart", watchdogService); err != nil {
			return fmt.Errorf("restart %s: %w", watchdogService, err)
		}
		why := "二进制已升级"
		if changed {
			why = "单元文件已变化"
		}
		r.rep.done("重启 "+watchdogService, why)
	}
	return nil
}
