package install

import (
	"context"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
)

// Restart 重启已安装的 agent 服务（C7 升级后调用）：系统模式 systemctl restart，
// Linux 用户模式 systemctl --user restart，macOS launchctl kickstart -k gui/<uid>/<label>。
func Restart(ctx context.Context, d Deps, p config.Paths) error {
	var err error
	switch {
	case d.GOOS == "darwin":
		_, err = d.Runner.Run(ctx, cmdLaunchctl, "kickstart", "-k", launchdService(d))
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
