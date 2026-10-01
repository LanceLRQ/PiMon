package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

const (
	adminToken  = "admin-token"
	screenToken = "screen-token"
	waitTimeout = 5 * time.Second
)

type fakeInstances struct {
	mu   sync.Mutex
	byID map[string]model.Instance
}

func newFakeInstances(list ...model.Instance) *fakeInstances {
	f := &fakeInstances{byID: map[string]model.Instance{}}
	for _, in := range list {
		f.byID[in.ID] = in
	}
	return f
}

func (f *fakeInstances) set(in model.Instance) {
	f.mu.Lock()
	f.byID[in.ID] = in
	f.mu.Unlock()
}

func (f *fakeInstances) remove(id string) {
	f.mu.Lock()
	delete(f.byID, id)
	f.mu.Unlock()
}

func (f *fakeInstances) List(context.Context) ([]model.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.Instance{}
	for _, in := range f.byID {
		out = append(out, in)
	}
	return out, nil
}

func (f *fakeInstances) View(_ context.Context, id string) (model.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in, ok := f.byID[id]
	if !ok {
		return model.Instance{}, instances.ErrNotFound
	}
	return in, nil
}

type fakeSettings struct {
	mu sync.Mutex
	v  model.Settings
}

func (f *fakeSettings) Get() model.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.v
}

func (f *fakeSettings) set(v model.Settings) {
	f.mu.Lock()
	f.v = v
	f.mu.Unlock()
}

type fakeSessions struct {
	mu sync.Mutex
	m  map[string]auth.SessionKind
}

func (f *fakeSessions) Lookup(_ context.Context, token string) (auth.SessionKind, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.m[token]
	return k, ok, nil
}

func (f *fakeSessions) drop(token string) {
	f.mu.Lock()
	delete(f.m, token)
	f.mu.Unlock()
}

type harness struct {
	t    *testing.T
	clk  *clock.Fake
	hub  *Hub
	inst *fakeInstances
	set  *fakeSettings
	srv  *httptest.Server
	sess *fakeSessions
}

func newHarness(t *testing.T, queue int, list ...model.Instance) *harness {
	t.Helper()
	return newHarnessWith(t, queue, nil, list...)
}

// newHarnessWith 可以换用真实的会话服务（mkSessions 非 nil 时，以测试的假时钟构造）。
func newHarnessWith(t *testing.T, queue int, mkSessions func(clock.Clock) SessionLookup, list ...model.Instance) *harness {
	t.Helper()
	h := &harness{
		sess: &fakeSessions{m: map[string]auth.SessionKind{adminToken: auth.KindAdmin, screenToken: auth.KindScreen}},
		t:    t,
		clk:  clock.NewFake(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)),
		inst: newFakeInstances(list...),
		set:  &fakeSettings{v: model.Settings{Language: "zh", Timezone: "Asia/Shanghai", AccessURL: "http://pi.lan", ReduceEffects: true}},
	}
	var sessions SessionLookup = h.sess
	if mkSessions != nil {
		sessions = mkSessions(h.clk)
	}
	h.hub = New(Config{
		Clock: h.clk, Build: "test-build", Instances: h.inst, Settings: h.set,
		Sessions: sessions, QueueSize: queue,
	})
	mw := httpx.WithRequestInfo(func() []netip.Prefix { return nil })
	h.srv = httptest.NewServer(mw(h.hub.Handler()))
	t.Cleanup(func() {
		h.hub.Close()
		h.srv.Close()
	})
	return h
}

func (h *harness) origin() string { return h.srv.URL }

func (h *harness) wsURL() string { return "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/ws" }

// dialAs 以给定 Cookie 与 Origin 握手；token 为空表示不带 Cookie，origin 为空表示不带 Origin 头。
func (h *harness) dialAs(token, origin string) (*websocket.Conn, *http.Response, error) {
	hdr := http.Header{}
	if token != "" {
		hdr.Set("Cookie", auth.CookieName+"="+token)
	}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	return websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{HTTPHeader: hdr})
}

func (h *harness) dial(token string) *websocket.Conn {
	h.t.Helper()
	c, _, err := h.dialAs(token, h.origin())
	if err != nil {
		h.t.Fatalf("握手失败: %v", err)
	}
	c.SetReadLimit(1 << 20)
	h.t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func read(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("消息不是 JSON: %v: %s", err, b)
	}
	return m
}

func send(t *testing.T, c *websocket.Conn, msg ui.ClientMessage) {
	t.Helper()
	b, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
}

// ping 发 ping 并读回下一条消息，它必须是 pong：用来确认此前没有多余的 patch 在路上。
func ping(t *testing.T, c *websocket.Conn) {
	t.Helper()
	send(t, c, ui.ClientMessage{Type: ui.TypePing})
	if m := read(t, c); m["type"] != string(ui.TypePong) {
		t.Fatalf("期望 pong，得到 %v", m)
	}
}

// closedSoon 在后台读取直到连接结束（同时让底层库处理服务端的关闭帧），结果经返回的 channel 给出。
func closedSoon(c *websocket.Conn) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		_, _, err := c.Read(ctx)
		if err == nil {
			err = errors.New("读到了意外的消息")
		} else if ctx.Err() != nil {
			err = errors.New("等待关闭超时")
		} else {
			err = nil
		}
		ch <- err
	}()
	return ch
}

func expectClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	expectClosedWith(t, c, -1)
}

// expectClosedWith 要求连接被服务端关闭；status 不为 -1 时还要求关闭码一致。
func expectClosedWith(t *testing.T, c *websocket.Conn, status websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("连接应已被服务端关闭")
	}
	if ctx.Err() != nil {
		t.Fatalf("等待关闭超时: %v", err)
	}
	if status != -1 && websocket.CloseStatus(err) != status {
		t.Fatalf("关闭码 = %v，期望 %v（%v）", websocket.CloseStatus(err), status, err)
	}
}

func inst(id, summary string) model.Instance {
	return model.Instance{ID: id, PluginID: "p", Name: id, Summary: summary, DisplayState: "ok"}
}

func TestHandshakeRejectsWithoutSession(t *testing.T) {
	h := newHarness(t, 0)
	_, resp, err := h.dialAs("", h.origin())
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 resp=%v err=%v", resp, err)
	}
	_, resp, err = h.dialAs("bogus", h.origin())
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无效会话应 401，得到 resp=%v err=%v", resp, err)
	}
}

func TestHandshakeRejectsBadOrigin(t *testing.T) {
	h := newHarness(t, 0)
	for _, origin := range []string{"http://evil.example", ""} {
		_, resp, err := h.dialAs(adminToken, origin)
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Origin=%q 应 403，得到 resp=%v err=%v", origin, resp, err)
		}
	}
}

func TestSnapshotIsFirstMessage(t *testing.T) {
	h := newHarness(t, 0, inst("a", "one"), inst("b", "two"))
	c := h.dial(adminToken)
	m := read(t, c)
	if m["type"] != string(ui.TypeSnapshot) || m["build"] != "test-build" || m["role"] != ui.RoleAdmin {
		t.Fatalf("首条消息应是 admin snapshot: %v", m)
	}
	if len(m["instances"].([]any)) != 2 {
		t.Fatalf("snapshot 应含两个实例: %v", m["instances"])
	}
	if m["settings"] == nil || m["screen_settings"] != nil {
		t.Fatalf("管理员应拿到完整设置: %v", m)
	}
	st, err := time.Parse(time.RFC3339Nano, m["server_time"].(string))
	if err != nil || !st.Equal(h.clk.Now()) {
		t.Fatalf("server_time 应取自注入时钟: %v %v", m["server_time"], err)
	}
}

func TestScreenSessionSnapshotAndSubscribeDenied(t *testing.T) {
	h := newHarness(t, 0, inst("a", "one"))
	c := h.dial(screenToken)
	m := read(t, c)
	if m["role"] != ui.RoleScreen || len(m["instances"].([]any)) != 0 {
		t.Fatalf("屏幕会话 snapshot 不应含实例: %v", m)
	}
	if m["settings"] != nil {
		t.Fatalf("屏幕会话不应拿到完整设置: %v", m)
	}
	ss := m["screen_settings"].(map[string]any)
	if ss["language"] != "zh" || ss["reduce_effects"] != true {
		t.Fatalf("屏幕设置不对: %v", ss)
	}
	if _, leaked := ss["access_url"]; leaked {
		t.Fatalf("屏幕设置泄露了不需要的字段: %v", ss)
	}

	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicInstances}})
	e := read(t, c)
	if e["type"] != string(ui.TypeError) || e["error"].(map[string]any)["code"] != ui.ErrSubscribeDenied {
		t.Fatalf("订阅实例应收到协议级错误: %v", e)
	}
	ping(t, c) // 连接未断开
}

func TestInstanceChangesCoalesceIntoOnePatch(t *testing.T) {
	h := newHarness(t, 0, inst("a", "v0"))
	c := h.dial(adminToken)
	read(t, c)

	for _, s := range []string{"v1", "v2", "v3"} {
		h.inst.set(inst("a", s))
		h.hub.NotifyInstance("a")
	}
	ping(t, c) // 1 秒窗口未到：没有 patch 先于 pong 到达

	h.clk.Advance(time.Second)
	p := read(t, c)
	if p["type"] != string(ui.TypePatch) || p["entity"] != ui.EntityInstanceState {
		t.Fatalf("应收到 instance_state patch: %v", p)
	}
	if got := p["instance"].(map[string]any)["summary"]; got != "v3" {
		t.Fatalf("patch 应带合并后的最新状态，得到 %v", got)
	}
	ping(t, c) // 合并成一个：之后没有第二个 patch
}

func TestInstanceRemovedAndNewPatch(t *testing.T) {
	h := newHarness(t, 0, inst("a", "x"))
	c := h.dial(adminToken)
	read(t, c)

	h.inst.remove("a")
	h.hub.NotifyInstance("a")
	h.inst.set(inst("b", "new"))
	h.hub.NotifyInstance("b")
	h.clk.Advance(time.Second)

	got := map[string]map[string]any{}
	for i := 0; i < 2; i++ {
		p := read(t, c)
		got[p["entity"].(string)] = p
	}
	if got[ui.EntityInstanceRemoved]["id"] != "a" {
		t.Fatalf("应收到 a 的 instance_removed: %v", got)
	}
	if got[ui.EntityInstanceState]["instance"].(map[string]any)["id"] != "b" {
		t.Fatalf("新建实例应作为 instance_state: %v", got)
	}
}

func TestSettingsPatchPerRole(t *testing.T) {
	h := newHarness(t, 0)
	admin, screen := h.dial(adminToken), h.dial(screenToken)
	read(t, admin)
	read(t, screen)

	h.set.set(model.Settings{Language: "en", Timezone: "UTC", AccessURL: "http://secret.lan"})
	h.hub.NotifySettings()
	h.clk.Advance(time.Second)

	a := read(t, admin)
	if a["entity"] != ui.EntitySettings || a["settings"].(map[string]any)["access_url"] != "http://secret.lan" {
		t.Fatalf("管理员应收到完整设置 patch: %v", a)
	}
	s := read(t, screen)
	if s["entity"] != ui.EntitySettings || s["settings"] != nil {
		t.Fatalf("屏幕会话不应拿到完整设置: %v", s)
	}
	if s["screen_settings"].(map[string]any)["language"] != "en" {
		t.Fatalf("屏幕设置 patch 不对: %v", s)
	}
}

func TestSubscribeNarrowsTopicsAndResendsSnapshot(t *testing.T) {
	h := newHarness(t, 0, inst("a", "x"))
	c := h.dial(adminToken)
	read(t, c)

	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicSettings}})
	m := read(t, c)
	if m["type"] != string(ui.TypeSnapshot) || len(m["instances"].([]any)) != 0 || len(m["topics"].([]any)) != 1 {
		t.Fatalf("仅订阅 settings 的 snapshot 不应含实例: %v", m)
	}
	h.inst.set(inst("a", "y"))
	h.hub.NotifyInstance("a")
	h.clk.Advance(time.Second)
	ping(t, c) // 未订阅实例：没有 patch
}

func TestBadMessageGetsErrorKeepsConnection(t *testing.T) {
	h := newHarness(t, 0)
	c := h.dial(adminToken)
	read(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"nope"}`)); err != nil {
		t.Fatal(err)
	}
	if e := read(t, c); e["error"].(map[string]any)["code"] != ui.ErrBadMessage {
		t.Fatalf("未知 type 应回 ws.bad_message: %v", e)
	}
	ping(t, c)
}

func TestIdleConnectionDropped(t *testing.T) {
	h := newHarness(t, 0)
	c := h.dial(adminToken)
	read(t, c)
	h.clk.Advance(59 * time.Second)
	ping(t, c) // 任何消息都让空闲计时重新开始
	h.clk.Advance(2 * time.Second)
	ping(t, c) // 距上次消息仅 2 秒，仍然存活

	h.clk.Advance(59 * time.Second)
	if h.hub.connCount() != 1 {
		t.Fatal("59 秒内不应断开")
	}
	h.clk.Advance(2 * time.Second)
	expectClosed(t, c)
	waitConns(t, h.hub, 0)
}

func TestSlowConnectionDroppedWithoutAffectingOthers(t *testing.T) {
	h := newHarness(t, 2, inst("a", "v0"))
	fast := h.dial(adminToken)
	read(t, fast)

	slow := newBlockingSink()
	sc, err := h.hub.attach(context.Background(), slow, auth.KindAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	slow.waitFirstWrite(t) // 写线程卡在 snapshot 上，队列从此只进不出
	waitConns(t, h.hub, 2)

	for i := 1; i <= 5; i++ {
		h.inst.set(inst("a", "v"+string(rune('0'+i))))
		h.hub.NotifyInstance("a")
		h.clk.Advance(time.Second)
		p := read(t, fast)
		if p["instance"].(map[string]any)["summary"] != "v"+string(rune('0'+i)) {
			t.Fatalf("快连接应按序收到每个 patch: %v", p)
		}
	}
	select {
	case <-sc.done:
	case <-time.After(waitTimeout):
		t.Fatal("队列满的慢连接应被断开")
	}
	waitConns(t, h.hub, 1)
	ping(t, fast)
}

func TestCloseDropsConnectionsAndRejectsNew(t *testing.T) {
	h := newHarness(t, 0)
	a, s := h.dial(adminToken), h.dial(screenToken)
	read(t, a)
	read(t, s)

	ca, cs := closedSoon(a), closedSoon(s)
	h.hub.Close()
	for _, ch := range []<-chan error{ca, cs} {
		if err := <-ch; err != nil {
			t.Fatalf("Close 应关闭连接: %v", err)
		}
	}
	waitConns(t, h.hub, 0)
	_, resp, err := h.dialAs(adminToken, h.origin())
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("关闭后新握手应 503: resp=%v err=%v", resp, err)
	}
	if body, _ := io.ReadAll(resp.Body); !strings.Contains(string(body), httpx.CodeShuttingDown) {
		t.Fatalf("503 应是统一的 JSON 错误体: %s", body)
	}
}

func TestStartContextEndClosesConnections(t *testing.T) {
	h := newHarness(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := h.hub.Start(ctx)
	c := h.dial(adminToken)
	read(t, c)
	closed := closedSoon(c)
	cancel()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("Start 的 done 应在 ctx 结束后关闭")
	}
	if err := <-closed; err != nil {
		t.Fatalf("ctx 结束应关闭连接: %v", err)
	}
}

func TestReconcilePushesTimeDrivenChanges(t *testing.T) {
	h := newHarness(t, 0, inst("a", "x"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.hub.Start(ctx)
	c := h.dial(adminToken)
	read(t, c)

	// 展示状态随时间自行变化（例如过期），没有任何变更通知。
	stale := inst("a", "x")
	stale.DisplayState = "stale"
	h.inst.set(stale)
	h.clk.Advance(reconcileInterval)
	p := read(t, c)
	if p["entity"] != ui.EntityInstanceState || p["instance"].(map[string]any)["display_state"] != "stale" {
		t.Fatalf("对账应推送变化的实例: %v", p)
	}
	h.clk.Advance(reconcileInterval)
	ping(t, c) // 没变化就不推
}

// blockingSink 的写入一直阻塞，直到写入的 ctx 结束。
type blockingSink struct{ first chan struct{} }

func newBlockingSink() *blockingSink { return &blockingSink{first: make(chan struct{}, 1)} }

func (b *blockingSink) write(ctx context.Context, _ []byte) error {
	select {
	case b.first <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (b *blockingSink) close(websocket.StatusCode, string) {}

func (b *blockingSink) waitFirstWrite(t *testing.T) {
	t.Helper()
	select {
	case <-b.first:
	case <-time.After(waitTimeout):
		t.Fatal("写线程没有开始写 snapshot")
	}
}

func waitConns(t *testing.T, h *Hub, n int) {
	t.Helper()
	deadline := time.After(waitTimeout)
	for {
		changed := h.connsChanged()
		if h.connCount() == n {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("连接数应为 %d，实际 %d", n, h.connCount())
		}
	}
}

func TestRevokedSessionDroppedByPeriodicRecheck(t *testing.T) {
	h := newHarness(t, 0)
	c := h.dial(adminToken)
	read(t, c)
	h.sess.drop(adminToken) // 没有任何回调：只靠定期复核兜底
	ping(t, c)              // 一直在 ping 也不能续命
	h.clk.Advance(sessionRecheckInterval)
	expectClosedWith(t, c, websocket.StatusPolicyViolation)
	waitConns(t, h.hub, 0)
}

func TestRecheckSessionsDropsRevokedImmediately(t *testing.T) {
	h := newHarness(t, 0)
	revoked, kept := h.dial(adminToken), h.dial(screenToken)
	read(t, revoked)
	read(t, kept)
	h.sess.drop(adminToken)
	h.hub.RecheckSessions()
	expectClosedWith(t, revoked, websocket.StatusPolicyViolation)
	ping(t, kept) // 其他会话的连接不受影响
}

func newRealSessions(t *testing.T, clk clock.Clock) (*auth.Sessions, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return auth.NewSessions(db, clk), db
}

// 真实会话服务：登出（Delete）与批量吊销（DeleteKind）经 OnRevoke 立即断开，过期靠定期复核。
func TestRealSessionLogoutRevokeAndExpiry(t *testing.T) {
	var (
		sessions *auth.Sessions
		db       *store.DB
	)
	h := newHarnessWith(t, 0, func(clk clock.Clock) SessionLookup {
		sessions, db = newRealSessions(t, clk)
		return sessions
	})
	fake := h.clk
	sessions.OnRevoke(h.hub.RecheckSessions)
	ctx := context.Background()

	logout, err := sessions.Create(ctx, auth.KindAdmin)
	if err != nil {
		t.Fatal(err)
	}
	c := h.dial(logout)
	read(t, c)
	if err := sessions.Delete(ctx, logout); err != nil {
		t.Fatal(err)
	}
	expectClosedWith(t, c, websocket.StatusPolicyViolation)

	screen, err := sessions.Create(ctx, auth.KindScreen)
	if err != nil {
		t.Fatal(err)
	}
	sc := h.dial(screen)
	read(t, sc)
	if err := sessions.DeleteKind(ctx, auth.KindScreen); err != nil {
		t.Fatal(err)
	}
	expectClosedWith(t, sc, websocket.StatusPolicyViolation)

	expired, err := sessions.Create(ctx, auth.KindAdmin)
	if err != nil {
		t.Fatal(err)
	}
	ec := h.dial(expired)
	read(t, ec)
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET expires_at = ?`, store.FormatTime(fake.Now().Add(-time.Second))); err != nil {
		t.Fatal(err)
	}
	fake.Advance(sessionRecheckInterval)
	expectClosedWith(t, ec, websocket.StatusPolicyViolation)
}

func TestBatchChangeFallsBackToSnapshot(t *testing.T) {
	var list []model.Instance
	for i := 0; i < 10; i++ {
		list = append(list, inst(string(rune('a'+i)), "v0"))
	}
	h := newHarness(t, 4, list...)
	c := h.dial(adminToken)
	read(t, c)

	for _, in := range list {
		in.Summary = "v1"
		h.inst.set(in)
		h.hub.NotifyInstance(in.ID)
	}
	h.clk.Advance(time.Second)
	m := read(t, c)
	if m["type"] != string(ui.TypeSnapshot) || len(m["instances"].([]any)) != 10 {
		t.Fatalf("批量变化应改发 snapshot: %v", m)
	}
	for _, raw := range m["instances"].([]any) {
		if raw.(map[string]any)["summary"] != "v1" {
			t.Fatalf("snapshot 应是最新状态: %v", raw)
		}
	}
	ping(t, c) // 连接健康，没有被当成慢连接
	waitConns(t, h.hub, 1)
}
