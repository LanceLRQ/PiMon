package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
)

// setupAdmin 完成首次设置并返回已登录的客户端与基地址。
func setupAdmin(t *testing.T, a *App) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient()
	code, _, err := a.SetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp, data := call(t, c, srv.URL, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": testPassword, "language": "zh", "timezone": "UTC", "access_url": "",
	}); resp.StatusCode != 200 {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}
	return c, srv.URL
}

func dialWS(t *testing.T, c *http.Client, base string) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(base)
	hdr := http.Header{"Origin": {base}}
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			hdr.Set("Cookie", ck.String())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("握手: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	readWS(t, conn) // snapshot
	return conn
}

func expectRevoked(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("应以 policy violation 立即断开: %v", err)
			}
			return
		}
	}
}

// 改密码吊销全部管理员会话后，旧 WebSocket 不必等定期复核，立即被断开。
func TestChangePasswordDisconnectsExistingWS(t *testing.T) {
	a := openApp(t, testConfig(t))
	c, base := setupAdmin(t, a)
	old := newClient()
	if resp, data := call(t, old, base, "POST", "/api/login", map[string]any{"password": testPassword}); resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, data)
	}
	conn := dialWS(t, old, base)
	if resp, data := call(t, c, base, "PUT", "/api/admin/password", map[string]any{
		"current_password": testPassword, "new_password": "another horse staple",
	}); resp.StatusCode != 200 {
		t.Fatalf("change password = %d %s", resp.StatusCode, data)
	}
	expectRevoked(t, conn)
}

// 屏幕令牌轮换后，旧屏幕会话的 WebSocket 立即被断开。
func TestScreenTokenResetDisconnectsScreenWS(t *testing.T) {
	a := openApp(t, testConfig(t))
	admin, base := setupAdmin(t, a)
	if err := a.screen.EnsureExists(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.cfg.ScreenTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	screen := newClient()
	screen.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if resp, _ := call(t, screen, base, "GET", "/screen/auth?token="+strings.TrimSpace(string(raw)), nil); resp.StatusCode != http.StatusFound {
		t.Fatalf("screen/auth = %d", resp.StatusCode)
	}
	conn := dialWS(t, screen, base)
	if resp, data := call(t, admin, base, "POST", "/api/screen/token/reset", nil); resp.StatusCode >= 300 {
		t.Fatalf("reset = %d %s", resp.StatusCode, data)
	}
	expectRevoked(t, conn)
}

// 所有响应（API、前端页面、静态资源、404）都带基础安全头。
func TestSecurityHeadersOnAllResponses(t *testing.T) {
	a := openApp(t, testConfig(t))
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient()
	for _, p := range []string{"/healthz", "/api/session", "/api/nope", "/instances/1", "/favicon.svg", "/assets/none.js"} {
		resp, _ := call(t, c, srv.URL, "GET", p, nil)
		for k, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "same-origin",
		} {
			if got := resp.Header.Get(k); got != want {
				t.Errorf("%s %s = %q，期望 %q", p, k, got, want)
			}
		}
	}
}
