package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// writeDef 写 systemd 单元（系统或用户）或 launchd plist；内容相同记「已存在」。
func (r *run) writeDef(_ context.Context) error {
	var content, label string
	switch r.t.kind {
	case kindSystem:
		content, label = systemUnit, "systemd 单元 "
	case kindUser:
		u, err := userUnit(r.t.paths.Bin)
		if err != nil {
			return err
		}
		content, label = u, "systemd 用户单元 "
	case kindDarwin:
		content, label = launchdPlist(r.t.paths.Bin, r.t.paths.LogFile), "LaunchAgent plist "
	}
	path := r.t.defPath
	cur, err := r.FS.ReadFile(path)
	switch {
	case err == nil && string(cur) == content:
		r.rep.Existed(label+path, "内容相同")
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("读取 %s: %w", path, err)
	}
	if r.t.kind != kindSystem {
		if err := ensureDir(r.FS, filepath.Dir(path), sysinstall.Owner{UID: r.EUID, GID: r.EGID}); err != nil {
			return err
		}
	}
	if err := r.FS.WriteFile(path, []byte(content), 0o644, nil); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	r.defChanged = true
	r.rep.Done("写入"+label+path, "")
	return nil
}

// restartWhy 返回需要重启的原因；空表示不需要。
func (r *run) restartWhy() string {
	switch {
	case r.binChanged:
		return "二进制已升级"
	case r.staleBin:
		return "运行中的服务不是当前二进制"
	case r.confChanged:
		return "配置已变化"
	case r.groupChanged:
		return "已加入 docker 组"
	case r.defChanged:
		return "服务定义已变化"
	}
	return ""
}

func (r *run) start(ctx context.Context) error {
	if r.t.kind == kindDarwin {
		return r.startLaunchd(ctx)
	}
	return r.startSystemd(ctx)
}

// startSystemd：已启用、在运行且无变化时什么都不做；否则按需 daemon-reload、enable --now，原本在运行时再 restart。
func (r *run) startSystemd(ctx context.Context) error {
	why := r.restartWhy()
	if r.activeBefore && why == "" {
		if _, err := systemctl(ctx, r.Deps, r.t, "is-enabled", ServiceName); err == nil {
			r.rep.Existed(ServiceName, "已启用并在运行")
			return nil
		}
	}
	if r.defChanged {
		if _, err := systemctl(ctx, r.Deps, r.t, "daemon-reload"); err != nil {
			return fmt.Errorf("daemon-reload: %w", err)
		}
	}
	if _, err := systemctl(ctx, r.Deps, r.t, "enable", "--now", ServiceName); err != nil {
		return fmt.Errorf("enable --now: %w", err)
	}
	r.rep.Done("启用并启动 "+ServiceName, "")
	// 服务原本没在运行时 enable --now 已经用新二进制、新配置启动，无需再重启。
	if r.activeBefore && why != "" {
		if _, err := systemctl(ctx, r.Deps, r.t, "restart", ServiceName); err != nil {
			return fmt.Errorf("restart: %w", err)
		}
		r.rep.Done("重启 "+ServiceName, why)
	}
	return nil
}
