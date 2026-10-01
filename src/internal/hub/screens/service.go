package screens

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// MaxVersions 是保留的布局版本数。
const MaxVersions = 20

// PluginSource 是校验与解析依赖的插件注册表；生产环境为 *plugins.Registry。
type PluginSource interface {
	Get(id string) (plugins.Plugin, bool)
}

// InstanceSource 是校验与解析依赖的实例列表；生产环境为 *instances.Service。
type InstanceSource interface {
	List(ctx context.Context) ([]model.Instance, error)
}

// Config 是 Service 的依赖。
type Config struct {
	DB        *store.DB
	Clock     clock.Clock
	Plugins   PluginSource
	Instances InstanceSource
}

// SaveOptions 是保存时的附加信息。
type SaveOptions struct {
	// Source 是版本来源，缺省为 edit。
	Source string
	// Note 写入版本摘要，如「按显示器自动选择网格」。
	Note string
}

// Service 是布局服务。
type Service struct {
	db   *store.DB
	clk  clock.Clock
	reg  PluginSource
	inst InstanceSource

	// mu 串行化写入（保存、回滚）。
	mu sync.Mutex

	// seeds 按网格取种子布局，供首次 viewport 自动选网格使用；为空则不自动选。
	seeds SeedFunc

	cbMu     sync.RWMutex
	onChange func(model.LayoutState)
}

// New 创建布局服务。
func New(c Config) *Service {
	s := &Service{db: c.DB, clk: c.Clock, reg: c.Plugins, inst: c.Instances}
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	if s.reg == nil {
		s.reg = noPlugins{}
	}
	if s.inst == nil {
		s.inst = noInstances{}
	}
	return s
}

type noPlugins struct{}

func (noPlugins) Get(string) (plugins.Plugin, bool) { return plugins.Plugin{}, false }

type noInstances struct{}

func (noInstances) List(context.Context) ([]model.Instance, error) { return nil, nil }

// OnChange 注册布局变化的订阅回调：保存或回滚成功之后同步调用（不持锁），参数是新版本。
// 回调必须非阻塞；重复注册会覆盖前一个。
func (s *Service) OnChange(f func(model.LayoutState)) {
	s.cbMu.Lock()
	s.onChange = f
	s.cbMu.Unlock()
}

func (s *Service) notify(st model.LayoutState) {
	s.cbMu.RLock()
	f := s.onChange
	s.cbMu.RUnlock()
	if f != nil {
		f(st)
	}
}

// EmptyLayout 是尚无任何版本时的布局：8×5 网格，只有一个空的首页。
func EmptyLayout() model.Layout {
	return model.Layout{
		Grid: model.Grid{Cols: 8, Rows: 5},
		Screens: []model.LayoutScreen{
			{ID: model.IndexScreenID, Name: "首页", InRotation: true, Widgets: []model.LayoutWidget{}},
		},
	}
}

type versionRow struct {
	version   int
	layout    string
	summary   string
	source    string
	createdAt time.Time
}

const versionCols = `version, layout_json, summary, source, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanVersion(sc rowScanner) (versionRow, error) {
	var (
		r       versionRow
		created string
	)
	if err := sc.Scan(&r.version, &r.layout, &r.summary, &r.source, &created); err != nil {
		return versionRow{}, err
	}
	t, err := store.ParseTime(created)
	if err != nil {
		return versionRow{}, err
	}
	r.createdAt = t
	return r, nil
}

// latestRow 读最大版本；没有任何版本时 ok 为 false。
func (s *Service) latestRow(ctx context.Context) (r versionRow, ok bool, err error) {
	r, err = scanVersion(s.db.QueryRowContext(ctx, `SELECT `+versionCols+` FROM layout_versions ORDER BY version DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return versionRow{}, false, nil
	}
	return r, err == nil, err
}

func (s *Service) rowAt(ctx context.Context, v int) (versionRow, error) {
	r, err := scanVersion(s.db.QueryRowContext(ctx, `SELECT `+versionCols+` FROM layout_versions WHERE version = ?`, v))
	if errors.Is(err, sql.ErrNoRows) {
		return versionRow{}, ErrVersionNotFound
	}
	return r, err
}

func (s *Service) snapshot(ctx context.Context) (*snapshot, error) {
	list, err := s.inst.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取实例列表: %w", err)
	}
	return newSnapshot(list), nil
}

func decodeLayout(raw string) (model.Layout, error) {
	var l model.Layout
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return model.Layout{}, fmt.Errorf("解析布局: %w", err)
	}
	normalize(&l)
	return l, nil
}

// state 把一行版本转成带失效引用的布局状态。
func (s *Service) state(r versionRow, sn *snapshot) (model.LayoutState, error) {
	l, err := decodeLayout(r.layout)
	if err != nil {
		return model.LayoutState{}, err
	}
	return model.LayoutState{
		Version: r.version, Source: r.source, CreatedAt: r.createdAt, Layout: l,
		Broken: s.checkStored(&l, sn),
	}, nil
}

// Current 返回当前布局（最大版本）。尚无版本时返回版本 0 与空布局。
func (s *Service) Current(ctx context.Context) (model.LayoutState, error) {
	r, ok, err := s.latestRow(ctx)
	if err != nil {
		return model.LayoutState{}, err
	}
	sn, err := s.snapshot(ctx)
	if err != nil {
		return model.LayoutState{}, err
	}
	if !ok {
		return model.LayoutState{Layout: EmptyLayout(), Broken: []model.LayoutProblem{}}, nil
	}
	return s.state(r, sn)
}

// Version 返回指定版本的完整布局；版本不存在（含已被淘汰）返回 ErrVersionNotFound。
func (s *Service) Version(ctx context.Context, v int) (model.LayoutState, error) {
	r, err := s.rowAt(ctx, v)
	if err != nil {
		return model.LayoutState{}, err
	}
	sn, err := s.snapshot(ctx)
	if err != nil {
		return model.LayoutState{}, err
	}
	return s.state(r, sn)
}

// Versions 按版本号从新到旧返回版本列表。
func (s *Service) Versions(ctx context.Context) ([]model.LayoutVersionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+versionCols+` FROM layout_versions ORDER BY version DESC`)
	if err != nil {
		return nil, err
	}
	var rs []versionRow
	for rows.Next() {
		r, err := scanVersion(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	sn, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.LayoutVersionInfo, 0, len(rs))
	for _, r := range rs {
		info := model.LayoutVersionInfo{Version: r.version, Source: r.source, CreatedAt: r.createdAt}
		if err := json.Unmarshal([]byte(r.summary), &info.Summary); err != nil {
			return nil, fmt.Errorf("解析版本摘要: %w", err)
		}
		if info.Summary.ChangedScreens == nil {
			info.Summary.ChangedScreens = []string{}
		}
		l, err := decodeLayout(r.layout)
		if err != nil {
			return nil, err
		}
		info.HasBroken = len(s.checkStored(&l, sn)) > 0
		out = append(out, info)
	}
	return out, nil
}

func validSource(src string) bool {
	switch src {
	case model.LayoutSourceSeed, model.LayoutSourceEdit, model.LayoutSourceRollback, model.LayoutSourceAuto:
		return true
	}
	return false
}

// Save 保存整份布局为新版本。baseVersion 必须等于当前版本（尚无版本时为 0），
// 否则返回 *ConflictError；布局有几何、尺寸或结构问题返回 *InvalidError，不产生版本。
// 引用失效不拒绝保存，体现在返回状态的 Broken 里。
func (s *Service) Save(ctx context.Context, baseVersion int, l model.Layout, opt SaveOptions) (model.LayoutState, error) {
	if opt.Source == "" {
		opt.Source = model.LayoutSourceEdit
	}
	if !validSource(opt.Source) {
		return model.LayoutState{}, fmt.Errorf("未知的布局版本来源 %q", opt.Source)
	}
	cp, err := cloneLayout(l)
	if err != nil {
		return model.LayoutState{}, err
	}
	sn, err := s.snapshot(ctx)
	if err != nil {
		return model.LayoutState{}, err
	}
	problems, broken := s.check(&cp, sn)
	if len(problems) > 0 {
		return model.LayoutState{}, &InvalidError{Problems: problems}
	}
	st, err := s.commit(ctx, &baseVersion, cp, opt.Source, model.LayoutSummary{Note: opt.Note})
	if err != nil {
		return model.LayoutState{}, err
	}
	st.Broken = broken
	s.notify(st)
	return st, nil
}

// Rollback 生成一个内容等于旧版本的新版本，不删除中间的版本。
// 旧版本不存在返回 ErrVersionNotFound；旧布局按当前插件已不合法时返回 *InvalidError。
func (s *Service) Rollback(ctx context.Context, version int) (model.LayoutState, error) {
	r, err := s.rowAt(ctx, version)
	if err != nil {
		return model.LayoutState{}, err
	}
	l, err := decodeLayout(r.layout)
	if err != nil {
		return model.LayoutState{}, err
	}
	sn, err := s.snapshot(ctx)
	if err != nil {
		return model.LayoutState{}, err
	}
	problems, broken := s.check(&l, sn)
	if len(problems) > 0 {
		return model.LayoutState{}, &InvalidError{Problems: problems}
	}
	st, err := s.commit(ctx, nil, l, model.LayoutSourceRollback, model.LayoutSummary{RolledBackFrom: version})
	if err != nil {
		return model.LayoutState{}, err
	}
	st.Broken = broken
	s.notify(st)
	return st, nil
}

// commit 在事务里写入新版本并淘汰最旧的；base 非 nil 时先做乐观锁检查。
// sum 里只需带 Note 或 RolledBackFrom，其余字段由相对上一版的差异计算。
func (s *Service) commit(ctx context.Context, base *int, l model.Layout, source string, sum model.LayoutSummary) (model.LayoutState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.LayoutState{}, err
	}
	defer func() { _ = tx.Rollback() }()

	latest := 0
	var prev *model.Layout
	var raw string
	switch err := tx.QueryRowContext(ctx, `SELECT version, layout_json FROM layout_versions ORDER BY version DESC LIMIT 1`).Scan(&latest, &raw); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return model.LayoutState{}, err
	default:
		p, err := decodeLayout(raw)
		if err != nil {
			return model.LayoutState{}, err
		}
		prev = &p
	}
	if base != nil && *base != latest {
		return model.LayoutState{}, &ConflictError{Latest: latest}
	}

	d := diff(prev, l)
	d.Note, d.RolledBackFrom = sum.Note, sum.RolledBackFrom
	sumJSON, err := json.Marshal(d)
	if err != nil {
		return model.LayoutState{}, err
	}
	layoutJSON, err := json.Marshal(l)
	if err != nil {
		return model.LayoutState{}, err
	}
	now := s.clk.Now()
	next := latest + 1
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO layout_versions (version, layout_json, summary, source, created_at) VALUES (?, ?, ?, ?, ?)`,
		next, string(layoutJSON), string(sumJSON), source, store.FormatTime(now)); err != nil {
		return model.LayoutState{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM layout_versions WHERE version <= ?`, next-MaxVersions); err != nil {
		return model.LayoutState{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.LayoutState{}, err
	}
	return model.LayoutState{Version: next, Source: source, CreatedAt: now.UTC(), Layout: l, Broken: []model.LayoutProblem{}}, nil
}

// ScreensUsing 返回当前布局里引用该实例的 screen（按布局顺序、去重）；
// 占位小组件解析出的绑定不算引用。实现 instances.ScreenReferrers。
func (s *Service) ScreensUsing(ctx context.Context, instanceID string) ([]model.ScreenRef, error) {
	out := []model.ScreenRef{}
	r, ok, err := s.latestRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	l, err := decodeLayout(r.layout)
	if err != nil {
		return nil, err
	}
	for _, sc := range l.Screens {
		if screenUses(sc, instanceID) {
			out = append(out, model.ScreenRef{ID: sc.ID, Name: sc.Name})
		}
	}
	return out, nil
}

func screenUses(sc model.LayoutScreen, instanceID string) bool {
	for _, w := range sc.Widgets {
		if w.Binding.InstanceID == instanceID {
			return true
		}
		for _, r := range w.Binding.Refs {
			if r.InstanceID == instanceID {
				return true
			}
		}
	}
	return false
}
