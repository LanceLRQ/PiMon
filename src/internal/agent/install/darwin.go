package install

import (
	"context"
	"errors"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// bootstrapAttempts 是 bootout 之后 bootstrap 的重试次数：bootout 返回时旧实例可能仍在退出，
// 紧接着 bootstrap 偶尔会以 EIO 失败。
const bootstrapAttempts = 5

// startLaunchd：未加载则 bootstrap；plist 变化则先 bootout 再 bootstrap；只有二进制或配置变化则发 SIGTERM，
// 让 run 优雅退出后由 KeepAlive 拉起（见 launchdRestart）。
func (r *run) startLaunchd(ctx context.Context) error {
	svc := launchdService(r.Deps)
	why := r.restartWhy()
	switch {
	case !r.activeBefore:
		if err := r.bootstrap(ctx); err != nil {
			return err
		}
		r.rep.Done("加载 LaunchAgent "+LaunchdLabel, "")
	case r.defChanged:
		if _, err := r.Runner.Run(ctx, cmdLaunchctl, "bootout", svc); err != nil {
			return fmt.Errorf("卸载旧的 LaunchAgent: %w", err)
		}
		if err := r.bootstrap(ctx); err != nil {
			return err
		}
		r.rep.Done("重新加载 LaunchAgent "+LaunchdLabel, "plist 已变化")
	case why != "":
		if err := launchdRestart(ctx, r.Deps); err != nil {
			return fmt.Errorf("重启 LaunchAgent: %w", err)
		}
		r.rep.Done("重启 LaunchAgent "+LaunchdLabel, why)
	default:
		r.rep.Existed("LaunchAgent "+LaunchdLabel, "已加载")
	}
	return nil
}

func (r *run) bootstrap(ctx context.Context) error {
	var err error
	for i := 0; i < bootstrapAttempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-r.Clock.After(pollEvery):
			}
		}
		_, err = r.Runner.Run(ctx, cmdLaunchctl, "bootstrap", launchdDomain(r.Deps), r.t.defPath)
		var ee *sysinstall.ExitError
		if err == nil || !errors.As(err, &ee) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("加载 LaunchAgent: %w", err)
	}
	return nil
}
