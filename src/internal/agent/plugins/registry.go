// Package plugins 是 agent 侧的插件注册表：合并调用方注入的内置插件（只保留 runs_on 含 agent 的数据源）
// 与数据目录 plugins/ 下的 exec 插件，监视目录变化并重新扫描，向 hub 上报可用插件的 manifest。
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/pluginscan"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 插件来源，与协议 PluginManifest.Origin 的取值一致。
const (
	OriginBuiltin = "builtin"
	OriginExec    = "exec"
)

// Issue 是某个插件目录未能加载的原因。
type Issue = pluginscan.Issue

// IssueNotForAgent 表示 exec 插件不能在 agent 上运行（runs_on 不含 agent，或是通知渠道插件），不上报。
const IssueNotForAgent pluginscan.IssueKind = "not_for_agent"

// Plugin 是注册表里一个可用的插件。Source 总是有值：exec 插件在扫描时就构造好 exec 执行器。
type Plugin struct {
	ID       string
	Origin   string // OriginBuiltin 或 OriginExec
	Manifest *manifest.Manifest
	Source   runtime.Source
	// Dir 与 RunPath 仅 exec 插件有值。
	Dir     string
	RunPath string
}

// Snapshot 是一次扫描后的结果。
type Snapshot struct {
	Plugins []Plugin
	Issues  []Issue
}

// Config 是注册表的依赖。Builtins 由调用方注入（入口传 runtime.Builtins()），注册表不读全局注册表；
// Debounce 缺省 1 秒，Clock 缺省真实时钟，EUID 缺省 os.Geteuid，Log 缺省 slog.Default()。
type Config struct {
	Dir      string
	Builtins []runtime.Source
	Clock    clock.Clock
	Debounce time.Duration
	EUID     func() int
	Log      *slog.Logger
}

// Registry 是 agent 插件注册表。Scan、Watch 与读取方法可并发使用。
type Registry struct {
	cfg Config

	scanMu sync.Mutex // 串行化扫描

	mu        sync.RWMutex
	snap      Snapshot
	byID      map[string]Plugin
	listeners []func()
}

// New 创建注册表；需调用 Scan 完成首次扫描。
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
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Registry{cfg: cfg, byID: map[string]Plugin{}}
}

// Snapshot 返回最近一次扫描的结果（插件按 id 升序，问题按目录升序）。
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{Plugins: slices.Clone(r.snap.Plugins), Issues: slices.Clone(r.snap.Issues)}
}

// Get 按 id 取可用插件。
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

// Manifests 返回全部可用插件的 manifest（按 id 升序），供 hello 与 plugins_changed 使用。
// 内层是 json.Marshal(*manifest.Manifest) 的输出，与 hub 的 manifest.FromJSON 往返一致。
func (r *Registry) Manifests() ([]agent.PluginManifest, error) {
	snap := r.Snapshot()
	out := make([]agent.PluginManifest, 0, len(snap.Plugins))
	for _, p := range snap.Plugins {
		raw, err := json.Marshal(p.Manifest)
		if err != nil {
			return nil, fmt.Errorf("序列化插件 %s 的 manifest: %w", p.ID, err)
		}
		out = append(out, agent.PluginManifest{Origin: p.Origin, Manifest: raw})
	}
	return out, nil
}

// OnChange 登记扫描完成后的回调（每次扫描都会调用，不论插件集合有没有变化），只做通知。
// 回调在扫描所在的 goroutine 里、释放扫描锁之后同步调用，应尽快返回；回调里可以调用 Get、Snapshot 与 Manifests。
func (r *Registry) OnChange(fn func()) {
	r.mu.Lock()
	r.listeners = append(r.listeners, fn)
	r.mu.Unlock()
}

// Scan 重新扫描内置插件与插件目录，更新内存快照，然后通知 OnChange 回调。
// 某个目录加载失败只记为该条目的 Issue（并写 Warn 日志），不影响其他插件。
func (r *Registry) Scan() Snapshot {
	snap := r.scan()
	r.mu.RLock()
	fns := slices.Clone(r.listeners)
	r.mu.RUnlock()
	for _, fn := range fns {
		fn()
	}
	return snap
}

func (r *Registry) scan() Snapshot {
	r.scanMu.Lock()
	defer r.scanMu.Unlock()

	// 所有注入的内置插件 id（含 hub 专用的）都保留，同名 exec 插件按冲突处理，不能冒充。
	builtinIDs := map[string]bool{}
	var plugins []Plugin
	for _, s := range r.cfg.Builtins {
		m := s.Manifest()
		builtinIDs[m.ID] = true
		if forAgent(m) {
			plugins = append(plugins, Plugin{ID: m.ID, Origin: OriginBuiltin, Manifest: m, Source: s})
		}
	}
	execs, issues := pluginscan.ScanDir(r.cfg.Dir, r.cfg.EUID(), builtinIDs, "agent", r.cfg.Log)
	for _, e := range execs {
		if !forAgent(e.Manifest) {
			issues = append(issues, Issue{
				Dir: e.Dir, ID: e.ID, Kind: IssueNotForAgent,
				Message: "插件不能在 agent 上运行（runs_on 不含 agent，或是通知渠道插件），不上报",
			})
			continue
		}
		plugins = append(plugins, Plugin{
			ID: e.ID, Origin: OriginExec, Manifest: e.Manifest,
			Source: runtime.NewExecSource(e.Manifest, e.RunPath), Dir: e.Dir, RunPath: e.RunPath,
		})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	sort.Slice(issues, func(i, j int) bool { return issues[i].Dir < issues[j].Dir })
	for _, is := range issues {
		r.cfg.Log.Warn("插件未加载", "dir", is.Dir, "id", is.ID, "kind", string(is.Kind), "reason", is.Message)
	}

	byID := make(map[string]Plugin, len(plugins))
	for _, p := range plugins {
		byID[p.ID] = p
	}
	r.mu.Lock()
	r.snap, r.byID = Snapshot{Plugins: plugins, Issues: issues}, byID
	r.mu.Unlock()
	return Snapshot{Plugins: slices.Clone(plugins), Issues: slices.Clone(issues)}
}

// forAgent 判断插件能否在 agent 上运行：数据源插件且 runs_on 含 agent。
func forAgent(m *manifest.Manifest) bool {
	return m.Kind == manifest.KindSource && slices.Contains(m.RunsOn, manifest.RunsOnAgent)
}

// Watch 监视插件目录，变化后防抖 Debounce 再重新扫描（随后通知 OnChange 回调）。
// 插件目录不存在时先创建（0750）。返回的 channel 在监视停止（ctx 结束）后关闭；
// ctx 结束后不再触发重扫与回调（已在进行中的一次会执行完，channel 在其返回后才关闭）。
func (r *Registry) Watch(ctx context.Context) (<-chan struct{}, error) {
	if err := os.MkdirAll(r.cfg.Dir, 0o750); err != nil {
		return nil, err
	}
	return pluginscan.Watch(ctx, pluginscan.WatchConfig{
		Dir: r.cfg.Dir, Clock: r.cfg.Clock, Debounce: r.cfg.Debounce, Log: r.cfg.Log,
	}, func() {
		if ctx.Err() != nil {
			return
		}
		r.Scan()
	})
}
