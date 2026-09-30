// Package plugins 是插件注册表：合并编译期注册的内置插件与插件目录里的 exec 插件，
// 监视目录变化并重新扫描，manifest 按（机器, 插件, 版本）存库。
package plugins

import (
	"context"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// MachineHub 是本期唯一的机器标识。
const MachineHub = "hub"

// Origin 是插件来源。
type Origin string

// 插件来源。
const (
	OriginBuiltin Origin = "builtin"
	OriginExec    Origin = "exec"
)

// Plugin 是注册表里一个可用的插件。
type Plugin struct {
	ID       string
	Origin   Origin
	Manifest *manifest.Manifest
	// Source 仅内置插件有值。
	Source runtime.Source
	// Dir 与 RunPath 仅 exec 插件有值，B5 据此构造 exec Source。
	Dir     string
	RunPath string
}

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

// Snapshot 是一次扫描后的结果。
type Snapshot struct {
	Plugins []Plugin
	Issues  []Issue
}

// Config 是注册表的依赖。
type Config struct {
	Dir      string
	DB       *store.DB
	Clock    clock.Clock
	Builtins []runtime.Source
	Debounce time.Duration
	EUID     func() int
}

// Registry 是插件注册表。Scan、Watch 与读取方法可并发使用。
type Registry struct {
	cfg Config

	scanMu sync.Mutex // 串行化扫描与落库

	mu        sync.RWMutex
	snap      Snapshot
	byID      map[string]Plugin
	listeners []func()
}

// New 创建注册表。Builtins 通常传 runtime.Builtins()；Debounce 缺省 1 秒，EUID 缺省 os.Geteuid。
func New(cfg Config) *Registry {
	if cfg.Debounce <= 0 {
		cfg.Debounce = time.Second
	}
	if cfg.EUID == nil {
		cfg.EUID = os.Geteuid
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	return &Registry{cfg: cfg, byID: map[string]Plugin{}}
}

// Snapshot 返回最近一次扫描的结果（插件按 id 升序，问题按目录名升序）。
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{
		Plugins: slices.Clone(r.snap.Plugins),
		Issues:  slices.Clone(r.snap.Issues),
	}
}

// Get 按 id 取可用插件。内置插件带 Source；exec 插件带目录与 run 路径，
// 执行由 runtime 包的 exec 执行器负责。
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

// OnChange 登记扫描完成后的回调（每次成功扫描都会调用，不论插件集合有没有变化）。
// 回调在扫描所在的 goroutine 里、释放扫描锁之后同步调用，应尽快返回；
// 回调里可以调用 Get 与 Snapshot，但不要在同一 goroutine 里调用 Scan 以外会阻塞很久的操作。
func (r *Registry) OnChange(fn func()) {
	r.mu.Lock()
	r.listeners = append(r.listeners, fn)
	r.mu.Unlock()
}

// Scan 重新扫描内置插件与插件目录，更新内存快照并把 manifest 同步入库，然后通知 OnChange 回调。
// 某个目录加载失败只记为该条目的 Issue，不影响其他插件；插件根目录读取失败同样只记一条 Issue，
// 内置插件照常注册。只有 manifest 入库失败才返回错误。
func (r *Registry) Scan(ctx context.Context) (Snapshot, error) {
	snap, err := r.scan(ctx)
	if err != nil {
		return snap, err
	}
	r.mu.RLock()
	fns := slices.Clone(r.listeners)
	r.mu.RUnlock()
	for _, fn := range fns {
		fn()
	}
	return snap, nil
}

func (r *Registry) scan(ctx context.Context) (Snapshot, error) {
	r.scanMu.Lock()
	defer r.scanMu.Unlock()

	plugins := make([]Plugin, 0, len(r.cfg.Builtins))
	builtinIDs := map[string]bool{}
	for _, s := range r.cfg.Builtins {
		m := s.Manifest()
		builtinIDs[m.ID] = true
		plugins = append(plugins, Plugin{ID: m.ID, Origin: OriginBuiltin, Manifest: m, Source: s})
	}
	execPlugins, issues := r.scanExec(builtinIDs)
	plugins = append(plugins, execPlugins...)
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	sort.Slice(issues, func(i, j int) bool { return issues[i].Dir < issues[j].Dir })

	if err := r.syncStore(ctx, plugins); err != nil {
		return Snapshot{}, err
	}
	byID := make(map[string]Plugin, len(plugins))
	for _, p := range plugins {
		byID[p.ID] = p
	}
	snap := Snapshot{Plugins: plugins, Issues: issues}
	r.mu.Lock()
	r.snap, r.byID = snap, byID
	r.mu.Unlock()
	return Snapshot{Plugins: slices.Clone(plugins), Issues: slices.Clone(issues)}, nil
}
