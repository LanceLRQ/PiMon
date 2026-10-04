// Package pluginscan 是 exec 插件目录的扫描、属主与权限检查、变化监视，
// 与存储和注册表无关，hub 与 agent 的插件注册表共用。
package pluginscan

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

// IssueKind 是加载问题的类别。
type IssueKind string

// 加载问题类别。
const (
	IssueInvalidManifest IssueKind = "invalid_manifest"
	IssueIDMismatch      IssueKind = "id_mismatch"
	IssueNotExec         IssueKind = "runtime_not_exec"
	IssueMissingRun      IssueKind = "missing_run"
	IssueNotExecutable   IssueKind = "not_executable"
	IssueInsecure        IssueKind = "insecure"
	IssueConflict        IssueKind = "conflict"
	// IssueDirUnreadable 表示插件根目录本身读取失败（权限、不是目录等），此时只有内置插件可用。
	IssueDirUnreadable IssueKind = "dir_unreadable"
)

// Issue 是某个插件目录未能加载的原因。
type Issue struct {
	Dir      string
	ID       string
	Kind     IssueKind
	Message  string
	Problems []manifest.Problem
}

// Exec 是扫描得到的一个可用 exec 插件。
type Exec struct {
	ID       string
	Manifest *manifest.Manifest
	Dir      string
	RunPath  string
}

// ScanDir 扫描插件目录下的每个子目录。目录不存在视为没有 exec 插件；
// 根目录读取失败降级为一条 IssueDirUnreadable 并记日志。builtinIDs 里的 id 视为冲突；
// euid 是运行进程的有效 uid，runner 是属主报错里的进程名（"hub"/"agent"）；
// log 记录目录读取失败，nil 时用 slog.Default()。返回顺序按目录名升序（os.ReadDir 的顺序）。
func ScanDir(dir string, euid int, builtinIDs map[string]bool, runner string, log *slog.Logger) ([]Exec, []Issue) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		if log == nil {
			log = slog.Default()
		}
		log.Warn("读取插件目录失败，只加载内置插件", "dir", dir, "err", err)
		return nil, []Issue{{Dir: dir, Kind: IssueDirUnreadable, Message: fmt.Sprintf("无法读取插件目录: %v", err)}}
	}
	var execs []Exec
	var issues []Issue
	for _, e := range entries {
		// 只看真实目录：不跟随符号链接，隐藏目录（如 .git）跳过。
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, is := loadExec(filepath.Join(dir, e.Name()), e.Name(), euid, builtinIDs, runner)
		if is != nil {
			issues = append(issues, *is)
			continue
		}
		execs = append(execs, *p)
	}
	return execs, issues
}

// loadExec 加载一个 exec 插件目录；失败时返回该目录的加载问题。
func loadExec(dir, name string, euid int, builtinIDs map[string]bool, runner string) (*Exec, *Issue) {
	fail := func(kind IssueKind, format string, args ...any) (*Exec, *Issue) {
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
	if msg := checkSecure(euid, runner, dir, filepath.Join(dir, manifestFile), run); msg != "" {
		return fail(IssueInsecure, "%s", msg)
	}
	return &Exec{ID: m.ID, Manifest: m, Dir: dir, RunPath: run}, nil
}

// checkSecure 检查目录、manifest 与入口：属主必须是运行进程的有效 uid 或 root，
// 且不能被组或其他用户写，防止别的用户替换可执行文件。返回空串表示通过。
func checkSecure(euid int, runner string, paths ...string) string {
	want := uint32(euid)
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
		if uid != want && uid != 0 {
			return fmt.Sprintf("%s 的属主 uid=%d 既不是 %s 运行用户 uid=%d 也不是 root", filepath.Base(p), uid, runner, want)
		}
	}
	return ""
}
