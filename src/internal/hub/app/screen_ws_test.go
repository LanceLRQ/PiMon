package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// screenFixture 是已完成首次设置、带管理员与屏幕会话的中枢。
type screenFixture struct {
	t      *testing.T
	a      *App
	clk    *clock.Fake
	base   string
	admin  *http.Client
	screen *http.Client
}

func newScreenFixture(t *testing.T) *screenFixture {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	a := openApp(t, testConfig(t), WithClock(clk))
	admin, base := setupAdmin(t, a)
	if err := a.screen.EnsureExists(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.cfg.ScreenTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	sc := newClient()
	sc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if resp, _ := call(t, sc, base, "GET", "/screen/auth?token="+strings.TrimSpace(string(raw)), nil); resp.StatusCode != http.StatusFound {
		t.Fatalf("screen/auth = %d", resp.StatusCode)
	}
	return &screenFixture{t: t, a: a, clk: clk, base: base, admin: admin, screen: sc}
}

// dial 握手并返回连接与 snapshot。
func (f *screenFixture) dial(c *http.Client) (*websocket.Conn, map[string]any) {
	f.t.Helper()
	u, _ := url.Parse(f.base)
	hdr := http.Header{"Origin": {f.base}}
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			hdr.Set("Cookie", ck.String())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.base, "http")+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		f.t.Fatalf("握手: %v", err)
	}
	f.t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, readWS(f.t, conn)
}

func (f *screenFixture) send(conn *websocket.Conn, msg ui.ClientMessage) {
	f.t.Helper()
	b, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		f.t.Fatal(err)
	}
}

func (f *screenFixture) putLayout(base int, l model.Layout) model.LayoutState {
	f.t.Helper()
	resp, data := call(f.t, f.admin, f.base, "PUT", "/api/screens", model.LayoutSaveRequest{BaseVersion: base, Layout: l})
	if resp.StatusCode != 200 {
		f.t.Fatalf("PUT /api/screens = %d %s", resp.StatusCode, data)
	}
	var st model.LayoutState
	if err := json.Unmarshal(data, &st); err != nil {
		f.t.Fatal(err)
	}
	return st
}

func emptyLayout(cols, rows int) model.Layout {
	return model.Layout{Grid: model.Grid{Cols: cols, Rows: rows}, Screens: []model.LayoutScreen{
		{ID: "index", Name: "首页", InRotation: true, Widgets: []model.LayoutWidget{}},
	}}
}

func (f *screenFixture) screenOps() []model.ScreenOp {
	f.t.Helper()
	_, data := call(f.t, f.admin, f.base, "GET", "/api/screen/ops", nil)
	var ops []model.ScreenOp
	if err := json.Unmarshal(data, &ops); err != nil {
		f.t.Fatal(err)
	}
	return ops
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLayoutSaveReachesAdminAndScreenSessions(t *testing.T) {
	f := newScreenFixture(t)
	admin, _ := f.dial(f.admin)
	screen, snap := f.dial(f.screen)
	if snap["role"] != "screen" || snap["resolved_layout"] == nil || snap["screen_state"] == nil {
		t.Fatalf("屏幕 snapshot 缺少布局或状态: %v", snap)
	}

	f.putLayout(1, emptyLayout(8, 5)) // 库里已有种子 v1；不推进时钟：立即推送

	a := readWS(t, admin)
	if a["entity"] != "layout" || a["layout"].(map[string]any)["version"] != float64(2) || a["resolved_layout"] != nil {
		t.Fatalf("管理员应收到原始布局: %v", a)
	}
	s := readWS(t, screen)
	if s["entity"] != "layout" || s["resolved_layout"].(map[string]any)["version"] != float64(2) || s["layout"] != nil {
		t.Fatalf("屏幕应收到解析后布局: %v", s)
	}
}

func TestScreenControlDeliveredAndMarked(t *testing.T) {
	f := newScreenFixture(t)

	// 没有屏幕在线：指令未送达。
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "refresh"}); resp.StatusCode != 200 {
		t.Fatalf("control = %d %s", resp.StatusCode, data)
	}
	if ops := f.screenOps(); len(ops) != 1 || ops[0].Delivered {
		t.Fatalf("无屏幕在线时应保持未送达: %+v", ops)
	}

	screen, _ := f.dial(f.screen)
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "switch", ScreenID: "index"}); resp.StatusCode != 200 {
		t.Fatalf("control = %d %s", resp.StatusCode, data)
	}
	m := readWS(t, screen)
	if m["type"] != "screen_control" || m["action"] != "switch" || m["screen_id"] != "index" {
		t.Fatalf("屏幕应收到指令: %v", m)
	}
	if ops := f.screenOps(); len(ops) != 2 || !ops[0].Delivered || ops[1].Delivered {
		t.Fatalf("送达后 delivered 置 1: %+v", ops)
	}

	// 开屏、关屏随 screen_state 下发，不走指令。
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "off"}); resp.StatusCode != 200 {
		t.Fatalf("control off = %d %s", resp.StatusCode, data)
	}
	if p := readWS(t, screen); p["entity"] != "screen_state" || p["screen_state"].(map[string]any)["mode"] != "off" {
		t.Fatalf("应收到 screen_state: %v", p)
	}
}

func TestHubSelfScreenOnlineFollowsScreenSessions(t *testing.T) {
	f := newScreenFixture(t)
	src, _ := runtime.Builtin("hub-self")
	collect := func() string {
		rep, err := src.Collect(context.Background(), runtime.Input{Clock: f.clk})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Find("hub.screens_online").Text
	}
	if got := collect(); got != "offline" {
		t.Fatalf("没有屏幕连接应为 offline: %q", got)
	}
	screen, _ := f.dial(f.screen)
	eventually(t, "屏幕上线", func() bool { return collect() == "online" })
	_ = screen.Close(websocket.StatusNormalClosure, "bye")
	eventually(t, "屏幕离线", func() bool { return collect() == "offline" })
	if on, seen := f.a.screenState.Online(); on || seen == nil {
		t.Fatalf("应记录最近在线时间: %v %v", on, seen)
	}
}

func TestFirstViewportAdoptionAutoSelectsGridOnSeedLayout(t *testing.T) {
	f := newScreenFixture(t)
	// 启动时已写入种子 v1（8x5），并注入了三份真种子。
	screen, _ := f.dial(f.screen)

	f.send(screen, ui.ClientMessage{Type: ui.TypeViewportReport, Viewport: &model.Viewport{W: 1280, H: 720, DPR: 1}})
	eventually(t, "viewport 稳定 2 秒后自动换网格", func() bool {
		f.clk.Advance(time.Second)
		st, err := f.a.screens.Current(context.Background())
		return err == nil && st.Version == 2
	})
	st, _ := f.a.screens.Current(context.Background())
	if st.Source != model.LayoutSourceAuto || st.Layout.Grid != (model.Grid{Cols: 10, Rows: 6}) {
		t.Fatalf("当前布局 = %+v", st)
	}
	// 屏幕会话收到换网格后的解析后布局（之前可能先收到同版本网格的展示状态重推）。
	for {
		m := readWS(t, screen)
		if m["entity"] != "layout" {
			continue
		}
		rl := m["resolved_layout"].(map[string]any)
		if rl["grid"].(map[string]any)["cols"] == float64(10) {
			if rl["version"] != float64(2) {
				t.Fatalf("version = %v", rl["version"])
			}
			break
		}
	}
}

// 全新库启动后种子实例与 8x5 种子布局齐全，再次启动不重复创建；
// 种子函数已接入：按显示器自动选择网格能换成 6x4。
func TestSeedOnFreshDatabaseAndSecondOpen(t *testing.T) {
	cfg := testConfig(t)
	a := openApp(t, cfg)
	ctx := context.Background()

	list, err := a.instances.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plugins := map[string]int{}
	for _, in := range list {
		plugins[in.PluginID]++
	}
	for _, id := range []string{"core", "weather", "host-metrics", "net-reach"} {
		if plugins[id] != 1 {
			t.Fatalf("种子实例 %s 数量 = %d，实例: %v", id, plugins[id], plugins)
		}
	}
	st, err := a.screens.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 1 || st.Source != model.LayoutSourceSeed || st.Layout.Grid != (model.Grid{Cols: 8, Rows: 5}) || len(st.Broken) != 0 {
		t.Fatalf("v1 种子布局不符: %+v", st)
	}

	applied, err := a.screens.AutoSelectGrid(ctx, model.Viewport{W: 800, H: 480, DPR: 1})
	if err != nil || !applied {
		t.Fatalf("种子函数应已注入，自动选网格 applied=%v err=%v", applied, err)
	}
	if st, _ = a.screens.Current(ctx); st.Layout.Grid != (model.Grid{Cols: 6, Rows: 4}) || len(st.Broken) != 0 {
		t.Fatalf("应换成 6x4: %+v", st)
	}
	_ = a.Close()

	b := openApp(t, cfg)
	list2, _ := b.instances.List(ctx)
	if len(list2) != len(list) {
		t.Fatalf("第二次启动不应重复创建实例: %d -> %d", len(list), len(list2))
	}
	if st2, _ := b.screens.Current(ctx); st2.Version != 2 {
		t.Fatalf("第二次启动不应再写布局: %+v", st2)
	}
}
