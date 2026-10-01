package ws

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// fakeLayouts 是可变的布局来源：Current 返回原始布局，Resolve 返回解析后布局。
type fakeLayouts struct {
	mu       sync.Mutex
	state    model.LayoutState
	resolved model.ResolvedLayout
	lang     string
}

func (f *fakeLayouts) Current(context.Context) (model.LayoutState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *fakeLayouts) Resolve(_ context.Context, lang string) (model.ResolvedLayout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lang = lang
	return f.resolved, nil
}

// set 同时更新原始布局版本与解析后布局：每个 instance 一个占位小组件（引用该实例）。
func (f *fakeLayouts) set(version int, displayState string, instanceIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = model.LayoutState{Version: version, Source: model.LayoutSourceEdit, Layout: model.Layout{
		Grid: model.Grid{Cols: 8, Rows: 5}, Screens: []model.LayoutScreen{{ID: "index", Name: "首页", InRotation: true, Widgets: []model.LayoutWidget{}}},
	}, Broken: []model.LayoutProblem{}}
	ws := []model.ResolvedWidget{}
	for i, id := range instanceIDs {
		ws = append(ws, model.ResolvedWidget{
			ID: "w" + id, Source: model.WidgetSourcePlugin, PluginID: "p", Template: "value",
			Size: model.WidgetSize{Cols: 1, Rows: 1}, Col: i, InstanceID: id, DisplayState: displayState,
			Slots: map[string][]model.WidgetRef{"value": {{InstanceID: id, Item: "x"}}},
		})
	}
	f.resolved = model.ResolvedLayout{Version: version, Grid: model.Grid{Cols: 8, Rows: 5}, Screens: []model.ResolvedScreen{
		{ID: "index", Name: "首页", InRotation: true, Widgets: ws},
	}}
}

type fakeScreenState struct {
	mu sync.Mutex
	st model.ScreenState
}

func (f *fakeScreenState) State() model.ScreenState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

type fakeScreenData struct {
	mu   sync.Mutex
	byID map[string]model.ScreenInstanceData
}

func (f *fakeScreenData) set(id, summary string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[id] = model.ScreenInstanceData{
		InstanceID: id, DisplayState: "ok", Summary: summary,
		Items: []report.Item{{Key: "x", Type: report.TypeNumber}},
	}
}

func (f *fakeScreenData) ScreenData(_ context.Context, id string) (model.ScreenInstanceData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.byID[id]
	if !ok {
		return model.ScreenInstanceData{}, instances.ErrNotFound
	}
	return d, nil
}

type fakeSink struct {
	mu       sync.Mutex
	viewport []model.Viewport
	coarse   []bool
	current  []string
	online   []bool
}

func (f *fakeSink) ReportViewport(v model.Viewport) {
	f.mu.Lock()
	f.viewport = append(f.viewport, v)
	f.mu.Unlock()
}

func (f *fakeSink) ReportCoarsePointer(c bool) {
	f.mu.Lock()
	f.coarse = append(f.coarse, c)
	f.mu.Unlock()
}

func (f *fakeSink) ReportCurrentScreen(id string) {
	f.mu.Lock()
	f.current = append(f.current, id)
	f.mu.Unlock()
}

func (f *fakeSink) SetScreenOnline(on bool) {
	f.mu.Lock()
	f.online = append(f.online, on)
	f.mu.Unlock()
}

func (f *fakeSink) onlineLog() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.online...)
}

type screenHarness struct {
	*harness
	layouts *fakeLayouts
	state   *fakeScreenState
	data    *fakeScreenData
	sink    *fakeSink
}

func newScreenHarness(t *testing.T, list ...model.Instance) *screenHarness {
	t.Helper()
	sh := &screenHarness{
		layouts: &fakeLayouts{},
		state:   &fakeScreenState{st: model.ScreenState{Mode: model.ScreenModeOn, ThemeID: model.ThemeAmbient, Reason: model.ScreenReasonSchedule}},
		data:    &fakeScreenData{byID: map[string]model.ScreenInstanceData{}},
		sink:    &fakeSink{},
	}
	sh.layouts.set(1, "ok", "a")
	sh.data.set("a", "A")
	sh.data.set("b", "B")
	sh.harness = newHarnessFull(t, 0, nil, func(c *Config) {
		c.Layouts, c.ScreenState, c.ScreenData, c.Sink = sh.layouts, sh.state, sh.data, sh.sink
	}, list...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sh.hub.Start(ctx) // 建立推送去重基线
	return sh
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("期望对象，得到 %v", v)
	}
	return m
}

func asList(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("期望数组，得到 %v", v)
	}
	return l
}

func topicSet(m map[string]any) string {
	var parts []string
	for _, x := range m["topics"].([]any) {
		parts = append(parts, x.(string))
	}
	return strings.Join(parts, ",")
}

func TestScreenSnapshotCarriesResolvedLayoutStateAndReferencedData(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"), inst("b", "y"))
	c := h.dial(screenToken)
	m := read(t, c)

	if got := topicSet(m); got != "layout,screen_data,screen_state,settings" {
		t.Fatalf("屏幕会话默认主题 = %s", got)
	}
	rl := asMap(t, m["resolved_layout"])
	if rl["version"] != float64(1) {
		t.Fatalf("应带解析后布局: %v", rl)
	}
	if m["layout"] != nil {
		t.Fatalf("屏幕会话不应拿到原始布局: %v", m["layout"])
	}
	if st := asMap(t, m["screen_state"]); st["mode"] != "on" || st["theme_id"] != "ambient" {
		t.Fatalf("screen_state = %v", st)
	}
	data := asList(t, m["screen_data"])
	if len(data) != 1 || asMap(t, data[0])["instance_id"] != "a" {
		t.Fatalf("screen_data 只应含布局引用的实例 a: %v", data)
	}
	if len(asList(t, m["instances"])) != 0 || m["settings"] != nil {
		t.Fatalf("屏幕会话不应拿到实例列表与完整设置: %v", m)
	}
	if h.layouts.lang != "zh" {
		t.Fatalf("解析布局应按设置语言: %q", h.layouts.lang)
	}
}

func TestAdminSnapshotCarriesRawLayoutAndScreenStateButNoData(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"))
	c := h.dial(adminToken)
	m := read(t, c)

	if got := topicSet(m); got != "instances,layout,screen_state,settings" {
		t.Fatalf("管理员默认主题 = %s", got)
	}
	if l := asMap(t, m["layout"]); l["version"] != float64(1) || l["layout"] == nil {
		t.Fatalf("管理员应拿到原始布局与版本: %v", l)
	}
	if m["resolved_layout"] != nil || m["screen_data"] != nil {
		t.Fatalf("管理员默认不收解析后布局与屏幕数据: %v", m)
	}

	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicLayout, ui.TopicScreenData}})
	m = read(t, c)
	if data := asList(t, m["screen_data"]); len(data) != 1 {
		t.Fatalf("管理员订阅 screen_data 后应带数据: %v", m)
	}
}

func TestScreenSessionStillCannotSubscribeInstances(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"))
	c := h.dial(screenToken)
	read(t, c)
	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicInstances, ui.TopicScreenData}})
	e := read(t, c)
	if e["type"] != string(ui.TypeError) || e["error"].(map[string]any)["code"] != ui.ErrSubscribeDenied {
		t.Fatalf("屏幕会话订阅 instances 应被拒: %v", e)
	}
}

func TestNotifyLayoutPushesImmediatelyPerRole(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"))
	admin, screen := h.dial(adminToken), h.dial(screenToken)
	read(t, admin)
	read(t, screen)

	h.layouts.set(2, "ok", "a", "b")
	h.hub.NotifyLayout(h.layouts.state) // 不推进时钟：不进 1 秒合并窗口

	a := read(t, admin)
	if a["entity"] != ui.EntityLayout || asMap(t, a["layout"])["version"] != float64(2) || a["resolved_layout"] != nil {
		t.Fatalf("管理员应收到原始布局 patch: %v", a)
	}
	s := read(t, screen)
	if s["entity"] != ui.EntityLayout || asMap(t, s["resolved_layout"])["version"] != float64(2) || s["layout"] != nil {
		t.Fatalf("屏幕应收到解析后布局 patch: %v", s)
	}
	d := read(t, screen)
	if d["entity"] != ui.EntityScreenData {
		t.Fatalf("新引用的实例数据应随即推送: %v", d)
	}
	got := asList(t, d["screen_data"])
	if len(got) != 1 || asMap(t, got[0])["instance_id"] != "b" {
		t.Fatalf("只应推送新引用的实例 b: %v", got)
	}
	ping(t, admin)
	ping(t, screen)
}

func TestNotifyLayoutDropsOlderVersion(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"))
	admin := h.dial(adminToken)
	read(t, admin)

	h.layouts.set(3, "ok", "a")
	st3 := h.layouts.state
	h.layouts.set(2, "ok", "a")
	st2 := h.layouts.state
	h.hub.NotifyLayout(st3)
	h.hub.NotifyLayout(st2) // 乱序到达的较旧版本

	if p := read(t, admin); asMap(t, p["layout"])["version"] != float64(3) {
		t.Fatalf("应先收到 v3: %v", p)
	}
	ping(t, admin) // v2 被丢弃，没有第二个 patch
}

func TestNotifyScreenStateAndControlAreImmediateAndRoleScoped(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"))
	admin, screen := h.dial(adminToken), h.dial(screenToken)
	read(t, admin)
	read(t, screen)

	off := model.ScreenState{Mode: model.ScreenModeOff, ThemeID: model.ThemeAmbient, Reason: model.ScreenReasonRemoteOff}
	h.hub.NotifyScreenState(off)
	for _, c := range []*websocket.Conn{admin, screen} {
		p := read(t, c)
		if p["entity"] != ui.EntityScreenState || asMap(t, p["screen_state"])["mode"] != "off" {
			t.Fatalf("两种角色都应立即收到 screen_state: %v", p)
		}
	}

	if n := h.hub.SendScreenControl(7, model.ScreenActionSwitch, "screen2"); n != 1 {
		t.Fatalf("应只发给 1 个屏幕会话: %d", n)
	}
	m := read(t, screen)
	if m["type"] != string(ui.TypeScreenControl) || m["action"] != "switch" || m["screen_id"] != "screen2" || m["op_id"] != float64(7) {
		t.Fatalf("屏幕指令 = %v", m)
	}
	ping(t, admin) // 管理员没有收到指令
}

func TestSendScreenControlWithoutScreenReturnsZero(t *testing.T) {
	h := newScreenHarness(t)
	admin := h.dial(adminToken)
	read(t, admin)
	if n := h.hub.SendScreenControl(1, model.ScreenActionRefresh, ""); n != 0 {
		t.Fatalf("没有屏幕在线应返回 0: %d", n)
	}
	ping(t, admin)
}

func TestScreenDataPatchOnlyForReferencedInstancesAndCoalesced(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"), inst("b", "y"))
	screen := h.dial(screenToken)
	read(t, screen)

	h.data.set("b", "B2") // 未被布局引用
	h.hub.NotifyInstance("b")
	h.clk.Advance(time.Second)
	ping(t, screen) // 没有 screen_data patch

	for _, s := range []string{"A1", "A2"} {
		h.data.set("a", s)
		h.hub.NotifyInstance("a")
	}
	ping(t, screen) // 窗口未到
	h.clk.Advance(time.Second)
	p := read(t, screen)
	got := asList(t, p["screen_data"])
	if p["entity"] != ui.EntityScreenData || len(got) != 1 || asMap(t, got[0])["summary"] != "A2" {
		t.Fatalf("应合并为一个带最新数据的 screen_data patch: %v", p)
	}
	ping(t, screen)

	// 内容没变的重复通知不再推送。
	h.hub.NotifyInstance("a")
	h.clk.Advance(time.Second)
	ping(t, screen)
}

func TestReconcilePushesResolvedLayoutWhenDisplayStateChanges(t *testing.T) {
	h := newScreenHarness(t, inst("a", "x"))
	screen := h.dial(screenToken)
	read(t, screen)

	h.layouts.set(1, "stale", "a") // 版本不变，仅展示状态随时间变化
	h.clk.Advance(reconcileInterval)
	p := read(t, screen)
	if p["entity"] != ui.EntityLayout {
		t.Fatalf("展示状态变化应重推解析后布局: %v", p)
	}
	w := asMap(t, asList(t, asMap(t, asList(t, asMap(t, p["resolved_layout"])["screens"])[0])["widgets"])[0])
	if w["display_state"] != "stale" {
		t.Fatalf("display_state = %v", w["display_state"])
	}
}

func TestViewportReportOnlyFromScreenSessions(t *testing.T) {
	h := newScreenHarness(t)
	admin, screen := h.dial(adminToken), h.dial(screenToken)
	read(t, admin)
	read(t, screen)
	coarse := true

	send(t, admin, ui.ClientMessage{Type: ui.TypeViewportReport, Viewport: &model.Viewport{W: 1, H: 1, DPR: 1}, CoarsePointer: &coarse, CurrentScreen: "x"})
	ping(t, admin)
	send(t, screen, ui.ClientMessage{
		Type: ui.TypeViewportReport, Viewport: &model.Viewport{W: 1024, H: 600, DPR: 1}, CoarsePointer: &coarse, CurrentScreen: "screen2",
	})
	send(t, screen, ui.ClientMessage{Type: ui.TypeViewportReport, CurrentScreen: strings.Repeat("x", maxCurrentScreenLen+1)})
	ping(t, screen) // 上面三条都已处理

	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	if len(h.sink.viewport) != 1 || h.sink.viewport[0] != (model.Viewport{W: 1024, H: 600, DPR: 1}) {
		t.Fatalf("只应采用屏幕会话的 viewport: %+v", h.sink.viewport)
	}
	if len(h.sink.coarse) != 1 || !h.sink.coarse[0] || len(h.sink.current) != 1 || h.sink.current[0] != "screen2" {
		t.Fatalf("coarse=%v current=%v", h.sink.coarse, h.sink.current)
	}
}

func TestScreenOnlineTransitions(t *testing.T) {
	h := newScreenHarness(t)
	admin := h.dial(adminToken)
	read(t, admin)
	if got := h.sink.onlineLog(); len(got) != 0 {
		t.Fatalf("管理员连接不影响屏幕在线: %v", got)
	}

	s1 := h.dial(screenToken)
	read(t, s1)
	s2 := h.dial(screenToken)
	read(t, s2)
	if got := h.sink.onlineLog(); len(got) != 1 || !got[0] {
		t.Fatalf("第一个屏幕连入才算上线: %v", got)
	}

	_ = s1.Close(websocket.StatusNormalClosure, "bye")
	waitConns(t, h.hub, 2) // 管理员 + 剩下的屏幕
	if got := h.sink.onlineLog(); len(got) != 1 {
		t.Fatalf("还有屏幕在线，不应离线: %v", got)
	}
	_ = s2.Close(websocket.StatusNormalClosure, "bye")
	waitConns(t, h.hub, 1)
	if got := h.sink.onlineLog(); len(got) != 2 || got[1] {
		t.Fatalf("最后一个屏幕断开后应离线: %v", got)
	}
}

func TestSettingsPatchCarriesScreenDisplaySettings(t *testing.T) {
	h := newScreenHarness(t)
	screen := h.dial(screenToken)
	read(t, screen)

	v := h.set.Get()
	v.Screen = model.ScreenDisplaySettings{CarouselMode: model.CarouselHomeOnly, IdleHomeSeconds: 30, DefaultDwellSeconds: 20, InputMode: model.InputNone, UIScale: 1.5}
	h.set.set(v)
	h.hub.NotifySettings()
	h.clk.Advance(time.Second)
	p := read(t, screen)
	ss := asMap(t, p["screen_settings"])
	sc := asMap(t, ss["screen"])
	if sc["carousel_mode"] != "home_only" || sc["idle_home_seconds"] != float64(30) || sc["ui_scale"] != 1.5 || sc["input_mode"] != "none" {
		t.Fatalf("屏幕设置应带显示参数: %v", ss)
	}
}
