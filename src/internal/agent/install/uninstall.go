package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// Uninstall 停止并禁用服务，删除单元或 plist、sudoers 与 sysctl 文件（系统模式）和二进制；
// Purge 时再删除配置与数据目录。不删除 pimon 用户（hub 可能也在用），只提示手动 userdel。
func Uninstall(ctx context.Context, d Deps, o UninstallOptions) error {
	rep := sysinstall.NewReport(d.Out)
	t, err := resolveTarget(d, o.User)
	if err != nil {
		rep.Failure("检测运行环境", err)
		return fmt.Errorf("uninstall 在「检测运行环境」失败: %w", err)
	}
	u := &uninstaller{Deps: d, t: t, rep: rep}
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"停止并删除服务", u.removeService},
		{"删除文件", u.removeFiles},
	}
	if o.Purge {
		steps = append(steps, struct {
			name string
			fn   func(context.Context) error
		}{"删除配置与数据", u.purge})
	}
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			rep.Failure(s.name, err)
			return fmt.Errorf("uninstall 在「%s」失败: %w", s.name, err)
		}
	}
	w := d.Out
	p := t.paths
	if !o.Purge {
		_, _ = fmt.Fprintf(w, "配置 %s 与数据目录 %s 已保留；加 --purge 可一并删除。\n", p.Config, p.DataDir)
	}
	if t.kind == kindSystem {
		_, _ = fmt.Fprintf(w, "系统用户 %s 未删除；如不再需要（且本机没有运行 pimon-hub），可手动执行 sudo userdel %s\n", serviceUser, serviceUser)
	}
	return nil
}

type uninstaller struct {
	Deps
	t   target
	rep *sysinstall.Report
}

func (u *uninstaller) exists(path string) (bool, error) {
	_, err := u.FS.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("检查 %s: %w", path, err)
}

func (u *uninstaller) removeService(ctx context.Context) error {
	if u.t.kind == kindDarwin {
		loaded, err := launchdLoaded(ctx, u.Deps)
		if err != nil {
			return err
		}
		if loaded {
			if _, err := u.Runner.Run(ctx, cmdLaunchctl, "bootout", launchdService(u.Deps)); err != nil {
				return fmt.Errorf("卸载 LaunchAgent: %w", err)
			}
			u.rep.Done("停止并卸载 LaunchAgent "+LaunchdLabel, "")
		}
		return u.removeFile(u.t.defPath, "LaunchAgent plist")
	}
	ok, err := u.exists(u.t.defPath)
	if err != nil {
		return err
	}
	if !ok {
		u.rep.Skipped("systemd 单元", "未安装（"+u.t.defPath+" 不存在）")
		return nil
	}
	if _, err := systemctl(ctx, u.Deps, u.t, "disable", "--now", ServiceName); err != nil {
		return fmt.Errorf("停止并禁用 %s: %w", ServiceName, err)
	}
	u.rep.Done("停止并禁用 "+ServiceName, "")
	if err := u.FS.Remove(u.t.defPath); err != nil {
		return fmt.Errorf("删除 %s: %w", u.t.defPath, err)
	}
	u.rep.Done("删除 systemd 单元 "+u.t.defPath, "")
	if _, err := systemctl(ctx, u.Deps, u.t, "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	return nil
}

// removeFile 删除单个文件，不存在时跳过。
func (u *uninstaller) removeFile(path, what string) error {
	err := u.FS.Remove(path)
	switch {
	case err == nil:
		u.rep.Done("删除 "+what+" "+path, "")
	case errors.Is(err, fs.ErrNotExist):
		u.rep.Skipped(what, path+" 不存在")
	default:
		return fmt.Errorf("删除 %s: %w", path, err)
	}
	return nil
}

func (u *uninstaller) removeFiles(_ context.Context) error {
	if u.t.kind == kindSystem {
		if err := u.removeFile(sudoersPath, "sudoers 文件"); err != nil {
			return err
		}
		if err := u.removeFile(sysctlPath, "sysctl 配置"); err != nil {
			return err
		}
	}
	return u.removeFile(u.t.paths.Bin, "二进制")
}

// purge 删除配置与数据目录；路径来自固定约定或家目录，删除前再核对一次不是根目录或家目录本身。
func (u *uninstaller) purge(_ context.Context) error {
	p := u.t.paths
	dirs := []string{p.DataDir}
	if u.t.kind == kindSystem {
		dirs = []string{filepath.Dir(p.Config), p.DataDir}
	}
	for _, dir := range dirs {
		clean := filepath.Clean(dir)
		if !filepath.IsAbs(clean) || clean == "/" || clean == filepath.Clean(u.Home) || !strings.Contains(filepath.Base(clean), "pimon") {
			return fmt.Errorf("拒绝删除可疑路径 %q", dir)
		}
		if err := u.FS.RemoveAll(clean); err != nil {
			return fmt.Errorf("删除 %s: %w", clean, err)
		}
		u.rep.Done("删除目录 "+clean, "")
	}
	return nil
}
