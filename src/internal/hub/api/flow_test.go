package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func cookieOf(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.client, "GET", "/healthz", nil)
	var got map[string]string
	_ = json.Unmarshal(data, &got)
	if resp.StatusCode != 200 || got["status"] != "ok" || got["version"] == "" {
		t.Fatalf("healthz = %d %s", resp.StatusCode, data)
	}
}

func TestFullFlow(t *testing.T) {
	e := newEnv(t)
	c := e.client

	_, data := e.do(c, "GET", "/api/setup/status", nil)
	if string(data) != "{\"needs_setup\":true}\n" {
		t.Fatalf("setup/status = %s", data)
	}
	resp, data := e.do(c, "GET", "/api/settings", nil)
	e.expectError(resp, data, 401, "auth.required")

	code, _, err := e.deps.SetupCodes.Generate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resp, data = e.do(c, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": testPassword, "language": "en", "timezone": "UTC", "access_url": "https://pi.example.com",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}

	resp, data = e.do(c, "GET", "/api/settings", nil)
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil || resp.StatusCode != 200 {
		t.Fatalf("settings = %d %s", resp.StatusCode, data)
	}
	if st.Language != "en" || st.Timezone != "UTC" || st.AccessURL != "https://pi.example.com" {
		t.Fatalf("设置未写入: %+v", st)
	}

	_, data = e.do(c, "GET", "/api/session", nil)
	var sess map[string]any
	_ = json.Unmarshal(data, &sess)
	if sess["authenticated"] != true || sess["kind"] != "admin" || sess["needs_setup"] != false {
		t.Fatalf("session = %s", data)
	}

	resp, _ = e.do(c, "POST", "/api/logout", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	if ck := cookieOf(resp); ck == nil || ck.MaxAge >= 0 {
		t.Fatalf("登出应清除 Cookie: %+v", ck)
	}
	resp, data = e.do(c, "GET", "/api/settings", nil)
	e.expectError(resp, data, 401, "auth.required")
	_, data = e.do(c, "GET", "/api/session", nil)
	_ = json.Unmarshal(data, &sess)
	if sess["authenticated"] != false {
		t.Fatalf("登出后 session = %s", data)
	}

	resp, data = e.do(c, "POST", "/api/login", map[string]any{"password": testPassword})
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, data)
	}
	resp, _ = e.do(c, "GET", "/api/settings", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("登录后 settings = %d", resp.StatusCode)
	}
}

func TestSetup_AlreadyDone(t *testing.T) {
	e := newEnv(t)
	e.setup()
	resp, data := e.do(e.newClient(), "POST", "/api/setup", map[string]any{"setup_code": "x", "password": testPassword})
	e.expectError(resp, data, 409, "setup.already_done")
	_, data = e.do(e.client, "GET", "/api/setup/status", nil)
	if string(data) != "{\"needs_setup\":false}\n" {
		t.Fatalf("setup/status = %s", data)
	}
}

func TestSetup_CodeWrongRemainingAndLock(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.deps.SetupCodes.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 9; i++ {
		resp, data := e.do(e.client, "POST", "/api/setup", map[string]any{"setup_code": "WRONG", "password": testPassword})
		er := e.expectError(resp, data, 401, "setup.invalid_code")
		if got := er.Error.Details["remaining"]; got != float64(10-i) {
			t.Fatalf("第 %d 次 remaining = %v", i, got)
		}
	}
	resp, data := e.do(e.client, "POST", "/api/setup", map[string]any{"setup_code": "WRONG", "password": testPassword})
	e.expectError(resp, data, 429, "auth.locked")
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("缺少 Retry-After")
	}
}

func TestSetup_PasswordTooShortAndInvalidFields(t *testing.T) {
	e := newEnv(t)
	code, _, _ := e.deps.SetupCodes.Generate(t.Context())
	resp, data := e.do(e.client, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": "短短短短短短短", "language": "fr", "timezone": "Nope/Zone", "access_url": "ftp://x",
	})
	er := e.expectError(resp, data, 400, "validation.failed")
	fields, _ := er.Error.Details["fields"].(map[string]any)
	want := map[string]string{"password": "out_of_range", "language": "invalid", "timezone": "invalid", "access_url": "invalid"}
	for k, v := range want {
		if fields[k] != v {
			t.Fatalf("fields[%s] = %v，期望 %s（全部 %v）", k, fields[k], v, fields)
		}
	}
	// 失败不应消费设置码，也不应创建管理员。
	if ok, _ := e.deps.Admins.Exists(t.Context()); ok {
		t.Fatal("校验失败不应创建管理员")
	}
	// 按字符计：8 个汉字合法。
	resp, data = e.do(e.client, "POST", "/api/setup", map[string]any{"setup_code": code, "password": "密码密码密码密码"})
	if resp.StatusCode != 200 {
		t.Fatalf("8 个汉字密码应通过: %d %s", resp.StatusCode, data)
	}
}

func TestSetup_InvalidJSON(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.client, "POST", "/api/setup", map[string]any{"setup_code": "a", "password": "b", "bogus": 1})
	e.expectError(resp, data, 400, "request.invalid_json")
}

func TestLogin_NoAdmin(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.client, "POST", "/api/login", map[string]any{"password": "whatever1"})
	e.expectError(resp, data, 409, "setup.required")
}

func TestLogin_WrongPasswordAndLock(t *testing.T) {
	e := newEnv(t)
	e.setup()
	c := e.newClient()
	for i := 1; i <= 9; i++ {
		resp, data := e.do(c, "POST", "/api/login", map[string]any{"password": "wrong-password"})
		er := e.expectError(resp, data, 401, "auth.invalid_password")
		if got := er.Error.Details["remaining"]; got != float64(10-i) {
			t.Fatalf("第 %d 次 remaining = %v", i, got)
		}
	}
	resp, data := e.do(c, "POST", "/api/login", map[string]any{"password": "wrong-password"})
	er := e.expectError(resp, data, 429, "auth.locked")
	wantUntil := e.clk.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339)
	if got := er.Error.Details["locked_until"]; got != wantUntil {
		t.Fatalf("locked_until = %v，期望 %s", got, wantUntil)
	}
	if got, _ := er.Error.Details["client_ip"].(string); got != "127.0.0.1" {
		t.Fatalf("client_ip = %v", er.Error.Details["client_ip"])
	}
	// 锁定期内正确密码也被拒绝。
	resp, data = e.do(c, "POST", "/api/login", map[string]any{"password": testPassword})
	e.expectError(resp, data, 429, "auth.locked")
}

func TestLogin_TrustedProxyCountsPerClient(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.trustLoopback()
	c := e.newClient()
	xff := func(ip string) reqOpt { return withHeader("X-Forwarded-For", ip) }
	for i := 0; i < 10; i++ {
		e.do(c, "POST", "/api/login", map[string]any{"password": "wrong-password"}, xff("203.0.113.1"))
	}
	resp, data := e.do(c, "POST", "/api/login", map[string]any{"password": testPassword}, xff("203.0.113.1"))
	e.expectError(resp, data, 429, "auth.locked")
	// 另一个客户端不受影响。
	resp, data = e.do(c, "POST", "/api/login", map[string]any{"password": testPassword}, xff("203.0.113.2"))
	if resp.StatusCode != 200 {
		t.Fatalf("其他客户端应能登录: %d %s", resp.StatusCode, data)
	}
}

func TestLogin_DirectForgedXFFIgnored(t *testing.T) {
	e := newEnv(t)
	e.setup()
	c := e.newClient()
	for i := 0; i < 10; i++ {
		e.do(c, "POST", "/api/login", map[string]any{"password": "wrong-password"},
			withHeader("X-Forwarded-For", "198.51.100."+string(rune('1'+i))))
	}
	resp, data := e.do(c, "POST", "/api/login", map[string]any{"password": testPassword},
		withHeader("X-Forwarded-For", "198.51.100.99"))
	e.expectError(resp, data, 429, "auth.locked")
}

func TestOriginCheck(t *testing.T) {
	e := newEnv(t)
	e.setup()
	body := map[string]any{"password": testPassword}
	resp, data := e.do(e.client, "POST", "/api/login", body, noOrigin())
	e.expectError(resp, data, 403, "origin.mismatch")
	resp, data = e.do(e.client, "POST", "/api/login", body, withHeader("Origin", "http://evil.example.com"))
	e.expectError(resp, data, 403, "origin.mismatch")
	// GET 不校验 Origin。
	resp, _ = e.do(e.client, "GET", "/api/session", nil, noOrigin())
	if resp.StatusCode != 200 {
		t.Fatalf("GET 无 Origin 应放行: %d", resp.StatusCode)
	}
}

func TestCookieAttributes(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.trustLoopback()
	host := e.srv.Listener.Addr().String()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, _ := e.do(c, "POST", "/api/login", map[string]any{"password": testPassword})
	ck := cookieOf(resp)
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.Secure || ck.MaxAge != 30*24*3600 {
		t.Fatalf("http 下管理员 Cookie 属性不符: %+v", ck)
	}

	resp, _ = e.do(c, "POST", "/api/login", map[string]any{"password": testPassword},
		withHeader("X-Forwarded-Proto", "https"), withHeader("Origin", "https://"+host))
	ck = cookieOf(resp)
	if ck == nil || !ck.Secure {
		t.Fatalf("https 下应带 Secure: %+v", ck)
	}

	resp, _ = e.do(c, "GET", "/screen/auth?token="+e.screenToken(), nil)
	ck = cookieOf(resp)
	if ck == nil || ck.MaxAge != 10*365*24*3600 || !ck.HttpOnly {
		t.Fatalf("屏幕 Cookie 属性不符: %+v", ck)
	}
}

func TestScreenSessionForbiddenOnAdminAPI(t *testing.T) {
	e := newEnv(t)
	e.setup()
	sc := e.newClient()
	resp, _ := e.do(sc, "GET", "/screen/auth?token="+e.screenToken(), nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/screen" {
		t.Fatalf("screen/auth = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, data := e.do(sc, "GET", "/api/settings", nil)
	e.expectError(resp, data, 403, "auth.forbidden")
	_, data = e.do(sc, "GET", "/api/session", nil)
	var sess map[string]any
	_ = json.Unmarshal(data, &sess)
	if sess["authenticated"] != true || sess["kind"] != "screen" {
		t.Fatalf("session = %s", data)
	}
}

func TestScreenAuth_WrongTokenAndLock(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 9; i++ {
		resp, data := e.do(e.client, "GET", "/screen/auth?token=bad", nil)
		if resp.StatusCode != 401 || resp.Header.Get("Content-Type")[:10] != "text/plain" {
			t.Fatalf("第 %d 次 = %d %q %s", i, resp.StatusCode, resp.Header.Get("Content-Type"), data)
		}
	}
	resp, _ := e.do(e.client, "GET", "/screen/auth?token=bad", nil)
	if resp.StatusCode != 429 {
		t.Fatalf("第 10 次 = %d，期望 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("第 10 次的 429 应带 Retry-After，得 %q", ra)
	}
	resp, _ = e.do(e.client, "GET", "/screen/auth?token="+e.screenToken(), nil)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("锁定期内应 429: %d", resp.StatusCode)
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	oldCookie := e.cookieValue(admin)

	resp, data := e.do(admin, "PUT", "/api/admin/password", map[string]any{"current_password": "nope-nope", "new_password": "brand new pw"})
	er := e.expectError(resp, data, 401, "auth.invalid_password")
	if er.Error.Details["remaining"] != float64(9) {
		t.Fatalf("remaining = %v", er.Error.Details["remaining"])
	}
	resp, data = e.do(admin, "PUT", "/api/admin/password", map[string]any{"current_password": testPassword, "new_password": "short"})
	er = e.expectError(resp, data, 400, "validation.failed")
	if f, _ := er.Error.Details["fields"].(map[string]any); f["new_password"] != "out_of_range" {
		t.Fatalf("fields = %v", er.Error.Details)
	}

	resp, data = e.do(admin, "PUT", "/api/admin/password", map[string]any{"current_password": testPassword, "new_password": "brand new pw"})
	if resp.StatusCode != 200 {
		t.Fatalf("改密码 = %d %s", resp.StatusCode, data)
	}
	// 当前客户端拿到新会话，仍可访问；旧 Cookie 失效。
	if r, _ := e.do(admin, "GET", "/api/settings", nil); r.StatusCode != 200 {
		t.Fatalf("新会话应可用: %d", r.StatusCode)
	}
	stale := e.newClient()
	r, data := e.do(stale, "GET", "/api/settings", nil, withHeader("Cookie", auth.CookieName+"="+oldCookie))
	e.expectError(r, data, 401, "auth.required")

	if r, _ := e.do(e.newClient(), "POST", "/api/login", map[string]any{"password": testPassword}); r.StatusCode != 401 {
		t.Fatalf("旧密码应失败: %d", r.StatusCode)
	}
	if r, _ := e.do(e.newClient(), "POST", "/api/login", map[string]any{"password": "brand new pw"}); r.StatusCode != 200 {
		t.Fatalf("新密码应成功: %d", r.StatusCode)
	}
}

func TestChangePassword_RequiresAdmin(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.client, "PUT", "/api/admin/password", map[string]any{"current_password": "a", "new_password": "b"})
	e.expectError(resp, data, 401, "auth.required")
}

func (e *env) cookieValue(c *http.Client) string {
	e.t.Helper()
	u, _ := url.Parse(e.srv.URL)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			return ck.Value
		}
	}
	e.t.Fatal("没有会话 Cookie")
	return ""
}

func TestScreenTokenReset(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	sc := e.newClient()
	oldToken := e.screenToken()
	e.do(sc, "GET", "/screen/auth?token="+oldToken, nil)
	if r, _ := e.do(sc, "GET", "/api/session", nil); r.StatusCode != 200 {
		t.Fatal("session")
	}

	resp, data := e.do(admin, "POST", "/api/screen/token/reset", nil)
	if resp.StatusCode != 204 || len(data) != 0 {
		t.Fatalf("reset = %d %q", resp.StatusCode, data)
	}
	_, data = e.do(sc, "GET", "/api/session", nil)
	var sess map[string]any
	_ = json.Unmarshal(data, &sess)
	if sess["authenticated"] != false {
		t.Fatalf("旧屏幕 Cookie 应失效: %s", data)
	}
	if r, _ := e.do(e.newClient(), "GET", "/screen/auth?token="+oldToken, nil); r.StatusCode != 401 {
		t.Fatalf("旧令牌应失效: %d", r.StatusCode)
	}
	if r, _ := e.do(e.newClient(), "GET", "/screen/auth?token="+e.screenToken(), nil); r.StatusCode != 302 {
		t.Fatalf("新令牌应可用: %d", r.StatusCode)
	}
	// 屏幕会话不能重置令牌。
	r, data := e.do(sc, "POST", "/api/screen/token/reset", nil)
	e.expectError(r, data, 401, "auth.required")
}

func TestSettings_ValidationFields(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	_, data := e.do(admin, "GET", "/api/settings", nil)
	var st model.Settings
	_ = json.Unmarshal(data, &st)

	st.Retention.RawHours = 0
	st.Language = "fr"
	resp, data := e.do(admin, "PUT", "/api/settings", st)
	er := e.expectError(resp, data, 400, "validation.failed")
	f, _ := er.Error.Details["fields"].(map[string]any)
	if f["retention.raw_hours"] != "out_of_range" || f["language"] != "invalid" {
		t.Fatalf("fields = %v", f)
	}

	st.Retention.RawHours = 48
	st.Language = "en"
	resp, data = e.do(admin, "PUT", "/api/settings", st)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	var got model.Settings
	_ = json.Unmarshal(data, &got)
	if got.Retention.RawHours != 48 || got.Language != "en" {
		t.Fatalf("返回值 = %+v", got)
	}
	resp, data = e.do(admin, "PUT", "/api/settings", map[string]any{"nope": 1})
	e.expectError(resp, data, 400, "request.invalid_json")
}
