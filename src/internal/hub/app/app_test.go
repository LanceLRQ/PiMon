package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 低成本 argon2 参数，仅用于测试。
var testParams = auth.Params{Memory: 64, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}

const testPassword = "correct horse"

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(t.TempDir(), "data"), LogLevel: "info"}
}

func testOpts(extra ...Option) []Option {
	base := []Option{
		WithClock(clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
		WithVersion("1.0.0"),
		WithHasherParams(testParams),
		WithGetenv(func(string) string { return "UTC" }),
		WithStderr(io.Discard),
	}
	return append(base, extra...)
}

func openApp(t *testing.T, cfg config.Config, extra ...Option) *App {
	t.Helper()
	a, err := Open(context.Background(), cfg, testOpts(extra...)...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func call(t *testing.T, c *http.Client, base, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func TestOpenEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	a := openApp(t, cfg)
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient()

	code, _, err := a.SetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp, data := call(t, c, srv.URL, "POST", "/api/setup", map[string]any{
		"setup_code": code, "password": testPassword, "language": "zh", "timezone": "UTC", "access_url": "",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("setup = %d %s", resp.StatusCode, data)
	}
	// 登出后换新 cookiejar，走真实登录。
	if resp, _ := call(t, c, srv.URL, "POST", "/api/logout", nil); resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	c = newClient()
	if resp, _ := call(t, c, srv.URL, "GET", "/api/settings", nil); resp.StatusCode != 401 {
		t.Fatalf("未登录读设置应为 401，得到 %d", resp.StatusCode)
	}
	resp, data = call(t, c, srv.URL, "POST", "/api/login", map[string]any{"password": testPassword})
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, data)
	}

	resp, data = call(t, c, srv.URL, "GET", "/api/settings", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("settings = %d %s", resp.StatusCode, data)
	}
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	st.Language = "en"
	resp, data = call(t, c, srv.URL, "PUT", "/api/settings", st)
	if resp.StatusCode != 200 {
		t.Fatalf("put settings = %d %s", resp.StatusCode, data)
	}
	_, data = call(t, c, srv.URL, "GET", "/api/settings", nil)
	var got model.Settings
	if err := json.Unmarshal(data, &got); err != nil || got.Language != "en" {
		t.Fatalf("设置未持久化: %s (%v)", data, err)
	}
	bad := st
	bad.Timezone = "Not/AZone"
	resp, data = call(t, c, srv.URL, "PUT", "/api/settings", bad)
	if resp.StatusCode != 400 || !strings.Contains(string(data), "validation.failed") {
		t.Fatalf("非法设置应 400 validation.failed，得到 %d %s", resp.StatusCode, data)
	}

	resp, data = call(t, c, srv.URL, "POST", "/api/backups", nil)
	if resp.StatusCode >= 300 {
		t.Fatalf("create backup = %d %s", resp.StatusCode, data)
	}
	resp, data = call(t, c, srv.URL, "GET", "/api/backups", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(data), "manual") {
		t.Fatalf("list backups = %d %s", resp.StatusCode, data)
	}
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(cfg.DataDir); err != nil || st.Mode().Perm() != 0o750 {
		t.Fatalf("数据目录权限 = %v, %v", st, err)
	}
}

func TestSetupCodeRejectedWhenAdminExists(t *testing.T) {
	a := openApp(t, testConfig(t))
	ctx := context.Background()
	if _, _, err := a.SetupCode(ctx); err != nil {
		t.Fatal(err)
	}
	h, _ := auth.Hasher{Params: testParams}.Hash(testPassword)
	if err := a.admins.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.SetupCode(ctx); err != ErrAdminExists {
		t.Fatalf("err = %v，期望 ErrAdminExists", err)
	}
}

func TestPreUpgradeBackupOnVersionChange(t *testing.T) {
	cfg := testConfig(t)
	first, err := Open(context.Background(), cfg, testOpts(WithVersion("1.0.0"))...)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := first.backups.List(); len(list) != 0 {
		t.Fatalf("全新安装不应触发升级备份: %+v", list)
	}
	_ = first.Close()

	// 同版本重开：不备份。
	same, err := Open(context.Background(), cfg, testOpts(WithVersion("1.0.0"))...)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := same.backups.List(); len(list) != 0 {
		t.Fatalf("同版本不应备份: %+v", list)
	}
	_ = same.Close()

	second := openApp(t, cfg, WithVersion("1.1.0"))
	list, err := second.backups.List()
	if err != nil || len(list) != 1 || list[0].Reason != backup.ReasonPreUpgrade {
		t.Fatalf("备份列表 = %+v, %v", list, err)
	}
	v, _ := second.db.AppVersion(context.Background())
	if v != "1.1.0" {
		t.Fatalf("版本未更新: %q", v)
	}
}

func TestResetPassword(t *testing.T) {
	a := openApp(t, testConfig(t))
	ctx := context.Background()
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)

	var out bytes.Buffer
	if err := a.ResetPassword(ctx, strings.NewReader("whatever123\n"), &out, false); err != ErrNoAdmin {
		t.Fatalf("无管理员时 err = %v，期望 ErrNoAdmin", err)
	}

	h, _ := auth.Hasher{Params: testParams}.Hash(testPassword)
	if err := a.admins.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	old := newClient()
	if resp, data := call(t, old, srv.URL, "POST", "/api/login", map[string]any{"password": testPassword}); resp.StatusCode != 200 {
		t.Fatalf("旧密码登录 = %d %s", resp.StatusCode, data)
	}

	if err := a.ResetPassword(ctx, strings.NewReader("short\n"), &out, false); err == nil {
		t.Fatal("过短密码应报错")
	}
	if err := a.ResetPassword(ctx, bytes.NewReader([]byte("brand new pass\r\n")), &out, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "brand new pass") {
		t.Fatal("输出不得回显密码")
	}
	// 旧会话被删除。
	if resp, _ := call(t, old, srv.URL, "GET", "/api/settings", nil); resp.StatusCode != 401 {
		t.Fatalf("旧会话应失效，状态 %d", resp.StatusCode)
	}
	fresh := newClient()
	if resp, _ := call(t, fresh, srv.URL, "POST", "/api/login", map[string]any{"password": "brand new pass"}); resp.StatusCode != 200 {
		t.Fatalf("新密码登录 = %d", resp.StatusCode)
	}
}

func TestInvalidStoredSettingsGiveReadableError(t *testing.T) {
	cfg := testConfig(t)
	a := openApp(t, cfg)
	if _, err := a.db.ExecContext(context.Background(),
		`INSERT INTO settings(key, value) VALUES('global', '{"language":"xx"}') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Skipf("无法构造非法设置: %v", err)
	}
	_ = a.Close()
	_, err := Open(context.Background(), cfg, testOpts()...)
	if err == nil || !strings.Contains(err.Error(), "设置") {
		t.Fatalf("err = %v，应包含可读提示", err)
	}
}

func TestServeStartsAndStopsGracefully(t *testing.T) {
	cfg := testConfig(t)
	var stderr bytes.Buffer
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithStderr(&stderr), WithOnListen(func(addr string) { addrCh <- addr }))

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
	resp, data := call(t, newClient(), base, "GET", "/healthz", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d %s", resp.StatusCode, data)
	}
	if !strings.Contains(stderr.String(), "设置码") {
		t.Fatalf("stderr 应打印设置码: %q", stderr.String())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("退出超时")
	}
}

func TestRestoreSubcommand(t *testing.T) {
	cfg := testConfig(t)
	a := openApp(t, cfg)
	info, err := a.backups.Create(context.Background(), backup.ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	if err := os.Remove(cfg.DBPath()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Restore(cfg, filepath.Join(cfg.BackupDir(), info.Name), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		t.Fatalf("恢复后数据库应存在: %v", err)
	}
	if !strings.Contains(out.String(), "停止服务") {
		t.Fatalf("应提示先停止服务: %q", out.String())
	}
	if err := Restore(cfg, filepath.Join(cfg.DataDir, "nope.tar.gz"), &out); err == nil {
		t.Fatal("不存在的备份应报错")
	}
}

func TestEnsureSetupCode已有有效设置码时提示而不重新生成(t *testing.T) {
	var stderr bytes.Buffer
	a := openApp(t, testConfig(t), WithStderr(&stderr))
	ctx := context.Background()
	code, _, err := a.setupCodes.Generate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ensureSetupCode(ctx); err != nil {
		t.Fatal(err)
	}
	out := stderr.String()
	if !strings.Contains(out, "已有未过期的设置码") || !strings.Contains(out, "setup-code") {
		t.Fatalf("stderr 应提示已有设置码: %q", out)
	}
	if strings.Contains(out, code) {
		t.Fatalf("提示不应泄露已有设置码明文: %q", out)
	}
	if ok, err := a.setupCodes.Verify(ctx, code); err != nil || !ok {
		t.Fatalf("原设置码应仍有效: ok=%v err=%v", ok, err)
	}
}

func writeExecPlugin(t *testing.T, root, id string) {
	t.Helper()
	d := filepath.Join(root, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	y := "id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: " + id +
		"\nkind: source\nruntime: exec\nruns_on: [hub]\noutputs:\n  - {key: v, type: number, title: V}\n"
	if err := os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{d, filepath.Join(d, "run"), filepath.Join(d, "plugin.yaml")} {
		mode := os.FileMode(0o755)
		if strings.HasSuffix(p, "plugin.yaml") {
			mode = 0o644
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginRegistryScansOnOpenAndWatchesWhileServing(t *testing.T) {
	cfg := testConfig(t)
	if err := os.MkdirAll(cfg.PluginDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	writeExecPlugin(t, cfg.PluginDir(), "before")
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithClock(clock.Real{}), WithOnListen(func(addr string) { addrCh <- addr }))
	if _, ok := a.plugins.Get("before"); !ok {
		t.Fatal("Open 时应已扫描插件目录")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	select {
	case <-addrCh:
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待监听超时")
	}
	writeExecPlugin(t, cfg.PluginDir(), "after")
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, ok := a.plugins.Get("after"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve 期间新增的插件未被发现")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("退出超时（监视应随 ctx 停止）")
	}
}
