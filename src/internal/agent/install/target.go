package install

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// kind 是安装形态。
type kind int

const (
	kindSystem kind = iota // Linux systemd 系统单元，User=pimon
	kindUser               // Linux systemd 用户单元
	kindDarwin             // macOS LaunchAgent
)

// target 是一次 install/uninstall 的形态与路径。
type target struct {
	kind  kind
	paths config.Paths
	// defPath 是 systemd 单元或 launchd plist 的路径。
	defPath string
}

// resolveTarget 按模式矩阵检查运行环境并解析路径：Linux 不带 --user 要求 root 与 systemd，
// 带 --user 要求非 root 与 systemd；macOS 只有用户模式，以 root 运行拒绝。
func resolveTarget(d Deps, user bool) (target, error) {
	var t target
	switch d.GOOS {
	case "darwin":
		if d.EUID == 0 {
			return t, errors.New("macOS 上 agent 以当前用户的 LaunchAgent 运行，请不要用 sudo 或 root 执行")
		}
		t.kind = kindDarwin
	case "linux":
		switch {
		case user && d.EUID == 0:
			return t, errors.New("--user 用户模式必须以非 root 用户运行（要装成系统服务请去掉 --user）")
		case !user && d.EUID != 0:
			return t, errors.New("系统模式必须以 root 运行，请用 sudo；开发机可加 --user 安装为用户服务")
		}
		fi, err := d.FS.Stat("/run/systemd/system")
		if err != nil || !fi.IsDir {
			return t, errors.New("未检测到 systemd（/run/systemd/system 不存在），agent 在 Linux 上只支持 systemd")
		}
		t.kind = kindSystem
		if user {
			t.kind = kindUser
		}
	default:
		return t, fmt.Errorf("不支持的操作系统 %s（支持 Linux 与 macOS）", d.GOOS)
	}
	if t.kind != kindSystem && (!filepath.IsAbs(d.Home) || filepath.Clean(d.Home) == "/") {
		return t, fmt.Errorf("家目录 %q 无效（用户模式需要非根目录的绝对路径，检查 HOME 环境变量）", d.Home)
	}
	p, err := config.Resolve(config.Env{GOOS: d.GOOS, EUID: d.EUID, Home: d.Home, User: user})
	if err != nil {
		return t, err
	}
	t.paths = p
	switch t.kind {
	case kindSystem:
		t.defPath = systemUnitPath
	case kindUser:
		t.defPath = filepath.Join(d.Home, ".config", "systemd", "user", ServiceName)
	case kindDarwin:
		t.defPath = filepath.Join(d.Home, "Library", "LaunchAgents", LaunchdLabel+".plist")
	}
	return t, nil
}

// systemctl 执行 systemctl，用户模式自动加 --user。
func systemctl(ctx context.Context, d Deps, t target, args ...string) (string, error) {
	argv := []string{cmdSystemctl}
	if t.kind == kindUser {
		argv = append(argv, "--user")
	}
	return d.Runner.Run(ctx, append(argv, args...)...)
}

// launchdDomain 返回 gui/<uid>；launchdService 返回 gui/<uid>/<label>。
func launchdDomain(d Deps) string  { return "gui/" + strconv.Itoa(d.EUID) }
func launchdService(d Deps) string { return launchdDomain(d) + "/" + LaunchdLabel }

// launchdLoaded 用 launchctl print 判断 LaunchAgent 是否已加载（非零退出视为未加载）。
func launchdLoaded(ctx context.Context, d Deps) (bool, error) {
	_, err := d.Runner.Run(ctx, cmdLaunchctl, "print", launchdService(d))
	var ee *sysinstall.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee):
		return false, nil
	}
	return false, err
}

// cliHint 返回本形态下执行子命令的写法（系统模式加 sudo，Linux 用户模式加 --user）。
func (t target) cliHint(sub string) string {
	switch t.kind {
	case kindSystem:
		return "sudo pimon-agent " + sub
	case kindUser:
		return "pimon-agent " + sub + " --user"
	}
	return "pimon-agent " + sub
}

// logHint 返回查看守护进程日志的方法。
func (t target) logHint() string {
	switch t.kind {
	case kindSystem:
		return "journalctl -u pimon-agent -n 50"
	case kindUser:
		return "journalctl --user -u pimon-agent -n 50"
	}
	return "~/.pimon/agent.log"
}
