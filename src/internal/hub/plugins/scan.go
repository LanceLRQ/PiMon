package plugins

import (
	"log/slog"

	"github.com/LanceLRQ/PiMon/src/internal/pluginscan"
)

// 加载问题类型与常量由 pluginscan 定义，这里保留原名供 API 与系统自检引用。
type (
	// IssueKind 是加载问题的类别。
	IssueKind = pluginscan.IssueKind
	// Issue 是某个插件目录未能加载的原因。
	Issue = pluginscan.Issue
)

// 加载问题类别。
const (
	IssueInvalidManifest = pluginscan.IssueInvalidManifest
	IssueIDMismatch      = pluginscan.IssueIDMismatch
	IssueNotExec         = pluginscan.IssueNotExec
	IssueMissingRun      = pluginscan.IssueMissingRun
	IssueNotExecutable   = pluginscan.IssueNotExecutable
	IssueInsecure        = pluginscan.IssueInsecure
	IssueConflict        = pluginscan.IssueConflict
	IssueDirUnreadable   = pluginscan.IssueDirUnreadable
)

// scanExec 调用共用扫描并把结果转成注册表的 Plugin。
func (r *Registry) scanExec(builtinIDs map[string]bool) ([]Plugin, []Issue) {
	execs, issues := pluginscan.ScanDir(r.cfg.Dir, r.cfg.EUID(), builtinIDs, "hub", slog.Default())
	var plugins []Plugin
	for _, e := range execs {
		plugins = append(plugins, Plugin{ID: e.ID, Origin: OriginExec, Manifest: e.Manifest, Dir: e.Dir, RunPath: e.RunPath})
	}
	return plugins, issues
}
