package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

const (
	sudoersMain = "/etc/sudoers"
	sudoersDir  = "/etc/sudoers.d"
	// sudoersCheckPath 是 visudo 校验用的暂存文件：文件名含 .，sudo 的 includedir 会忽略它，
	// 即使残留也不会被读入。
	sudoersCheckPath = sudoersDir + "/.pimon-agent.check"
	cmdVisudo        = "/usr/sbin/visudo"
)

// noPermHint 说明不写 sudoers 的后果。
const noPermHint = "storage-health 的 SMART、RAID 详情与 Btrfs 条目将显示无权限"

// rootOwner 是 sudoers 文件的属主 root:root。
var rootOwner = sysinstall.Owner{UID: 0, GID: 0}

// ensureSudoers 是系统模式的 sudoers 步骤：为本机存在的 smartctl/mdadm/btrfs 写入只读命令白名单。
// 文件内容只由 sudocmd 生成；交互终端询问（回答 y/yes 才写），非交互须显式 --sudoers。
// 前提不满足（未装 sudo、没有 includedir、没有 visudo）或 visudo 校验不通过时只警告、不落地，
// 不让整个 install 失败；写文件本身出错才返回错误。
func (r *run) ensureSudoers(ctx context.Context) error {
	paths, errs := sudocmd.ResolveAll(r.SudoFS)
	for _, name := range sudocmd.Commands {
		if err := errs[name]; err != nil && !errors.Is(err, sudocmd.ErrNotFound) {
			r.rep.Warn("sudoers", fmt.Sprintf("不放行 %s：%v", name, err))
		}
	}
	if len(paths) == 0 {
		r.rep.Skipped("sudoers", "未检测到 smartctl、mdadm、btrfs，无需写入")
		return nil
	}
	content, err := sudocmd.SudoersFile(serviceUser, paths)
	if err != nil {
		return fmt.Errorf("生成 sudoers 内容: %w", err)
	}
	if reason := r.sudoersUnusable(); reason != "" {
		r.rep.Warn("sudoers", reason+"，未写入；"+noPermHint)
		return nil
	}
	if old, err := r.FS.ReadFile(sudoersPath); err == nil && string(old) == content {
		if sudoersPermOK(r.FS, sudoersPath) {
			r.rep.Existed("sudoers 白名单", sudoersPath)
			return nil
		}
		// 内容已是用户认可过的白名单，只是权限或属主不对：不再询问，直接校验后重写。
		if err := r.writeSudoers(ctx, content); err != nil {
			return err
		}
		r.rep.Done("已修正 sudoers 权限为 0440 root:root", sudoersPath)
		return nil
	}
	if !r.o.Sudoers {
		if !r.Interactive {
			r.rep.Warn("sudoers", "未写入 "+sudoersPath+"，"+noPermHint+"；非交互模式需显式加 --sudoers")
			return nil
		}
		specs, err := sudocmd.Specs(paths)
		if err != nil {
			return fmt.Errorf("生成 sudoers 规格: %w", err)
		}
		_, _ = fmt.Fprintf(r.Out, "storage-health 插件需要以 sudo -n 运行只读命令读取 SMART、RAID 详情与 Btrfs 状态。\n"+
			"将写入 %s，只允许用户 %s 以 root 执行以下命令（参数逐字匹配）：\n  %s\n",
			sudoersPath, serviceUser, strings.Join(specs, "\n  "))
		if !r.ask("是否写入？[y/N] ") {
			r.rep.Skipped("sudoers", "未写入；"+noPermHint)
			return nil
		}
	}
	return r.writeSudoers(ctx, content)
}

// sudoersPermOK 判断 sudoers 文件的权限为 0440 且属主为 root:root；取不到信息时按不符处理。
func sudoersPermOK(fsys sysinstall.FS, path string) bool {
	fi, err := fsys.Stat(path)
	return err == nil && fi.Mode == 0o440 && fi.UID == rootOwner.UID && fi.GID == rootOwner.GID
}

// sudoersUnusable 检查 sudoers.d 下的文件能否生效与校验，返回不能写入的原因；可以写入时返回空串。
func (r *run) sudoersUnusable() string {
	b, err := r.FS.ReadFile(sudoersMain)
	if err != nil {
		return "读不到 " + sudoersMain + "（可能未安装 sudo）"
	}
	if !hasIncludeDir(string(b)) {
		return sudoersMain + " 没有 @includedir " + sudoersDir + "（或 #includedir），写入也不会生效"
	}
	if fi, err := r.FS.Stat(sudoersDir); err != nil || !fi.IsDir {
		return sudoersDir + " 不是目录"
	}
	if _, err := r.FS.Stat(cmdVisudo); err != nil {
		return "没有 " + cmdVisudo + "，无法校验 sudoers 语法"
	}
	return ""
}

// hasIncludeDir 判断主 sudoers 是否引入 /etc/sudoers.d（@includedir 或旧写法 #includedir，# 后不能有空格）。
func hasIncludeDir(conf string) bool {
	for _, line := range strings.Split(conf, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && (f[0] == "@includedir" || f[0] == "#includedir") && filepath.Clean(f[1]) == sudoersDir {
			return true
		}
	}
	return false
}

// writeSudoers 先写暂存文件并用 visudo -cf 校验，通过后以 0440 root:root 原子写入目标文件，最后删除暂存。
// 校验失败时删除暂存、保留原有文件并警告。
func (r *run) writeSudoers(ctx context.Context, content string) error {
	if err := r.FS.WriteFile(sudoersCheckPath, []byte(content), 0o440, &rootOwner); err != nil {
		return fmt.Errorf("写入 sudoers 暂存文件 %s: %w", sudoersCheckPath, err)
	}
	if _, err := r.Runner.Run(ctx, cmdVisudo, "-cf", sudoersCheckPath); err != nil {
		if rmErr := r.removeSudoersCheck(); rmErr != nil {
			return rmErr
		}
		r.rep.Warn("sudoers", fmt.Sprintf("visudo 校验未通过（%v），未写入 %s；%s", err, sudoersPath, noPermHint))
		return nil
	}
	if err := r.FS.WriteFile(sudoersPath, []byte(content), 0o440, &rootOwner); err != nil {
		_ = r.removeSudoersCheck()
		return fmt.Errorf("写入 %s: %w", sudoersPath, err)
	}
	if err := r.removeSudoersCheck(); err != nil {
		// 目标已落地；暂存文件名带 .，不会被 includedir 读入，残留无害，只警告。
		r.rep.Warn("sudoers", fmt.Sprintf("%s 已写入，但%v", sudoersPath, err))
		return nil
	}
	r.rep.Done("写入 sudoers 白名单", sudoersPath)
	return nil
}

func (r *run) removeSudoersCheck() error {
	if err := r.FS.Remove(sudoersCheckPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("删除 sudoers 暂存文件 %s: %w", sudoersCheckPath, err)
	}
	return nil
}

// sudoersHint 是 Linux 用户模式的 sudoers 检查：本机有相关命令时只提示，不改系统设置。
func (r *run) sudoersHint(_ context.Context) error {
	if paths, _ := sudocmd.ResolveAll(r.SudoFS); len(paths) > 0 {
		r.rep.Warn("sudoers", "用户模式不写 sudoers："+noPermHint+"；需要时请改用系统模式安装并加 --sudoers")
	}
	return nil
}

// darwinSudoersHint 是 macOS 的 sudoers 说明：不写 sudoers，storage-health 只支持 Linux。
func (r *run) darwinSudoersHint(_ context.Context) error {
	r.rep.Skipped("sudoers", "macOS 不写 sudoers；storage-health 仅支持 Linux")
	return nil
}
