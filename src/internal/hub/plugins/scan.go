package plugins

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

const (
	manifestFile = "plugin.yaml"
	// runFile 是 exec 插件的固定入口。
	runFile = "run"
)

// scanExec 扫描插件目录下的每个子目录。目录不存在视为没有 exec 插件；
// 根目录读取失败降级为一条 IssueDirUnreadable 并记日志，不让中枢起不来。
func (r *Registry) scanExec(builtinIDs map[string]bool) ([]Plugin, []Issue) {
	entries, err := os.ReadDir(r.cfg.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		slog.Warn("读取插件目录失败，只加载内置插件", "dir", r.cfg.Dir, "err", err)
		return nil, []Issue{{Dir: r.cfg.Dir, Kind: IssueDirUnreadable, Message: fmt.Sprintf("无法读取插件目录: %v", err)}}
	}
	var plugins []Plugin
	var issues []Issue
	for _, e := range entries {
		// 只看真实目录：不跟随符号链接，隐藏目录（如 .git）跳过。
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(r.cfg.Dir, e.Name())
		p, is := r.loadExec(dir, e.Name(), builtinIDs)
		if is != nil {
			issues = append(issues, *is)
			continue
		}
		plugins = append(plugins, *p)
	}
	return plugins, issues
}

// loadExec 加载一个 exec 插件目录；失败时返回该目录的加载问题。
func (r *Registry) loadExec(dir, name string, builtinIDs map[string]bool) (*Plugin, *Issue) {
	fail := func(kind IssueKind, format string, args ...any) (*Plugin, *Issue) {
		return nil, &Issue{Dir: dir, ID: name, Kind: kind, Message: fmt.Sprintf(format, args...)}
	}
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return fail(IssueInvalidManifest, "无法读取 %s: %v", manifestFile, err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		var me *manifest.Error
		if errors.As(err, &me) {
			return nil, &Issue{Dir: dir, ID: name, Kind: IssueInvalidManifest, Message: me.Error(), Problems: me.Problems}
		}
		return fail(IssueInvalidManifest, "%v", err)
	}
	if m.ID != name {
		return fail(IssueIDMismatch, "目录名 %q 与 manifest 的 id %q 不一致", name, m.ID)
	}
	if m.Runtime != manifest.RuntimeExec {
		return fail(IssueNotExec, "插件目录里的 manifest 必须声明 runtime: exec，实际为 %q", m.Runtime)
	}
	if builtinIDs[m.ID] {
		return fail(IssueConflict, "id %q 与内置插件冲突，内置插件优先，该目录被忽略", m.ID)
	}
	run := filepath.Join(dir, runFile)
	info, err := os.Lstat(run)
	if errors.Is(err, fs.ErrNotExist) {
		return fail(IssueMissingRun, "缺少可执行入口 %s", runFile)
	}
	if err != nil {
		return fail(IssueMissingRun, "无法读取入口 %s: %v", runFile, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fail(IssueNotExecutable, "入口 %s 必须是带执行权限的普通文件", runFile)
	}
	if msg := r.checkSecure(dir, filepath.Join(dir, manifestFile), run); msg != "" {
		return fail(IssueInsecure, "%s", msg)
	}
	return &Plugin{ID: m.ID, Origin: OriginExec, Manifest: m, Dir: dir, RunPath: run}, nil
}

// checkSecure 检查目录、manifest 与入口：属主必须是 hub 进程的有效 uid 或 root，
// 且不能被组或其他用户写，防止别的用户替换可执行文件。返回空串表示通过。
func (r *Registry) checkSecure(paths ...string) string {
	euid := uint32(r.cfg.EUID())
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			return fmt.Sprintf("无法检查 %s: %v", filepath.Base(p), err)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Sprintf("%s 可被组或其他用户写（权限 %04o），请去掉 g+w、o+w", filepath.Base(p), info.Mode().Perm())
		}
		uid, ok := ownerUID(info)
		if !ok {
			return fmt.Sprintf("无法确定 %s 的属主", filepath.Base(p))
		}
		if uid != euid && uid != 0 {
			return fmt.Sprintf("%s 的属主 uid=%d 既不是 hub 运行用户 uid=%d 也不是 root", filepath.Base(p), uid, euid)
		}
	}
	return ""
}
