package screens

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

const hostMetricsManifest = `id: host-metrics
version: 1.0.0
api_version: 1
name: {zh: 主机指标, en: Host metrics}
kind: source
runtime: builtin
runs_on: [hub, agent]
interval: 30s
timeout: 20s
outputs:
  - {key: cpu, type: gauge, title: {zh: CPU 使用率, en: CPU usage}}
`

// execOnlyManifest 是只在设备上存在的 exec 插件（中枢注册表里没有）。
const execOnlyManifest = `id: nas-raid
version: 1.0.0
api_version: 1
name: {zh: 阵列, en: RAID}
kind: source
runtime: exec
runs_on: [agent]
interval: 60s
timeout: 10s
outputs:
  - {key: health, type: state, title: {zh: 健康, en: Health}}
widgets:
  - id: health
    name: {zh: 阵列健康, en: RAID health}
    sizes:
      1x1: {template: state, bind: {value: {item: health}}}
`

type fakeDeviceSource struct {
	list []model.Device
	err  error
}

func (f *fakeDeviceSource) List(context.Context) ([]model.Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]model.Device(nil), f.list...), nil
}

func mustManifest(t *testing.T, src string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func hostsWidget(id string, col, row int) model.LayoutWidget {
	return model.LayoutWidget{
		ID: id, Source: model.WidgetSourceAggregate, Template: "status-grid",
		Size: model.WidgetSize{Cols: 2, Rows: 2}, Col: col, Row: row,
		Binding: model.WidgetBinding{DataSource: model.AggregateSourceHosts},
	}
}

// newHostsFixture 在演示插件之外注册 host-metrics，并准备 hub 与设备上的 host-metrics 实例。
func newHostsFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	reg := f.svc.reg.(fakePlugins)
	reg["host-metrics"] = plugins.Plugin{ID: "host-metrics", Manifest: mustManifest(t, hostMetricsManifest)}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f.inst.list = append(f.inst.list,
		model.Instance{ID: "hm-hub-late", PluginID: "host-metrics", RunsOn: "hub", DisplayState: "ok", CreatedAt: base.Add(time.Hour)},
		model.Instance{ID: "hm-hub", PluginID: "host-metrics", RunsOn: "hub", DisplayState: "ok", CreatedAt: base},
		// 设备 d1 上最早的实例已暂停，仍算 CPU 来源；它的展示状态严重也不影响整块灯组。
		model.Instance{ID: "hm-d1", PluginID: "host-metrics", RunsOn: "d1", Paused: true, DisplayState: "critical", CreatedAt: base.Add(2 * time.Hour)},
		model.Instance{ID: "hm-d1-late", PluginID: "host-metrics", RunsOn: "d1", DisplayState: "ok", CreatedAt: base.Add(3 * time.Hour)},
	)
	return f
}

func TestCatalog聚合目录含主机灯组且尺寸同status_grid(t *testing.T) {
	c := New(Config{}).Catalog()
	var plain, hosts *model.CatalogEntry
	for i, e := range c.Aggregate {
		switch {
		case e.Template == "status-grid" && e.DataSource == "":
			plain = &c.Aggregate[i]
		case e.Template == "status-grid" && e.DataSource == model.AggregateSourceHosts:
			hosts = &c.Aggregate[i]
		}
	}
	if plain == nil || hosts == nil {
		t.Fatalf("聚合目录应同时有 status-grid 与主机灯组: %+v", c.Aggregate)
	}
	if fmt.Sprint(plain.Sizes) != fmt.Sprint(hosts.Sizes) {
		t.Fatalf("灯组尺寸应同 status-grid: %v vs %v", hosts.Sizes, plain.Sizes)
	}
}

func TestSave主机灯组校验(t *testing.T) {
	f := newHostsFixture(t)
	ctx := context.Background()
	st := mustSave(t, f, 0, layoutWith(hostsWidget("h", 0, 0)))
	if len(st.Broken) != 0 {
		t.Fatalf("灯组不应有失效引用: %+v", st.Broken)
	}

	withRefs := hostsWidget("h", 0, 0)
	withRefs.Binding.Refs = []model.WidgetRef{{InstanceID: "hm-hub", Item: "cpu"}}
	wrongTpl := hostsWidget("t", 2, 0)
	wrongTpl.Template = "list"
	unknownSrc := hostsWidget("u", 4, 0)
	unknownSrc.Binding.DataSource = "planets"
	generic := model.LayoutWidget{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "value", Size: model.WidgetSize{Cols: 1, Rows: 1}, Col: 0, Row: 2,
		Binding: model.WidgetBinding{DataSource: model.AggregateSourceHosts},
	}
	plug := pw("p", "temp", 1, 1, 1, 2, "")
	plug.Binding.DataSource = model.AggregateSourceHosts
	badSize := hostsWidget("s", 2, 2)
	badSize.Size = model.WidgetSize{Cols: 1, Rows: 1}

	_, err := f.svc.Save(ctx, 1, layoutWith(withRefs, wrongTpl, unknownSrc, generic, plug, badSize), SaveOptions{})
	ps := problemsOf(t, err)
	field := map[string]string{}
	for _, p := range ps {
		field[p.Widget] = p.Field
	}
	want := map[string]string{
		"h": "screens[0].widgets[0].binding.refs",
		"t": "screens[0].widgets[1].template",
		"u": "screens[0].widgets[2].binding.data_source",
		"g": "screens[0].widgets[3].binding.data_source",
		"p": "screens[0].widgets[4].binding.data_source",
	}
	for w, f := range want {
		if field[w] != f {
			t.Errorf("小组件 %s 的问题字段 = %q，期望 %q（全部：%+v）", w, field[w], f, ps)
		}
	}
	if !hasProblem(ps, "s", model.LayoutProblemSizeNotAllowed) {
		t.Errorf("灯组 1x1 应不在允许尺寸内: %+v", ps)
	}
}

func TestResolve主机灯组未注入设备时只有中枢(t *testing.T) {
	f := newHostsFixture(t)
	mustSave(t, f, 0, layoutWith(hostsWidget("h", 0, 0)))
	res, err := f.svc.Resolve(context.Background(), "zh")
	if err != nil {
		t.Fatal(err)
	}
	w := res.Screens[0].Widgets[0]
	if len(w.Hosts) != 1 || w.Hosts[0] != (model.ResolvedHost{ID: "hub", State: model.PresenceOnline, InstanceID: "hm-hub"}) {
		t.Fatalf("hosts = %+v", w.Hosts)
	}
	if w.DisplayState != "ok" || w.Template != "status-grid" {
		t.Fatalf("灯组 = %+v", w)
	}
	items := w.Slots["items"]
	if len(items) != 1 || items[0] != (model.ResolvedRef{InstanceID: "hm-hub", Item: "cpu", Title: "CPU 使用率"}) {
		t.Fatalf("items = %+v", items)
	}
}

func TestResolve主机灯组各状态与顺序(t *testing.T) {
	f := newHostsFixture(t)
	incompat := &model.DeviceIncompatible{Reason: "version_too_old", RequiredMin: 2}
	devs := &fakeDeviceSource{list: []model.Device{
		{ID: "d1", Name: "NAS", Confirmed: true, Presence: model.PresenceOnline},
		{ID: "d2", Name: "Mac", Confirmed: true, Presence: model.PresenceDisconnected},
		{ID: "d3", Name: "占位", Confirmed: false, Presence: model.PresencePending, Replaces: "d2"},
		{ID: "d4", Name: "VPS", Confirmed: true, Presence: model.PresenceOffline},
		{ID: "d5", Name: "笔记本", Confirmed: true, Presence: model.PresenceDormant},
		{ID: "d6", Name: "新机", Confirmed: true, Presence: model.PresencePending},
		{ID: "d7", Name: "维护中", Confirmed: true, Presence: model.PresenceOnline, Maintenance: true, Incompatible: incompat},
		{ID: "d8", Name: "旧版", Confirmed: true, Presence: model.PresenceOffline, Incompatible: incompat},
		{ID: "d9", Name: "刚升级", Confirmed: true, Presence: model.PresenceOnline, Incompatible: incompat},
	}}
	f.svc.UseDevices(devs)
	mustSave(t, f, 0, layoutWith(hostsWidget("h", 0, 0)))
	res, err := f.svc.Resolve(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	w := res.Screens[0].Widgets[0]
	want := []model.ResolvedHost{
		{ID: "hub", State: model.PresenceOnline, InstanceID: "hm-hub"},
		{ID: "d1", Name: "NAS", State: model.PresenceOnline, InstanceID: "hm-d1"},
		{ID: "d2", Name: "Mac", State: model.PresenceDisconnected},
		{ID: "d4", Name: "VPS", State: model.PresenceOffline},
		{ID: "d5", Name: "笔记本", State: model.PresenceDormant},
		{ID: "d6", Name: "新机", State: model.PresencePending},
		{ID: "d7", Name: "维护中", State: model.HostStateMaintenance},
		{ID: "d8", Name: "旧版", State: model.HostStateIncompatible},
		{ID: "d9", Name: "刚升级", State: model.PresenceOnline},
	}
	if fmt.Sprint(w.Hosts) != fmt.Sprint(want) {
		t.Fatalf("hosts =\n%+v\n期望\n%+v", w.Hosts, want)
	}
	if w.DisplayState != "ok" {
		t.Fatalf("整块灯组展示状态应固定为 ok: %q", w.DisplayState)
	}
	items := w.Slots["items"]
	if len(items) != 2 || items[0].InstanceID != "hm-hub" || items[1].InstanceID != "hm-d1" || items[1].Item != "cpu" || items[1].Title != "CPU usage" {
		t.Fatalf("items 应按灯组顺序放各机器的 cpu 引用: %+v", items)
	}
	ids := ReferencedInstanceIDs(res)
	if fmt.Sprint(ids) != "[hm-d1 hm-hub]" {
		t.Fatalf("灯组引用的实例应进入推送集合: %v", ids)
	}
}

func TestResolve没有灯组时不读设备_读设备失败返回错误(t *testing.T) {
	f := newHostsFixture(t)
	devs := &fakeDeviceSource{err: errors.New("读库失败")}
	f.svc.UseDevices(devs)
	mustSave(t, f, 0, layoutWith(pw("a", "temp", 1, 1, 0, 0, "i-early")))
	if _, err := f.svc.Resolve(context.Background(), "zh"); err != nil {
		t.Fatalf("布局没有灯组时不应读设备: %v", err)
	}
	mustSave(t, f, 1, layoutWith(hostsWidget("h", 0, 0)))
	if _, err := f.svc.Resolve(context.Background(), "zh"); err == nil {
		t.Fatal("读设备失败应返回错误，等待下一轮再解析")
	}
}

// 只在设备上的 exec 插件：中枢注册表没有，按绑定实例的运行位置查设备上报的 manifest。
func TestExec插件只在设备上时回退查设备manifest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reg := f.svc.reg.(fakePlugins)
	reg["d1/nas-raid"] = plugins.Plugin{ID: "nas-raid", Manifest: mustManifest(t, execOnlyManifest)}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f.inst.list = append(f.inst.list,
		model.Instance{ID: "raid", PluginID: "nas-raid", RunsOn: "d1", DisplayState: "ok", CreatedAt: base},
		model.Instance{ID: "raid-hub", PluginID: "nas-raid", RunsOn: "hub", DisplayState: "ok", CreatedAt: base.Add(time.Hour)},
	)
	bound := model.LayoutWidget{
		ID: "b", Source: model.WidgetSourcePlugin, PluginID: "nas-raid", WidgetID: "health",
		Size: model.WidgetSize{Cols: 1, Rows: 1}, Binding: model.WidgetBinding{InstanceID: "raid"},
	}
	placeholder := bound
	placeholder.ID, placeholder.Col, placeholder.Binding = "ph", 1, model.WidgetBinding{}
	gen := model.LayoutWidget{
		ID: "g", Source: model.WidgetSourceGeneric, Template: "state", Size: model.WidgetSize{Cols: 1, Rows: 1}, Col: 2,
		Binding: model.WidgetBinding{Refs: []model.WidgetRef{{InstanceID: "raid", Item: "health"}}},
	}
	onHub := bound
	onHub.ID, onHub.Col, onHub.Binding = "hub", 3, model.WidgetBinding{InstanceID: "raid-hub"}
	st := mustSave(t, f, 0, layoutWith(bound, placeholder, gen, onHub))
	broken := map[string]string{}
	for _, b := range st.Broken {
		broken[b.Widget] = b.Code
	}
	if len(broken) != 1 || broken["hub"] != model.LayoutBrokenPluginMissing {
		t.Fatalf("只有绑在中枢实例上的应为 plugin_missing: %+v", st.Broken)
	}

	res, err := f.svc.Resolve(ctx, "zh")
	if err != nil {
		t.Fatal(err)
	}
	ws := res.Screens[0].Widgets
	for _, w := range ws[:2] {
		if w.DisplayState != "ok" || w.Template != "state" || w.Title != "阵列健康" || len(w.Slots["value"]) != 1 || w.Slots["value"][0].Title != "健康" {
			t.Errorf("设备插件小组件应按设备 manifest 解析: %+v", w)
		}
	}
	if ws[2].Title != "健康" || ws[2].Slots["value"][0].Title != "健康" {
		t.Errorf("通用小组件标题应取设备 manifest: %+v", ws[2])
	}
	if ws[3].DisplayState != "broken" {
		t.Errorf("中枢上没有该插件应为引用失效: %+v", ws[3])
	}
}
