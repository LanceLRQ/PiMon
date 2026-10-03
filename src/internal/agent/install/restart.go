package install

import (
	"context"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
)

// Restart 重启已安装的 agent 服务（C7 升级后调用）：系统模式 systemctl restart，
// Linux 用户模式 systemctl --user restart，macOS 发 SIGTERM 由 KeepAlive 拉起（见 launchdRestart）。
func Restart(ctx context.Context, d Deps, p config.Paths) error {
	var err error
	switch {
	case d.GOOS == "darwin":
		err = launchdRestart(ctx, d)
	case p.System:
		_, err = d.Runner.Run(ctx, cmdSystemctl, "restart", ServiceName)
	default:
		_, err = d.Runner.Run(ctx, cmdSystemctl, "--user", "restart", ServiceName)
	}
	if err != nil {
		return fmt.Errorf("重启 agent 服务: %w", err)
	}
	return nil
}

// launchdRestart 用 launchctl kill SIGTERM gui/<uid>/<label> 让守护进程走 run 的优雅退出，
// 再由 plist 的 KeepAlive 自动拉起。不用 kickstart -k：它可能在优雅退出完成前强杀进程。
func launchdRestart(ctx context.Context, d Deps) error {
	_, err := d.Runner.Run(ctx, cmdLaunchctl, "kill", "SIGTERM", launchdService(d))
	return err
}
