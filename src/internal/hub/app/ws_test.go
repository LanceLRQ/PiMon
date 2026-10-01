package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func readWS(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("读取 WebSocket 消息: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// 经真实 Serve：握手鉴权、snapshot 的 build 与注入 index 同源、设置变化经 REST 推成 patch、
// 关停时连接被关闭。
func TestUIWebSocketEndToEnd(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	addrCh := make(chan string, 1)
	a := openApp(t, testConfig(t), WithClock(clk), WithVersion("9.9.9-test"), WithOnListen(func(addr string) { addrCh <- addr }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	var base string
	select {
	case addr := <-addrCh:
		base = "http://" + addr
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待监听超时")
	}

	hc := newClient()
	code, _, err := a.SetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp, data := call(t, hc, base, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": testPassword, "language": "zh", "timezone": "UTC", "access_url": "",
	}); resp.StatusCode != 200 {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}

	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/ws"
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	if _, resp, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {base}}}); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("未带会话的握手应 401: resp=%v err=%v", resp, err)
	}
	u, _ := url.Parse(base)
	hdr := http.Header{"Origin": {base}}
	for _, ck := range hc.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			hdr.Set("Cookie", ck.String())
		}
	}
	c, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("握手: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	snap := readWS(t, c)
	if snap["type"] != "snapshot" || snap["build"] != "9.9.9-test" || snap["role"] != "admin" {
		t.Fatalf("snapshot 不对: %v", snap)
	}

	_, data := call(t, hc, base, "GET", "/api/settings", nil)
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	st.Language = "en"
	if resp, data := call(t, hc, base, "PUT", "/api/settings", st); resp.StatusCode != 200 {
		t.Fatalf("put settings = %d %s", resp.StatusCode, data)
	}
	clk.Advance(time.Second)
	p := readWS(t, c)
	if p["entity"] != "settings" || p["settings"].(map[string]any)["language"] != "en" {
		t.Fatalf("应收到设置 patch: %v", p)
	}

	closed := make(chan error, 1)
	go func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rcancel()
		_, _, err := c.Read(rctx)
		closed <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve 未在关停后退出")
	}
	if err := <-closed; err == nil || websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("关停应以 going away 关闭连接: %v", err)
	}
}
