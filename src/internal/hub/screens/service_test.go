package screens

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

const demoManifest = `id: demo
version: 1.0.0
api_version: 1
name: {zh: 演示, en: Demo}
kind: source
runtime: builtin
runs_on: [hub]
interval: 60s
timeout: 10s
outputs:
  - {key: temp, type: number, title: {zh: 温度, en: Temperature}}
  - {key: load, type: gauge, title: {zh: 负载, en: Load}}
  - {key: "target[*]", type: gauge, title: {zh: 目标, en: Targets}}
widgets:
  - id: temp
    name: {zh: 温度, en: Temperature}
    sizes:
      1x1: {template: value, bind: {value: {item: temp}}}
      2x1: {template: value, bind: {value: {item: temp}}}
  - id: load
    name: {zh: 负载, en: Load}
    sizes:
      2x2: {template: gauge, bind: {value: {item: load}}}
`

type fakePlugins map[string]plugins.Plugin

func (f fakePlugins) Get(id string) (plugins.Plugin, bool) { p, ok := f[id]; return p, ok }

type fakeInstances struct{ list []model.Instance }

func (f *fakeInstances) List(context.Context) ([]model.Instance, error) {
	return append([]model.Instance(nil), f.list...), nil
}

func (f *fakeInstances) remove(id string) {
	out := f.list[:0]
	for _, i := range f.list {
		if i.ID != id {
			out = append(out, i)
		}
	}
	f.list = out
}

type fixture struct {
	svc  *Service
	clk  *clock.Fake
	inst *fakeInstances
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse([]byte(demoManifest))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	inst := &fakeInstances{list: []model.Instance{
		{ID: "i-late", PluginID: "demo", Name: "后建", DisplayState: "warning", CreatedAt: base.Add(2 * time.Hour)},
		{ID: "i-early", PluginID: "demo", Name: "先建", DisplayState: "ok", CreatedAt: base},
		{ID: "i-other", PluginID: "other", Name: "别的插件", DisplayState: "ok", CreatedAt: base},
	}}
	svc := New(Config{
		DB: db, Clock: clk, Instances: inst,
		Plugins: fakePlugins{"demo": {ID: "demo", Manifest: m}},
	})
	return &fixture{svc: svc, clk: clk, inst: inst}
}

func pw(id, widgetID string, w, h, col, row int, instance string) model.LayoutWidget {
	return model.LayoutWidget{
		ID: id, Source: model.WidgetSourcePlugin, PluginID: "demo", WidgetID: widgetID,
		Size: model.WidgetSize{Cols: w, Rows: h}, Col: col, Row: row,
		Binding: model.WidgetBinding{InstanceID: instance},
	}
}

func layoutWith(ws ...model.LayoutWidget) model.Layout {
	return model.Layout{
		Grid: model.Grid{Cols: 6, Rows: 4},
		Screens: []model.LayoutScreen{
			{ID: "index", Name: "首页", InRotation: true, Widgets: ws},
		},
	}
}

func mustSave(t *testing.T, f *fixture, base int, l model.Layout) model.LayoutState {
	t.Helper()
	st, err := f.svc.Save(context.Background(), base, l, SaveOptions{Source: model.LayoutSourceEdit})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return st
}

func problemsOf(t *testing.T, err error) []model.LayoutProblem {
	t.Helper()
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("期望 *InvalidError，得到 %v", err)
	}
	return inv.Problems
}

func hasProblem(ps []model.LayoutProblem, widget, code string) bool {
	for _, p := range ps {
		if p.Widget == widget && p.Code == code {
			return true
		}
	}
	return false
}

func TestCurrent尚无版本时返回只有首页的空布局(t *testing.T) {
	f := newFixture(t)
	st, err := f.svc.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 0 || len(st.Layout.Screens) != 1 || st.Layout.Screens[0].ID != model.IndexScreenID {
		t.Fatalf("空布局 = %+v", st)
	}
	if st.Layout.Screens[0].Widgets == nil || st.Broken == nil {
		t.Fatal("切片字段应为空数组而不是 null")
	}
}

func TestSave生成版本与摘要(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v1 := mustSave(t, f, 0, layoutWith(pw("w1", "temp", 1, 1, 0, 0, "i-early")))
	if v1.Version != 1 || v1.Source != model.LayoutSourceEdit || !v1.CreatedAt.Equal(f.clk.Now()) {
		t.Fatalf("v1 = %+v", v1)
	}
	cur, _ := f.svc.Current(ctx)
	if cur.Version != 1 || len(cur.Layout.Screens[0].Widgets) != 1 {
		t.Fatalf("Current = %+v", cur)
	}

	l2 := layoutWith(pw("w1", "temp", 2, 1, 0, 0, "i-early"), pw("w2", "load", 2, 2, 0, 1, "i-early"))
	l2.Screens = append(l2.Screens, model.LayoutScreen{ID: "s2", Name: "第二屏", Widgets: []model.LayoutWidget{}})
	v2 := mustSave(t, f, 1, l2)
	if v2.Version != 2 {
		t.Fatalf("v2 = %d", v2.Version)
	}
	vs, err := f.svc.Versions(ctx)
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 {
		t.Fatalf("Versions = %+v %v", vs, err)
	}
	s := vs[0].Summary
	if s.WidgetsAdded != 1 || s.WidgetsChanged != 1 || s.WidgetsRemoved != 0 || s.GridChanged ||
		len(s.ChangedScreens) != 2 || s.ChangedScreens[0] != "index" || s.ChangedScreens[1] != "s2" {
		t.Fatalf("v2 摘要 = %+v", s)
	}
	first := vs[1].Summary
	if first.WidgetsAdded != 1 || !first.GridChanged || len(first.ChangedScreens) != 1 {
		t.Fatalf("v1 摘要 = %+v", first)
	}

	// 网格变化与删除
	l3 := l2
	l3.Grid = model.Grid{Cols: 8, Rows: 5}
	l3.Screens = []model.LayoutScreen{l2.Screens[0]}
	v3 := mustSave(t, f, 2, l3)
	_ = v3
	vs, _ = f.svc.Versions(ctx)
	s3 := vs[0].Summary
	if !s3.GridChanged || s3.WidgetsAdded != 0 || len(s3.ChangedScreens) != 1 || s3.ChangedScreens[0] != "s2" {
		t.Fatalf("v3 摘要 = %+v", s3)
	}
}

func TestSave版本冲突(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustSave(t, f, 0, layoutWith())
	mustSave(t, f, 1, layoutWith())
	_, err := f.svc.Save(ctx, 1, layoutWith(), SaveOptions{Source: model.LayoutSourceEdit})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Latest != 2 {
		t.Fatalf("期望版本冲突 latest=2，得到 %v", err)
	}
	// 冲突不产生版本
	cur, _ := f.svc.Current(ctx)
	if cur.Version != 2 {
		t.Fatalf("version = %d", cur.Version)
	}
	// 尚无版本时 base 不为 0 也冲突
	g := newFixture(t)
	_, err = g.svc.Save(ctx, 3, layoutWith(), SaveOptions{Source: model.LayoutSourceEdit})
	if !errors.As(err, &ce) || ce.Latest != 0 {
		t.Fatalf("空库 base=3 应冲突: %v", err)
	}
}

func TestSave校验碰撞越界与尺寸白名单(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	save := func(l model.Layout) []model.LayoutProblem {
		_, err := f.svc.Save(ctx, 0, l, SaveOptions{Source: model.LayoutSourceEdit})
		return problemsOf(t, err)
	}

	ps := save(layoutWith(pw("a", "temp", 1, 1, 0, 0, ""), pw("b", "temp", 1, 1, 0, 0, "")))
	if !hasProblem(ps, "b", model.LayoutProblemOverlap) || ps[0].Screen != "index" {
		t.Fatalf("重叠 = %+v", ps)
	}
	ps = save(layoutWith(pw("a", "temp", 2, 1, 5, 0, "")))
	if !hasProblem(ps, "a", model.LayoutProblemOutOfBounds) {
		t.Fatalf("越界 = %+v", ps)
	}
	// 插件声明了 1x1 与 2x1，没有 3x1
	ps = save(layoutWith(pw("a", "temp", 3, 1, 0, 0, "")))
	if !hasProblem(ps, "a", model.LayoutProblemSizeNotAllowed) {
		t.Fatalf("尺寸白名单 = %+v", ps)
	}

	// 通用小组件按目录限制尺寸
	gen := model.LayoutWidget{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "state",
		Size:    model.WidgetSize{Cols: 4, Rows: 2},
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: "i-early", Item: "temp"}}},
	}
	if ps = save(layoutWith(gen)); !hasProblem(ps, "g", model.LayoutProblemSizeNotAllowed) {
		t.Fatalf("通用尺寸 = %+v", ps)
	}
	gen.Template = "no-such"
	if ps = save(layoutWith(gen)); !hasProblem(ps, "g", model.LayoutProblemInvalid) {
		t.Fatalf("未知模板 = %+v", ps)
	}
	// 网格比小组件小
	l := layoutWith(pw("a", "load", 2, 2, 0, 0, ""))
	l.Grid = model.Grid{Cols: 1, Rows: 1}
	if ps = save(l); !hasProblem(ps, "a", model.LayoutProblemOutOfBounds) {
		t.Fatalf("小网格 = %+v", ps)
	}
	cur, _ := f.svc.Current(ctx)
	if cur.Version != 0 {
		t.Fatalf("被拒绝的保存不应产生版本: %d", cur.Version)
	}
}

func TestSave结构校验(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	check := func(name string, l model.Layout) {
		t.Helper()
		_, err := f.svc.Save(ctx, 0, l, SaveOptions{Source: model.LayoutSourceEdit})
		ps := problemsOf(t, err)
		for _, p := range ps {
			if p.Code == model.LayoutProblemInvalid {
				return
			}
		}
		t.Fatalf("%s: 期望 invalid，得到 %+v", name, ps)
	}
	noIndex := layoutWith()
	noIndex.Screens[0].ID = "other"
	check("缺少 index", noIndex)

	dupWidget := layoutWith(pw("a", "temp", 1, 1, 0, 0, ""), pw("a", "temp", 1, 1, 1, 0, ""))
	check("widget id 重复", dupWidget)

	dupScreen := layoutWith()
	dupScreen.Screens = append(dupScreen.Screens, model.LayoutScreen{ID: "index", Name: "x"})
	check("screen id 重复", dupScreen)

	badGrid := layoutWith()
	badGrid.Grid = model.Grid{Cols: 0, Rows: 4}
	check("网格为 0", badGrid)

	noName := layoutWith()
	noName.Screens[0].Name = " "
	check("名称为空", noName)

	badDwell := layoutWith()
	badDwell.Screens[0].DwellSeconds = 1
	check("停留过短", badDwell)

	badSource := layoutWith(model.LayoutWidget{ID: "a", Source: "weird", Size: model.WidgetSize{Cols: 1, Rows: 1}})
	check("未知来源", badSource)

	badIcon := pw("a", "temp", 1, 1, 0, 0, "")
	badIcon.Options = map[string]any{"icon": "Not Valid!"}
	check("图标名", layoutWith(badIcon))

	mixed := pw("a", "temp", 1, 1, 0, 0, "")
	mixed.Binding.Refs = []model.WidgetRef{{InstanceID: "i-early", Item: "temp"}}
	check("plugin 来源不应带 refs", layoutWith(mixed))

	noPlugin := pw("a", "temp", 1, 1, 0, 0, "")
	noPlugin.PluginID = ""
	check("plugin 来源缺 plugin_id", layoutWith(noPlugin))
}

func TestSave超过二十个版本淘汰最旧(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 23; i++ {
		mustSave(t, f, i, layoutWith())
	}
	vs, err := f.svc.Versions(ctx)
	if err != nil || len(vs) != MaxVersions {
		t.Fatalf("版本数 = %d %v", len(vs), err)
	}
	if vs[0].Version != 23 || vs[len(vs)-1].Version != 4 {
		t.Fatalf("保留范围 = %d..%d", vs[len(vs)-1].Version, vs[0].Version)
	}
	if _, err := f.svc.Version(ctx, 3); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("已淘汰的版本应不存在: %v", err)
	}
}

func TestRollback生成新版本(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	calls := []model.LayoutState{}
	f.svc.OnChange(func(s model.LayoutState) { calls = append(calls, s) })

	mustSave(t, f, 0, layoutWith(pw("w1", "temp", 1, 1, 0, 0, "i-early")))
	l2 := layoutWith(pw("w1", "temp", 1, 1, 0, 0, "i-early"), pw("w2", "temp", 1, 1, 1, 0, "i-early"))
	l2.Grid = model.Grid{Cols: 8, Rows: 5}
	mustSave(t, f, 1, l2)

	st, err := f.svc.Rollback(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 3 || st.Source != model.LayoutSourceRollback ||
		len(st.Layout.Screens[0].Widgets) != 1 || st.Layout.Grid.Cols != 6 {
		t.Fatalf("回滚结果 = %+v", st)
	}
	vs, _ := f.svc.Versions(ctx)
	if len(vs) != 3 || vs[0].Summary.RolledBackFrom != 1 || !vs[0].Summary.GridChanged || vs[0].Summary.WidgetsRemoved != 1 {
		t.Fatalf("摘要 = %+v", vs[0].Summary)
	}
	if len(calls) != 3 || calls[2].Version != 3 {
		t.Fatalf("OnChange 调用 = %d", len(calls))
	}
	if _, err := f.svc.Rollback(ctx, 99); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("不存在的版本: %v", err)
	}
	if len(calls) != 3 {
		t.Fatal("失败的回滚不应触发回调")
	}
}

func TestOnChange仅在成功后调用(t *testing.T) {
	f := newFixture(t)
	n := 0
	f.svc.OnChange(func(model.LayoutState) { n++ })
	if _, err := f.svc.Save(context.Background(), 0, layoutWith(pw("a", "temp", 9, 9, 0, 0, "")), SaveOptions{Source: model.LayoutSourceEdit}); err == nil {
		t.Fatal("应被拒绝")
	}
	if _, err := f.svc.Save(context.Background(), 5, layoutWith(), SaveOptions{Source: model.LayoutSourceEdit}); err == nil {
		t.Fatal("应冲突")
	}
	if n != 0 {
		t.Fatalf("回调 %d 次", n)
	}
	mustSave(t, f, 0, layoutWith())
	if n != 1 {
		t.Fatalf("回调 %d 次", n)
	}
}

func TestSave实例缺失标为引用失效而不拒绝(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	st := mustSave(t, f, 0, layoutWith(
		pw("ok", "temp", 1, 1, 0, 0, "i-early"),
		pw("gone", "temp", 1, 1, 1, 0, "i-deleted"),
		pw("wrong", "temp", 1, 1, 2, 0, "i-other"),
	))
	got := map[string]string{}
	for _, b := range st.Broken {
		got[b.Widget] = b.Code
	}
	if len(got) != 2 || got["gone"] != model.LayoutBrokenInstanceMissing || got["wrong"] != model.LayoutBrokenInstancePlugin {
		t.Fatalf("broken = %+v", st.Broken)
	}

	// 之后实例被删除：Current 随之标出
	f.inst.remove("i-early")
	cur, _ := f.svc.Current(ctx)
	got = map[string]string{}
	for _, b := range cur.Broken {
		got[b.Widget] = b.Code
	}
	if got["ok"] != model.LayoutBrokenInstanceMissing {
		t.Fatalf("实例删除后 broken = %+v", cur.Broken)
	}
	vs, _ := f.svc.Versions(ctx)
	if !vs[0].HasBroken {
		t.Fatal("版本列表应标出含失效引用")
	}
}

func TestSave插件消失与小组件消失标为引用失效(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	noPlugin := pw("np", "temp", 1, 1, 0, 0, "")
	noPlugin.PluginID = "vanished"
	noWidget := pw("nw", "ghost", 1, 1, 1, 0, "")
	st := mustSave(t, f, 0, layoutWith(noPlugin, noWidget))
	got := map[string]string{}
	for _, b := range st.Broken {
		got[b.Widget] = b.Code
	}
	if got["np"] != model.LayoutBrokenPluginMissing || got["nw"] != model.LayoutBrokenWidgetMissing {
		t.Fatalf("broken = %+v", st.Broken)
	}
	_ = ctx
}

func TestSaveGeneric与Aggregate引用实例检查(t *testing.T) {
	f := newFixture(t)
	gen := model.LayoutWidget{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "value",
		Size:    model.WidgetSize{Cols: 1, Rows: 1},
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: "i-deleted", Item: "temp"}}},
	}
	agg := model.LayoutWidget{
		ID: "a", Source: model.WidgetSourceAggregate, Template: "status-grid",
		Size: model.WidgetSize{Cols: 2, Rows: 2}, Col: 2,
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{
			{InstanceID: "i-early", Item: "temp"}, {InstanceID: "i-late", Item: "temp"},
		}},
	}
	st := mustSave(t, f, 0, layoutWith(gen, agg))
	if len(st.Broken) != 1 || st.Broken[0].Widget != "g" {
		t.Fatalf("broken = %+v", st.Broken)
	}
	tooMany := gen
	tooMany.Binding.Refs = []model.WidgetRef{{InstanceID: "i-early", Item: "a"}, {InstanceID: "i-early", Item: "b"}}
	_, err := f.svc.Save(context.Background(), 1, layoutWith(tooMany), SaveOptions{Source: model.LayoutSourceEdit})
	if ps := problemsOf(t, err); !hasProblem(ps, "g", model.LayoutProblemInvalid) {
		t.Fatalf("generic 多个 refs: %+v", ps)
	}
}

func TestResolve占位绑定到最早创建的实例且不产生新版本(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustSave(t, f, 0, layoutWith(
		pw("placeholder", "temp", 1, 1, 0, 0, ""),
		pw("bound", "temp", 1, 1, 1, 0, "i-late"),
		pw("titled", "load", 2, 2, 2, 0, "i-early"),
	))
	titled := layoutWith(
		pw("placeholder", "temp", 1, 1, 0, 0, ""),
		pw("bound", "temp", 1, 1, 1, 0, "i-late"),
		pw("titled", "load", 2, 2, 2, 0, "i-early"),
	)
	titled.Screens[0].Widgets[2].Options = map[string]any{"title": "自定义标题"}
	mustSave(t, f, 1, titled)

	res, err := f.svc.Resolve(ctx, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 2 || res.Grid.Cols != 6 || len(res.Screens) != 1 {
		t.Fatalf("resolved = %+v", res)
	}
	ws := res.Screens[0].Widgets
	ph := ws[0]
	if ph.InstanceID != "i-early" || !ph.Placeholder || ph.Template != "value" || ph.Title != "温度" || ph.DisplayState != "ok" {
		t.Fatalf("占位 = %+v", ph)
	}
	refs := ph.Slots["value"]
	if len(refs) != 1 || refs[0].InstanceID != "i-early" || refs[0].Item != "temp" {
		t.Fatalf("占位槽 = %+v", ph.Slots)
	}
	if b := ws[1]; b.InstanceID != "i-late" || b.Placeholder || b.DisplayState != "warning" {
		t.Fatalf("已绑定 = %+v", b)
	}
	if ws[2].Title != "自定义标题" || ws[2].Template != "gauge" {
		t.Fatalf("标题 = %+v", ws[2])
	}
	if en, _ := f.svc.Resolve(ctx, "en"); en.Screens[0].Widgets[0].Title != "Temperature" {
		t.Fatalf("英文标题 = %q", en.Screens[0].Widgets[0].Title)
	}
	// 解析不产生新版本
	if cur, _ := f.svc.Current(ctx); cur.Version != 2 {
		t.Fatalf("version = %d", cur.Version)
	}
	// 布局里仍是占位
	if cur, _ := f.svc.Current(ctx); cur.Layout.Screens[0].Widgets[0].Binding.InstanceID != "" {
		t.Fatal("占位不应被写回布局")
	}
}

func TestResolve无实例为未配置_实例删除为引用失效(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inst.remove("i-early")
	f.inst.remove("i-late")
	mustSave(t, f, 0, layoutWith(
		pw("ph", "temp", 1, 1, 0, 0, ""),
		pw("gone", "temp", 1, 1, 1, 0, "i-deleted"),
	))
	res, _ := f.svc.Resolve(ctx, "zh")
	ws := res.Screens[0].Widgets
	if ws[0].DisplayState != "unconfigured" || ws[0].InstanceID != "" || len(ws[0].Slots) != 0 {
		t.Fatalf("未配置 = %+v", ws[0])
	}
	if ws[1].DisplayState != "broken" || len(ws[1].Slots) != 0 {
		t.Fatalf("引用失效 = %+v", ws[1])
	}
}

func TestResolveGeneric与Aggregate(t *testing.T) {
	f := newFixture(t)
	gen := model.LayoutWidget{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "value",
		Size:    model.WidgetSize{Cols: 1, Rows: 1},
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: "i-early", Item: "temp"}}},
	}
	agg := model.LayoutWidget{
		ID: "a", Source: model.WidgetSourceAggregate, Template: "status-grid",
		Size: model.WidgetSize{Cols: 2, Rows: 2}, Col: 2,
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{
			{InstanceID: "i-early", Item: "temp"}, {InstanceID: "i-late", Item: "temp"},
		}},
	}
	empty := model.LayoutWidget{
		ID: "e", Source: model.WidgetSourceAggregate, Template: "status-grid",
		Size: model.WidgetSize{Cols: 2, Rows: 2}, Col: 4,
	}
	mustSave(t, f, 0, layoutWith(gen, agg, empty))
	res, _ := f.svc.Resolve(context.Background(), "zh")
	ws := res.Screens[0].Widgets
	if ws[0].Template != "value" || ws[0].Title != "温度" || len(ws[0].Slots["value"]) != 1 || ws[0].DisplayState != "ok" {
		t.Fatalf("generic = %+v", ws[0])
	}
	if len(ws[1].Slots["items"]) != 2 || ws[1].DisplayState != "warning" {
		t.Fatalf("aggregate 取最严重的状态: %+v", ws[1])
	}
	if ws[2].DisplayState != "unconfigured" {
		t.Fatalf("空聚合 = %+v", ws[2])
	}
}

func TestScreensUsing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l := layoutWith(pw("a", "temp", 1, 1, 0, 0, "i-early"))
	l.Screens = append(l.Screens, model.LayoutScreen{ID: "s2", Name: "第二屏", Widgets: []model.LayoutWidget{{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "value", Size: model.WidgetSize{Cols: 1, Rows: 1},
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: "i-early", Item: "temp"}}},
	}}}, model.LayoutScreen{ID: "s3", Name: "空屏", Widgets: []model.LayoutWidget{}})
	mustSave(t, f, 0, l)
	refs, err := f.svc.ScreensUsing(ctx, "i-early")
	if err != nil || len(refs) != 2 || refs[0].ID != "index" || refs[0].Name != "首页" || refs[1].ID != "s2" {
		t.Fatalf("refs = %+v %v", refs, err)
	}
	refs, _ = f.svc.ScreensUsing(ctx, "i-late")
	if refs == nil || len(refs) != 0 {
		t.Fatalf("无引用应为空切片: %#v", refs)
	}
}

func TestVersion读取指定版本(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustSave(t, f, 0, layoutWith(pw("a", "temp", 1, 1, 0, 0, "")))
	mustSave(t, f, 1, layoutWith())
	st, err := f.svc.Version(ctx, 1)
	if err != nil || st.Version != 1 || len(st.Layout.Screens[0].Widgets) != 1 {
		t.Fatalf("Version(1) = %+v %v", st, err)
	}
	if _, err := f.svc.Version(ctx, 0); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("Version(0): %v", err)
	}
}

func TestCatalog聚合目录至少支持2x2与4x2(t *testing.T) {
	c := New(Config{}).Catalog()
	var sizes []model.WidgetSize
	for _, e := range c.Aggregate {
		if e.Template == "status-grid" {
			sizes = e.Sizes
		}
	}
	has := func(w, h int) bool {
		for _, s := range sizes {
			if s.Cols == w && s.Rows == h {
				return true
			}
		}
		return false
	}
	if !has(2, 2) || !has(4, 2) {
		t.Fatalf("status-grid 尺寸 = %+v", sizes)
	}
	if len(c.Generic) == 0 {
		t.Fatal("通用目录为空")
	}
}

func TestResolve为每个引用补数据项标题(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	gen := model.LayoutWidget{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "value",
		Size:    model.WidgetSize{Cols: 1, Rows: 1},
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: "i-early", Item: "load"}}},
	}
	agg := model.LayoutWidget{
		ID: "a", Source: model.WidgetSourceAggregate, Template: "status-grid",
		Size: model.WidgetSize{Cols: 2, Rows: 2}, Col: 2,
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{
			{InstanceID: "i-early", Item: "temp"},
			{InstanceID: "i-late", Item: "target[home]"},
			{InstanceID: "i-early", Item: "nothing"},
			{InstanceID: "i-other", Item: "temp"},
		}},
	}
	mustSave(t, f, 0, layoutWith(pw("p", "load", 2, 2, 0, 2, "i-early"), gen, agg))
	zh, err := f.svc.Resolve(ctx, "zh")
	if err != nil {
		t.Fatal(err)
	}
	ws := zh.Screens[0].Widgets
	if got := ws[0].Slots["value"][0].Title; got != "负载" {
		t.Fatalf("plugin 槽标题 = %q", got)
	}
	if got := ws[1].Slots["value"][0].Title; got != "负载" {
		t.Fatalf("generic 槽标题 = %q", got)
	}
	var titles []string
	for _, r := range ws[2].Slots["items"] {
		titles = append(titles, r.Title)
	}
	// 动态键成员取方括号里的名字；manifest 没有声明的项与别的插件的实例没有标题
	want := []string{"温度", "home", "", ""}
	if len(titles) != len(want) {
		t.Fatalf("titles = %v", titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("聚合标题 = %v，期望 %v", titles, want)
		}
	}
	en, _ := f.svc.Resolve(ctx, "en")
	if got := en.Screens[0].Widgets[2].Slots["items"][0].Title; got != "Temperature" {
		t.Fatalf("英文标题 = %q", got)
	}
}
