package seed

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	_ "github.com/LanceLRQ/PiMon/src/plugins/core"
	_ "github.com/LanceLRQ/PiMon/src/plugins/hostmetrics"
	_ "github.com/LanceLRQ/PiMon/src/plugins/netreach"
	_ "github.com/LanceLRQ/PiMon/src/plugins/weather"
)

// fakeInstances 记录创建调用，List 返回已有与新建的实例。
type fakeInstances struct {
	list    []model.Instance
	created []model.InstanceInput
	listErr error
}

func (f *fakeInstances) List(context.Context) ([]model.Instance, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]model.Instance(nil), f.list...), nil
}

func (f *fakeInstances) Create(_ context.Context, in model.InstanceInput) (model.InstanceDetail, error) {
	f.created = append(f.created, in)
	inst := model.Instance{
		ID: fmt.Sprintf("new-%s", in.PluginID), PluginID: in.PluginID, Name: in.Name,
		CreatedAt: time.Date(2026, 10, 1, 0, 0, len(f.list), 0, time.UTC),
	}
	f.list = append(f.list, inst)
	return model.InstanceDetail{Instance: inst}, nil
}

type fixture struct {
	seeder *Seeder
	inst   *fakeInstances
	layout *screens.Service
	lang   string
}

type registry map[string]plugins.Plugin

func (r registry) Get(id string) (plugins.Plugin, bool) { p, ok := r[id]; return p, ok }

func newFixture(t *testing.T, existing ...model.Instance) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	reg := registry{}
	for _, s := range runtime.Builtins() {
		m := s.Manifest()
		reg[m.ID] = plugins.Plugin{ID: m.ID, Origin: plugins.OriginBuiltin, Manifest: m, Source: s}
	}
	f := &fixture{inst: &fakeInstances{list: existing}, lang: "zh"}
	f.layout = screens.New(screens.Config{
		DB: db, Clock: clock.NewFake(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)),
		Plugins: reg, Instances: f.inst,
	})
	f.seeder = New(Config{Instances: f.inst, Layouts: f.layout, Language: func() string { return f.lang }})
	return f
}

func (f *fixture) createdPlugins() []string {
	var out []string
	for _, c := range f.inst.created {
		out = append(out, c.PluginID)
	}
	return out
}

func TestRunSeedsInstancesAndLayoutOnEmptyDB(t *testing.T) {
	f := newFixture(t)
	if err := f.seeder.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := f.createdPlugins()
	want := []string{"core", "weather", "host-metrics", "net-reach"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("创建的实例 = %v，期望 %v", got, want)
	}
	st, err := f.layout.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 1 || st.Source != model.LayoutSourceSeed || st.Layout.Grid != (model.Grid{Cols: 8, Rows: 5}) {
		t.Fatalf("v1 应为 8x5 种子: %+v", st)
	}
	if len(st.Broken) != 0 {
		t.Fatalf("种子布局不应有失效引用: %+v", st.Broken)
	}
	if n := len(st.Layout.Screens); n != 1 || st.Layout.Screens[0].ID != model.IndexScreenID || st.Layout.Screens[0].Name != "首页" {
		t.Fatalf("应只有名为「首页」的 index: %+v", st.Layout.Screens)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 2; i++ {
		if err := f.seeder.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.inst.created) != 4 {
		t.Fatalf("第二次不应再创建实例: %v", f.createdPlugins())
	}
	if st, _ := f.layout.Current(context.Background()); st.Version != 1 {
		t.Fatalf("第二次不应再写版本: %d", st.Version)
	}
}

func TestRunSkipsWhenLayoutVersionExists(t *testing.T) {
	f := newFixture(t)
	empty := model.Layout{Grid: model.Grid{Cols: 6, Rows: 4}, Screens: []model.LayoutScreen{
		{ID: model.IndexScreenID, Name: "首页", InRotation: true, Widgets: []model.LayoutWidget{}},
	}}
	if _, err := f.layout.Save(context.Background(), 0, empty, screens.SaveOptions{Source: model.LayoutSourceEdit}); err != nil {
		t.Fatal(err)
	}
	if err := f.seeder.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.inst.created) != 0 {
		t.Fatalf("已有布局版本时不应创建实例: %v", f.createdPlugins())
	}
	if st, _ := f.layout.Current(context.Background()); st.Version != 1 || st.Source != model.LayoutSourceEdit {
		t.Fatalf("不应覆盖已有布局: %+v", st)
	}
}

func TestRunReusesExistingInstanceOfSamePlugin(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f := newFixture(t,
		model.Instance{ID: "w-late", PluginID: "weather", Name: "后建", CreatedAt: base.Add(time.Hour)},
		model.Instance{ID: "w-early", PluginID: "weather", Name: "先建", CreatedAt: base},
	)
	if err := f.seeder.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.createdPlugins()) != "[core host-metrics net-reach]" {
		t.Fatalf("已有 weather 实例应复用: %v", f.createdPlugins())
	}
	st, _ := f.layout.Current(context.Background())
	var found bool
	for _, w := range st.Layout.Screens[0].Widgets {
		if w.PluginID == "weather" {
			found = true
			if w.Binding.InstanceID != "w-early" {
				t.Errorf("应绑定最早创建的 weather 实例，得到 %q", w.Binding.InstanceID)
			}
		}
	}
	if !found {
		t.Fatal("布局里没有天气小组件")
	}
}

func TestRunSeedsEnglishHomeName(t *testing.T) {
	f := newFixture(t)
	f.lang = "en"
	if err := f.seeder.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := f.layout.Current(context.Background())
	if st.Layout.Screens[0].Name != "Home" {
		t.Fatalf("英文首页名 = %q", st.Layout.Screens[0].Name)
	}
}

// 三份种子布局（6x4、8x5、10x6）都必须通过布局校验，且各自含期望的核心小组件。
func TestAllSeedLayoutsPassValidation(t *testing.T) {
	f := newFixture(t)
	if err := f.seeder.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	grids := []model.Grid{{Cols: 6, Rows: 4}, {Cols: 8, Rows: 5}, {Cols: 10, Rows: 6}}
	for _, g := range grids {
		l, ok := f.seeder.Layout(g)
		if !ok || l.Grid != g {
			t.Fatalf("网格 %+v 没有种子: ok=%v grid=%+v", g, ok, l.Grid)
		}
		cur, _ := f.layout.Current(context.Background())
		saved, err := f.layout.Save(context.Background(), cur.Version, l, screens.SaveOptions{Source: model.LayoutSourceAuto})
		if err != nil {
			t.Fatalf("网格 %+v 种子布局校验失败: %v", g, err)
		}
		if len(saved.Broken) != 0 {
			t.Errorf("网格 %+v 种子布局有失效引用: %+v", g, saved.Broken)
		}
		kinds := map[string]bool{}
		for _, w := range saved.Layout.Screens[0].Widgets {
			switch {
			case w.PluginID == "core" && w.WidgetID == "clock":
				kinds["clock"] = true
			case w.PluginID == "weather":
				kinds["weather"] = true
			case w.PluginID == "host-metrics":
				kinds["host"] = true
			case w.Source == model.WidgetSourceAggregate && w.Template == "status-grid":
				kinds["reach"] = true
			}
		}
		if len(kinds) != 4 {
			t.Errorf("网格 %+v 应含时钟、天气、主机指标与网络连通总览: %v", g, kinds)
		}
	}
	if _, ok := f.seeder.Layout(model.Grid{Cols: 7, Rows: 3}); ok {
		t.Error("没有种子的网格应返回 ok=false")
	}
}

func TestSeedLayoutOmitsReachWhenInstanceMissing(t *testing.T) {
	f := newFixture(t) // 未运行 Run：没有任何实例
	l, ok := f.seeder.Layout(model.Grid{Cols: 8, Rows: 5})
	if !ok {
		t.Fatal("应能生成")
	}
	for _, w := range l.Screens[0].Widgets {
		if w.Source == model.WidgetSourceAggregate {
			t.Fatalf("没有 net-reach 实例时不应放聚合小组件: %+v", w)
		}
	}
}

// 读不到实例列表时不提供种子（而不是给出残缺布局），Run 则返回错误且不写任何东西。
func TestLayoutUnavailableWhenInstanceListFails(t *testing.T) {
	f := newFixture(t)
	f.inst.listErr = errors.New("读实例失败")
	if l, ok := f.seeder.Layout(model.Grid{Cols: 8, Rows: 5}); ok {
		t.Fatalf("读实例失败时应 ok=false: %+v", l)
	}
	if err := f.seeder.Run(context.Background()); err == nil {
		t.Fatal("Run 应返回错误")
	}
	if st, _ := f.layout.Current(context.Background()); st.Version != 0 {
		t.Fatalf("不应写入布局: %+v", st)
	}
}
