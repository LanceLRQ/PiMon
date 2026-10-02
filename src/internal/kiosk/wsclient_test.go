package kiosk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// fakeHub 是假的 /ws 服务：记录握手头，可拒绝握手，可向客户端推消息并收取客户端消息。
type fakeHub struct {
	srv     *httptest.Server
	rejectN atomic.Int32
	// rejectCode 为握手被拒时的状态码，0 表示 401；retryAfter 非空时作为 Retry-After 头。
	rejectCode atomic.Int32
	retryAfter atomic.Value // string
	auths      chan string
	conns      chan *websocket.Conn
	msgs       chan map[string]any
	paths      chan string
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{
		auths: make(chan string, 64),
		conns: make(chan *websocket.Conn, 64),
		msgs:  make(chan map[string]any, 256),
		paths: make(chan string, 64),
	}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.paths <- r.URL.Path
		h.auths <- r.Header.Get("Authorization")
		if h.rejectN.Load() > 0 {
			h.rejectN.Add(-1)
			code := int(h.rejectCode.Load())
			if code == 0 {
				code = http.StatusUnauthorized
			}
			if ra, _ := h.retryAfter.Load().(string); ra != "" {
				w.Header().Set("Retry-After", ra)
			}
			http.Error(w, "rejected", code)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.conns <- c
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil {
				h.msgs <- m
			}
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) nextConn(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-h.conns:
		return c
	case <-time.After(waitLimit):
		t.Fatal("等待客户端连接超时")
		return nil
	}
}

func (h *fakeHub) nextAuth(t *testing.T) string {
	t.Helper()
	select {
	case a := <-h.auths:
		return a
	case <-time.After(waitLimit):
		t.Fatal("等待握手超时")
		return ""
	}
}

func (h *fakeHub) nextMsg(t *testing.T, typ string) map[string]any {
	t.Helper()
	deadline := time.After(waitLimit)
	for {
		select {
		case m := <-h.msgs:
			if m["type"] == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("等待客户端消息 %q 超时", typ)
			return nil
		}
	}
}

func sendJSON(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("推送消息失败: %v", err)
	}
}

// linkSinkStub 记录链路回调。
type linkSinkStub struct {
	token       atomic.Value // string
	tokenErr    atomic.Value // error 或 nil
	tokenReads  atomic.Int32
	connected   chan struct{}
	settings    chan ui.ScreenSettings
	builds      chan string
	onConnected func()
	gate        chan struct{} // 非 nil 时 OnSettings 先阻塞等待它
	started     chan struct{} // OnSettings 进入时通知
}

func newSinkStub(token string) *linkSinkStub {
	s := &linkSinkStub{
		connected: make(chan struct{}, 16),
		settings:  make(chan ui.ScreenSettings, 16),
		builds:    make(chan string, 16),
	}
	s.token.Store(token)
	s.tokenErr.Store(errNil)
	return s
}

func (s *linkSinkStub) OnConnected() {
	if s.onConnected != nil {
		s.onConnected()
	}
	s.connected <- struct{}{}
}

func (s *linkSinkStub) OnSettings(v ui.ScreenSettings) {
	if s.started != nil {
		s.started <- struct{}{}
	}
	if s.gate != nil {
		<-s.gate
	}
	s.settings <- v
}

func (s *linkSinkStub) OnBuild(b string) { s.builds <- b }

func (s *linkSinkStub) ReadToken() (string, error) {
	s.tokenReads.Add(1)
	if e := s.tokenErr.Load().(error); e != errNil {
		return "", e
	}
	return s.token.Load().(string), nil
}

type wsFixture struct {
	hub    *fakeHub
	clk    *armClock
	link   *WSLink
	sink   *linkSinkStub
	states chan model.ScreenState
	hooks  atomic.Int32 // OnConnected 钩子次数
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	f := &wsFixture{hub: newFakeHub(t), clk: newArmClock(testStart), sink: newSinkStub("tok-1"), states: make(chan model.ScreenState, 16)}
	link, err := NewWSLink(WSConfig{
		HubURL:        f.hub.srv.URL,
		Clock:         f.clk,
		Log:           quietLogger(),
		OnScreenState: func(s model.ScreenState) { f.states <- s },
		OnConnected:   func() { f.hooks.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.link = link
	return f
}

func (f *wsFixture) run(t *testing.T) (stop func()) {
	t.Helper()
	return runUntilCancel(t, func(ctx context.Context) { f.link.Run(ctx, f.sink) })
}

func (f *wsFixture) waitConnected(t *testing.T) {
	t.Helper()
	select {
	case <-f.sink.connected:
	case <-time.After(waitLimit):
		t.Fatal("等待 OnConnected 超时")
	}
}

func TestWSURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:31415":   "ws://127.0.0.1:31415/ws",
		"http://127.0.0.1:31415/":  "ws://127.0.0.1:31415/ws",
		"https://pimon.lan":        "wss://pimon.lan/ws",
		"http://[::1]:31415":       "ws://[::1]:31415/ws",
		"http://h:1/prefix":        "ws://h:1/prefix/ws",
		"http://127.0.0.1:31415//": "ws://127.0.0.1:31415/ws",
	}
	for in, want := range cases {
		got, err := wsURL(in)
		if err != nil || got != want {
			t.Errorf("%s: got (%q,%v) want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://h", "127.0.0.1:31415", "://x"} {
		if _, err := wsURL(bad); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

func TestWSLink_握手带Bearer令牌并拼接ws路径(t *testing.T) {
	f := newWSFixture(t)
	f.run(t)
	if got := f.hub.nextAuth(t); got != "Bearer tok-1" {
		t.Fatalf("Authorization=%q", got)
	}
	if p := <-f.hub.paths; p != "/ws" {
		t.Fatalf("path=%q", p)
	}
	f.waitConnected(t)
	if f.hooks.Load() != 1 {
		t.Fatalf("连接钩子应触发 1 次，实际 %d", f.hooks.Load())
	}
}

func TestWSLink_握手失败按1s起翻倍退避_连上后清零(t *testing.T) {
	f := newWSFixture(t)
	f.hub.rejectCode.Store(http.StatusServiceUnavailable)
	f.hub.rejectN.Store(3)
	f.run(t)
	for i, d := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		f.hub.nextAuth(t)
		f.clk.waitArmed(t, d)
		if i < 2 {
			f.clk.Advance(d)
		}
	}
	f.clk.Advance(4 * time.Second)
	f.hub.nextAuth(t)
	conn := f.hub.nextConn(t)
	f.waitConnected(t)

	// 连上后断开：退避从 1s 重新开始
	_ = conn.CloseNow()
	f.clk.waitArmed(t, time.Second)
}

func TestWSLink_退避上限30秒(t *testing.T) {
	f := newWSFixture(t)
	f.hub.rejectCode.Store(http.StatusServiceUnavailable)
	f.hub.rejectN.Store(100)
	f.run(t)
	for _, d := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		d *= time.Second
		f.hub.nextAuth(t)
		f.clk.waitArmed(t, d)
		f.clk.Advance(d)
	}
}

func TestWSLink_401后令牌不变不再拨号_令牌变化立即重拨(t *testing.T) {
	f := newWSFixture(t)
	f.hub.rejectN.Store(1)
	f.run(t)
	if got := f.hub.nextAuth(t); got != "Bearer tok-1" {
		t.Fatal(got)
	}
	for i := 0; i < 5; i++ {
		f.clk.waitArmed(t, wsTokenPoll)
		f.clk.Advance(wsTokenPoll)
	}
	f.clk.waitArmed(t, wsTokenPoll)
	if len(f.hub.auths) != 0 {
		t.Fatalf("令牌未变不应再拨号，多出 %d 次", len(f.hub.auths))
	}
	f.sink.token.Store("tok-2")
	f.clk.Advance(wsTokenPoll)
	if got := f.hub.nextAuth(t); got != "Bearer tok-2" {
		t.Fatalf("令牌变化后应立即用新令牌重拨: %q", got)
	}
	f.hub.nextConn(t)
	f.waitConnected(t)
}

func TestWSLink_429按RetryAfter等待_缺省15分钟(t *testing.T) {
	f := newWSFixture(t)
	f.hub.rejectCode.Store(http.StatusTooManyRequests)
	f.hub.retryAfter.Store("120")
	f.hub.rejectN.Store(1)
	f.run(t)
	f.hub.nextAuth(t)
	f.clk.waitArmed(t, 120*time.Second)
	f.clk.Advance(120 * time.Second)
	f.hub.nextAuth(t) // 重拨成功
	f.hub.nextConn(t)
	f.waitConnected(t)
}

func TestWSLink_429无RetryAfter等15分钟(t *testing.T) {
	f := newWSFixture(t)
	f.hub.rejectCode.Store(http.StatusTooManyRequests)
	f.hub.rejectN.Store(1)
	f.run(t)
	f.hub.nextAuth(t)
	f.clk.waitArmed(t, 15*time.Minute)
	if len(f.hub.auths) != 0 {
		t.Fatal("等待期间不应拨号")
	}
	f.clk.Advance(15 * time.Minute)
	f.hub.nextAuth(t)
	f.hub.nextConn(t)
}

func TestWSLink_每次拨号前重读令牌(t *testing.T) {
	f := newWSFixture(t)
	f.run(t)
	if got := f.hub.nextAuth(t); got != "Bearer tok-1" {
		t.Fatal(got)
	}
	conn := f.hub.nextConn(t)
	f.waitConnected(t)

	f.sink.token.Store("tok-2") // 令牌轮换，hub 随后断开连接
	_ = conn.CloseNow()
	f.clk.waitArmed(t, time.Second)
	f.clk.Advance(time.Second)
	if got := f.hub.nextAuth(t); got != "Bearer tok-2" {
		t.Fatalf("重连应使用新令牌: %q", got)
	}
	if n := f.sink.tokenReads.Load(); n != 2 {
		t.Fatalf("应读取令牌 2 次，实际 %d", n)
	}
}

func TestWSLink_令牌读不到时退避重试且不拨号(t *testing.T) {
	f := newWSFixture(t)
	f.sink.tokenErr.Store(errTokenGone)
	f.run(t)
	f.clk.waitArmed(t, time.Second)
	if len(f.hub.auths) != 0 {
		t.Fatal("读不到令牌不应拨号")
	}
	f.sink.tokenErr.Store(errNil)
	f.clk.Advance(time.Second)
	if got := f.hub.nextAuth(t); got != "Bearer tok-1" {
		t.Fatal(got)
	}
}

var errTokenGone = errors.New("令牌文件不存在")

func TestWSLink_每30秒发ping(t *testing.T) {
	f := newWSFixture(t)
	f.run(t)
	f.hub.nextConn(t)
	f.waitConnected(t)
	for i := 0; i < 3; i++ {
		f.clk.waitArmed(t, 30*time.Second)
		f.clk.Advance(30 * time.Second)
		f.hub.nextMsg(t, "ping")
	}
}

func TestWSLink_snapshot与patch分发(t *testing.T) {
	f := newWSFixture(t)
	f.run(t)
	conn := f.hub.nextConn(t)
	f.waitConnected(t)

	sendJSON(t, conn, map[string]any{
		"type":            "snapshot",
		"build":           "b-1",
		"screen_settings": map[string]any{"language": "zh", "timezone": "Asia/Shanghai", "screen": map[string]any{"ui_scale": 1.5, "daily_restart": map[string]any{"enabled": true, "at": "04:00"}}},
		"screen_state":    map[string]any{"mode": "off", "theme_id": "ambient", "reason": "schedule"},
	})
	if b := <-f.sink.builds; b != "b-1" {
		t.Fatalf("build=%q", b)
	}
	s := <-f.sink.settings
	if s.Timezone != "Asia/Shanghai" || s.Screen.UIScale != 1.5 || !s.Screen.DailyRestart.Enabled || s.Screen.DailyRestart.At != "04:00" {
		t.Fatalf("settings=%+v", s)
	}
	if st := <-f.states; st.Mode != "off" {
		t.Fatalf("state=%+v", st)
	}

	sendJSON(t, conn, map[string]any{"type": "patch", "entity": "settings", "screen_settings": map[string]any{"timezone": "UTC", "screen": map[string]any{"ui_scale": 2}}})
	if s := <-f.sink.settings; s.Timezone != "UTC" || s.Screen.UIScale != 2 {
		t.Fatalf("patch settings=%+v", s)
	}
	sendJSON(t, conn, map[string]any{"type": "patch", "entity": "screen_state", "screen_state": map[string]any{"mode": "on", "reason": "wake"}})
	if st := <-f.states; st.Mode != "on" {
		t.Fatalf("patch state=%+v", st)
	}

	// 无关实体、pong、error 不分发
	sendJSON(t, conn, map[string]any{"type": "patch", "entity": "layout"})
	sendJSON(t, conn, map[string]any{"type": "pong"})
	sendJSON(t, conn, map[string]any{"type": "error", "error": map[string]any{"code": "ws.bad_message"}})
	sendJSON(t, conn, map[string]any{"type": "snapshot", "build": "marker"})
	if b := <-f.sink.builds; b != "marker" {
		t.Fatalf("build=%q", b)
	}
	if len(f.sink.settings) != 0 || len(f.states) != 0 {
		t.Fatal("不应有多余分发")
	}
}

func TestWSLink_读循环与回调解耦_信箱只保留最新值(t *testing.T) {
	f := newWSFixture(t)
	f.sink.gate = make(chan struct{})
	f.sink.started = make(chan struct{}, 16)
	f.run(t)
	conn := f.hub.nextConn(t)
	f.waitConnected(t)

	patch := func(tz string) {
		sendJSON(t, conn, map[string]any{"type": "patch", "entity": "settings", "screen_settings": map[string]any{"timezone": tz}})
	}
	patch("p1")
	<-f.sink.started // p1 正卡在回调里
	patch("p2")
	patch("p3")
	// 读循环没被回调拖住：p2、p3 之后的 build 标记依然能被分发。
	sendJSON(t, conn, map[string]any{"type": "snapshot", "build": "marker"})
	if b := <-f.sink.builds; b != "marker" {
		t.Fatalf("build=%q", b)
	}

	close(f.sink.gate)
	var got []string
	for len(got) < 2 {
		select {
		case s := <-f.sink.settings:
			got = append(got, s.Timezone)
		case <-time.After(waitLimit):
			t.Fatalf("等待设置分发超时，已收到 %v", got)
		}
	}
	if got[0] != "p1" || got[1] != "p3" {
		t.Fatalf("应只交付 p1 与最新的 p3，实际 %v", got)
	}
	select {
	case s := <-f.sink.settings:
		t.Fatalf("不应再有交付: %+v", s)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMailbox_只保留最新值(t *testing.T) {
	m := newMailbox[int]()
	m.put(1)
	m.put(2)
	m.put(3)
	if v, ok := m.take(); !ok || v != 3 {
		t.Fatalf("got (%d,%v)", v, ok)
	}
	if _, ok := m.take(); ok {
		t.Fatal("取空后不应再有值")
	}
}

func TestWSLink_连上后OnConnected里的上报被送出_连接前的上报被丢弃(t *testing.T) {
	f := newWSFixture(t)
	f.link.Report(model.KioskReport{Version: "early"}) // 未连接，丢弃
	f.sink.onConnected = func() { f.link.Report(model.KioskReport{Version: "late", Restarts: 2}) }
	f.run(t)
	f.hub.nextConn(t)
	m := f.hub.nextMsg(t, "kiosk_report")
	k := m["kiosk"].(map[string]any)
	if k["version"] != "late" || k["restarts"] != float64(2) {
		t.Fatalf("kiosk=%v", k)
	}
	f.waitConnected(t)
	select {
	case extra := <-f.hub.msgs:
		if extra["type"] == "kiosk_report" {
			t.Fatalf("不应有 early 上报: %v", extra)
		}
	default:
	}
}

func TestWSLink_上报只保留最新(t *testing.T) {
	f := newWSFixture(t)
	f.run(t)
	f.hub.nextConn(t)
	f.waitConnected(t)
	for i := 1; i <= 5; i++ {
		f.link.Report(model.KioskReport{Version: "v", Restarts: i})
	}
	var last float64
	deadline := time.After(waitLimit)
	for last != 5 {
		select {
		case m := <-f.hub.msgs:
			if m["type"] == "kiosk_report" {
				last = m["kiosk"].(map[string]any)["restarts"].(float64)
			}
		case <-deadline:
			t.Fatalf("最后一次上报应送达，最近一次 restarts=%v", last)
		}
	}
}

func TestWSLink_Wake发送kiosk_wake_未连接时丢弃(t *testing.T) {
	f := newWSFixture(t)
	if f.link.Wake() { // 未连接：不 panic、不阻塞，返回未投递
		t.Fatal("未连接时 Wake 应返回 false")
	}
	f.run(t)
	f.hub.nextConn(t)
	f.waitConnected(t)
	if !f.link.Wake() {
		t.Fatal("已连接时 Wake 应返回 true")
	}
	m := f.hub.nextMsg(t, "kiosk_wake")
	if _, has := m["minutes"]; has {
		t.Fatalf("不应带 minutes（用 hub 默认）: %v", m)
	}
}

func TestWSLink_取消后Run返回并断开(t *testing.T) {
	f := newWSFixture(t)
	stop := f.run(t)
	conn := f.hub.nextConn(t)
	f.waitConnected(t)
	stop()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("客户端应已断开")
	}
	f.link.Report(model.KioskReport{}) // 已断开：不阻塞
}
