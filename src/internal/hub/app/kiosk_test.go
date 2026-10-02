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

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

func (f *screenFixture) screenToken() string {
	f.t.Helper()
	raw, err := os.ReadFile(f.a.cfg.ScreenTokenPath())
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

// dialKiosk 以 Bearer 令牌握手，不带 Origin 与 Cookie（kiosk 是非浏览器客户端）。
func (f *screenFixture) dialKiosk(token string) (*websocket.Conn, *http.Response, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.base, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if conn != nil {
		f.t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func (f *screenFixture) mustKiosk() (*websocket.Conn, map[string]any) {
	f.t.Helper()
	conn, _, err := f.dialKiosk(f.screenToken())
	if err != nil {
		f.t.Fatalf("kiosk 握手: %v", err)
	}
	return conn, readWS(f.t, conn)
}

func (f *screenFixture) status() model.ScreenStatus {
	f.t.Helper()
	resp, data := call(f.t, f.admin, f.base, "GET", "/api/screen/status", nil)
	if resp.StatusCode != 200 {
		f.t.Fatalf("status = %d %s", resp.StatusCode, data)
	}
	var st model.ScreenStatus
	if err := json.Unmarshal(data, &st); err != nil {
		f.t.Fatal(err)
	}
	return st
}

func TestKioskHandshakeSucceedsWithoutOriginAndCookie(t *testing.T) {
	f := newScreenFixture(t)
	_, snap := f.mustKiosk()
	if snap["role"] != ui.RoleKiosk || snap["screen_settings"] == nil || snap["screen_state"] == nil {
		t.Fatalf("kiosk snapshot = %v", snap)
	}
	if snap["resolved_layout"] != nil || snap["screen_data"] != nil || snap["settings"] != nil {
		t.Fatalf("kiosk 不应收到布局、数据或完整设置: %v", snap)
	}
	if topics, _ := snap["topics"].([]any); len(topics) != 2 {
		t.Fatalf("topics = %v", snap["topics"])
	}
	ss := snap["screen_settings"].(map[string]any)["screen"].(map[string]any)
	if dr, _ := ss["daily_restart"].(map[string]any); dr == nil || dr["enabled"] != false || dr["at"] != "04:00" {
		t.Fatalf("screen_settings 应含 daily_restart 默认值: %v", ss)
	}
}

func TestKioskHandshakeRejectsBadToken(t *testing.T) {
	f := newScreenFixture(t)
	for _, tok := range []string{"wrong", ""} {
		_, resp, err := f.dialKiosk(tok)
		if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("令牌 %q 应 401: err=%v resp=%v", tok, err, resp)
		}
	}
}

func TestKioskHandshakeRateLimitedAfterRepeatedFailures(t *testing.T) {
	f := newScreenFixture(t)
	good := f.screenToken()
	for i := 0; i < loginMaxFailures-1; i++ {
		if _, resp, _ := f.dialKiosk("wrong"); resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次应 401: %v", i+1, resp)
		}
	}
	if _, resp, _ := f.dialKiosk("wrong"); resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("达到上限应 429: %v", resp)
	}
	// 与 /screen/auth 共用同一限流键：锁定期内正确令牌也被拒。
	if _, resp, _ := f.dialKiosk(good); resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("锁定期内应 429: %v", resp)
	}
	sc := newClient()
	sc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if resp, _ := call(t, sc, f.base, "GET", "/screen/auth?token="+good, nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("/screen/auth 同键锁定: %d", resp.StatusCode)
	}
	f.clk.Advance(loginLockTime + time.Second)
	if conn, _, err := f.dialKiosk(good); err != nil {
		t.Fatalf("锁定到期后应可连接: %v", err)
	} else {
		readWS(t, conn)
	}
}

func TestKioskBearerDoesNotWriteSession(t *testing.T) {
	f := newScreenFixture(t)
	count := func() int {
		var n int
		if err := f.a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	f.mustKiosk()
	if after := count(); after != before {
		t.Fatalf("kiosk 握手不应写 sessions: %d → %d", before, after)
	}
}

func TestKioskDoesNotCountAsScreenOnlineNorReceiveControl(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	if on, seen := f.a.screenState.Online(); on || seen != nil {
		t.Fatalf("kiosk 不应让屏幕在线: %v %v", on, seen)
	}

	// 没有真屏幕：指令未送达（kiosk 不吞）。
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "refresh"}); resp.StatusCode != 200 {
		t.Fatalf("control = %d %s", resp.StatusCode, data)
	}
	if ops := f.screenOps(); len(ops) != 1 || ops[0].Delivered {
		t.Fatalf("只有 kiosk 在线时指令应保持未送达: %+v", ops)
	}

	screen, _ := f.dial(f.screen)
	eventually(t, "屏幕上线", func() bool { on, _ := f.a.screenState.Online(); return on })
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "switch", ScreenID: "index"}); resp.StatusCode != 200 {
		t.Fatalf("control = %d %s", resp.StatusCode, data)
	}
	if m := readWS(t, screen); m["type"] != "screen_control" {
		t.Fatalf("屏幕应收到指令: %v", m)
	}
	if ops := f.screenOps(); len(ops) != 2 || !ops[0].Delivered {
		t.Fatalf("指令应标记送达: %+v", ops)
	}
	// kiosk 之后收到的第一条消息必须是 pong，而不是 screen_control。
	f.sendRaw(kiosk, `{"type":"ping"}`)
	if m := readWS(t, kiosk); m["type"] != "pong" {
		t.Fatalf("kiosk 不应收到 screen_control: %v", m)
	}

	// 屏幕断开：在线以真屏幕为准，与 kiosk 无关。
	_ = screen.Close(websocket.StatusNormalClosure, "bye")
	eventually(t, "屏幕离线", func() bool { on, _ := f.a.screenState.Online(); return !on })
}

func (f *screenFixture) sendRaw(conn *websocket.Conn, raw string) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		f.t.Fatal(err)
	}
}

func TestKioskSubscribeRestrictedToSettingsAndScreenState(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	f.sendRaw(kiosk, `{"type":"subscribe","topics":["layout"]}`)
	m := readWS(t, kiosk)
	if m["type"] != "error" || m["error"].(map[string]any)["code"] != ui.ErrSubscribeDenied {
		t.Fatalf("kiosk 订阅 layout 应被拒: %v", m)
	}
	f.sendRaw(kiosk, `{"type":"subscribe","topics":["screen_state"]}`)
	if m := readWS(t, kiosk); m["type"] != "snapshot" {
		t.Fatalf("订阅 screen_state 应重发 snapshot: %v", m)
	}
}

func TestKioskTokenRotationDisconnectsKiosk(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/token/reset", nil); resp.StatusCode >= 300 {
		t.Fatalf("reset = %d %s", resp.StatusCode, data)
	}
	expectRevoked(t, kiosk)
	if _, resp, _ := f.dialKiosk("stale"); resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("旧令牌应 401: %v", resp)
	}
	if conn, _, err := f.dialKiosk(f.screenToken()); err != nil {
		t.Fatalf("新令牌应可连接: %v", err)
	} else {
		readWS(t, conn)
	}
}

func TestKioskPeriodicRecheckDisconnectsAfterRotation(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	// 绕过吊销回调，直接改库内令牌，只能靠周期复核发现。
	if _, err := f.a.db.ExecContext(context.Background(), `UPDATE screen_tokens SET token_hash = 'x' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(16 * time.Second)
	expectRevoked(t, kiosk)
}

func TestKioskReportStoredAndOfflineKeepsContent(t *testing.T) {
	f := newScreenFixture(t)
	if st := f.status(); st.Kiosk != nil {
		t.Fatalf("从未连过应为 null: %+v", st.Kiosk)
	}
	kiosk, _ := f.mustKiosk()
	eventually(t, "kiosk 在线", func() bool { k := f.status().Kiosk; return k != nil && k.Online })
	touch := true
	rss := int64(4096)
	started := f.clk.Now().Add(-time.Minute).UTC()
	f.send(kiosk, ui.ClientMessage{Type: ui.TypeKioskReport, Kiosk: &model.KioskReport{
		Version: "1.0.0", ChromiumStartedAt: &started, Restarts: 3, Touchscreen: &touch, ChromiumRSSBytes: &rss,
		IdleCheck: &model.KioskIdleCheck{Greeter: true, CheckedAt: f.clk.Now().UTC()},
	}})
	eventually(t, "上报入库", func() bool { k := f.status().Kiosk; return k != nil && k.Version == "1.0.0" })
	k := f.status().Kiosk
	if !k.Online || k.Restarts != 3 || k.Touchscreen == nil || !*k.Touchscreen || k.ChromiumRSSBytes == nil ||
		*k.ChromiumRSSBytes != 4096 || k.IdleCheck == nil || !k.IdleCheck.Greeter || k.LastReportAt == nil {
		t.Fatalf("kiosk 状态 = %+v", k)
	}
	_ = kiosk.Close(websocket.StatusNormalClosure, "bye")
	eventually(t, "kiosk 离线", func() bool { k := f.status().Kiosk; return k != nil && !k.Online })
	if k := f.status().Kiosk; k.Version != "1.0.0" || k.Restarts != 3 || k.IdleCheck == nil {
		t.Fatalf("离线后应保留最后上报: %+v", k)
	}
}

func TestKioskWakeTurnsScreenOn(t *testing.T) {
	f := newScreenFixture(t)
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "off"}); resp.StatusCode != 200 {
		t.Fatalf("off = %d %s", resp.StatusCode, data)
	}
	if st := f.a.screenState.State(); st.Mode != model.ScreenModeOff {
		t.Fatalf("应已关屏: %+v", st)
	}
	kiosk, _ := f.mustKiosk()
	f.send(kiosk, ui.ClientMessage{Type: ui.TypeKioskWake})
	m := readWS(t, kiosk)
	if m["entity"] != "screen_state" || m["screen_state"].(map[string]any)["mode"] != "on" ||
		m["screen_state"].(map[string]any)["reason"] != "wake" {
		t.Fatalf("唤醒后应推送亮屏状态: %v", m)
	}
	ops := f.screenOps()
	if len(ops) == 0 || ops[0].Action != "wake" || ops[0].ClientIP != "kiosk" || ops[0].Params["minutes"] != float64(30) {
		t.Fatalf("操作记录: %+v", ops)
	}
}

func TestKioskWakeRejectsOutOfRangeMinutes(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	f.send(kiosk, ui.ClientMessage{Type: ui.TypeKioskWake, Minutes: 100000})
	m := readWS(t, kiosk)
	if m["type"] != "error" || m["error"].(map[string]any)["code"] != ui.ErrBadMessage {
		t.Fatalf("越界分钟数应报协议错误: %v", m)
	}
}

func TestKioskMessagesFromNonKioskAreProtocolErrors(t *testing.T) {
	f := newScreenFixture(t)
	for name, c := range map[string]*http.Client{"admin": f.admin, "screen": f.screen} {
		conn, _ := f.dial(c)
		for _, typ := range []ui.ClientMessageType{ui.TypeKioskReport, ui.TypeKioskWake} {
			f.send(conn, ui.ClientMessage{Type: typ, Kiosk: &model.KioskReport{Version: "x"}})
			m := readWS(t, conn)
			if m["type"] != "error" || m["error"].(map[string]any)["code"] != ui.ErrBadMessage {
				t.Fatalf("%s 发 %s 应报协议错误: %v", name, typ, m)
			}
		}
	}
	if k := f.status().Kiosk; k != nil {
		t.Fatalf("非 kiosk 的上报不应生效: %+v", k)
	}
	if st := f.a.screenState.State(); st.Reason == model.ScreenReasonWake {
		t.Fatal("非 kiosk 的 kiosk_wake 不应执行唤醒")
	}
}

func TestKioskReportWithoutPayloadIsProtocolError(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	f.sendRaw(kiosk, `{"type":"kiosk_report"}`)
	if m := readWS(t, kiosk); m["type"] != "error" {
		t.Fatalf("缺少载荷应报协议错误: %v", m)
	}
}

func TestDailyRestartSettingsAndNextRestart(t *testing.T) {
	f := newScreenFixture(t)
	resp, data := call(t, f.admin, f.base, "GET", "/api/settings", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET settings = %d", resp.StatusCode)
	}
	var s model.Settings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Screen.DailyRestart.Enabled || s.Screen.DailyRestart.At != "04:00" {
		t.Fatalf("默认值 = %+v", s.Screen.DailyRestart)
	}
	s.Screen.DailyRestart.At = "25:00"
	if resp, _ := call(t, f.admin, f.base, "PUT", "/api/settings", s); resp.StatusCode != 400 {
		t.Fatalf("非法时刻应 400: %d", resp.StatusCode)
	}

	f.mustKiosk()
	eventually(t, "kiosk 在线", func() bool { k := f.status().Kiosk; return k != nil && k.Online })
	if k := f.status().Kiosk; k.NextRestart != nil {
		t.Fatalf("未开启应为 null: %v", k.NextRestart)
	}
	s.Screen.DailyRestart = model.DailyRestartSettings{Enabled: true, At: "03:30"}
	if resp, data := call(t, f.admin, f.base, "PUT", "/api/settings", s); resp.StatusCode != 200 {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	k := f.status().Kiosk
	want := time.Date(2026, 1, 1, 3, 30, 0, 0, time.UTC) // 假时钟 2026-01-01 00:00，时区 UTC
	if k.NextRestart == nil || !k.NextRestart.Equal(want) {
		t.Fatalf("next_restart = %v，期望 %v", k.NextRestart, want)
	}
}

func TestDailyRestartPushedToKioskWithSettingsPatch(t *testing.T) {
	f := newScreenFixture(t)
	kiosk, _ := f.mustKiosk()
	_, data := call(t, f.admin, f.base, "GET", "/api/settings", nil)
	var s model.Settings
	_ = json.Unmarshal(data, &s)
	s.Screen.DailyRestart = model.DailyRestartSettings{Enabled: true, At: "05:00"}
	if resp, data := call(t, f.admin, f.base, "PUT", "/api/settings", s); resp.StatusCode != 200 {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	f.clk.Advance(2 * time.Second) // 合并窗口
	m := readWS(t, kiosk)
	dr := m["screen_settings"].(map[string]any)["screen"].(map[string]any)["daily_restart"].(map[string]any)
	if m["entity"] != "settings" || dr["enabled"] != true || dr["at"] != "05:00" {
		t.Fatalf("kiosk 应收到设置 patch: %v", m)
	}
}

func TestScreenAuthKeepsLatest20ScreenSessions(t *testing.T) {
	f := newScreenFixture(t) // 已有 1 条
	admins := func() int { return f.countSessions("admin") }
	adminBefore := admins()
	var tokens []string
	for i := 0; i < 24; i++ {
		f.clk.Advance(time.Second) // 创建时间不同
		sc := newClient()
		sc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, _ := call(t, sc, f.base, "GET", "/screen/auth?token="+f.screenToken(), nil)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("screen/auth = %d", resp.StatusCode)
		}
		tokens = append(tokens, cookieOf(t, sc, f.base))
	}
	if n := f.countSessions("screen"); n != 20 {
		t.Fatalf("屏幕会话应保留 20 条，实际 %d", n)
	}
	if admins() != adminBefore {
		t.Fatal("管理员会话不应受影响")
	}
	for i, tok := range tokens {
		_, ok, err := f.a.sessions.Lookup(context.Background(), tok)
		if err != nil {
			t.Fatal(err)
		}
		if want := i >= 4; ok != want { // 24 条新建 + 初始 1 条，只留最新 20 条 = 最后 20 条新建
			t.Fatalf("第 %d 次建的会话 ok=%v 期望 %v", i, ok, want)
		}
	}
}

func (f *screenFixture) countSessions(kind string) int {
	f.t.Helper()
	var n int
	if err := f.a.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions WHERE kind = ?`, kind).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func cookieOf(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	u, _ := url.Parse(base)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "pimon_session" {
			return ck.Value
		}
	}
	t.Fatal("没有会话 Cookie")
	return ""
}
