package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/history"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screenstate"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/settings"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/internal/hub/system"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

const testPassword = "correct horse"

// 低成本 argon2 参数，仅用于测试。
var testParams = auth.Params{Memory: 64, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}

// countingHasher 记录 Verify 的调用次数与瞬时最大并发数。
type countingHasher struct {
	auth.Hasher
	delay    time.Duration
	verifies atomic.Int64
	active   atomic.Int64
	peak     atomic.Int64
}

func (h *countingHasher) Verify(encoded, password string) (bool, error) {
	h.verifies.Add(1)
	cur := h.active.Add(1)
	defer h.active.Add(-1)
	for {
		p := h.peak.Load()
		if cur <= p || h.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	time.Sleep(h.delay)
	return h.Hasher.Verify(encoded, password)
}

type env struct {
	t         *testing.T
	clk       *clock.Fake
	deps      Deps
	db        *store.DB
	refs      *fakeReferrers
	plugins   *plugins.Registry
	pluginDir string
	hasher    *countingHasher
	srv       *httptest.Server
	client    *http.Client
	tokenFn   string
	ring      *logging.Ring
}

func newEnv(t *testing.T) *env { return newEnvWith(t) }

// newEnvWith 在默认测试插件之外追加内置插件。
func newEnvWith(t *testing.T, extra ...runtime.Source) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st, err := settings.Load(ctx, db,
		settings.WithGetenv(func(string) string { return "Asia/Shanghai" }),
		settings.WithReadlink(func(string) (string, error) { return "", os.ErrNotExist }))
	if err != nil {
		t.Fatal(err)
	}
	hasher := &countingHasher{Hasher: auth.Hasher{Params: testParams}}
	tokenPath := filepath.Join(dir, "screen.token")
	keyPath := filepath.Join(dir, "secret.key")
	box, err := secret.LoadOrCreate(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	refs := &fakeReferrers{refs: map[string][]model.ProxyReferrer{}}
	pluginDir := filepath.Join(dir, "plugins")
	reg := plugins.New(plugins.Config{
		Dir: pluginDir, DB: db, Clock: clk, Builtins: append(testBuiltins(t), extra...),
	})
	if _, err := reg.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	hist := history.New(history.Config{DB: db, Clock: clk, Retention: func() model.RetentionSettings { return st.Get().Retention }})
	inst := instances.New(instances.Config{DB: db, Box: box, Clock: clk, Plugins: reg, History: hist})
	ring := logging.NewRing(5)
	deps := Deps{
		System: system.New(system.Config{
			Clock: clk, Version: "v-test", DataDir: dir, Plugins: reg, Ring: ring,
			ProcWriteBytes: func() (int64, bool) { return 0, false },
		}),
		DataDir:      dir,
		ListenAddr:   func() string { return "0.0.0.0:41999" },
		LocalIPs:     func() []net.IP { return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.7.8")} },
		Plugins:      reg,
		Instances:    inst,
		History:      hist,
		Settings:     st,
		Hasher:       hasher,
		Limiter:      auth.NewLimiter(clk, 10, 15*time.Minute),
		SetupCodes:   auth.NewSetupCodes(db, clk, box),
		Admins:       auth.NewAdmins(db, clk),
		Sessions:     auth.NewSessions(db, clk),
		ScreenTokens: auth.NewScreenTokens(db, clk, tokenPath),
		Backups: backup.New(backup.Config{
			DB: db, Clock: clk, SecretPath: keyPath,
			Dir: filepath.Join(dir, "backups"), Settings: st.Get,
		}),
		Proxies: proxies.New(proxies.Config{DB: db, Box: box, Clock: clk, Referrers: refs}),
	}
	inst.UseProxies(deps.Proxies)
	deps.Screens = screens.New(screens.Config{DB: db, Clock: clk, Plugins: reg, Instances: inst})
	inst.UseScreenRefs(deps.Screens)
	deps.ScreenState = screenstate.New(screenstate.Config{DB: db, Clock: clk, Timezone: func() string { return st.Get().Timezone }})
	if err := deps.ScreenState.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deps.ScreenTokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(deps))
	t.Cleanup(srv.Close)
	e := &env{t: t, clk: clk, deps: deps, db: db, refs: refs, plugins: reg, pluginDir: pluginDir, hasher: hasher, srv: srv, tokenFn: tokenPath, ring: ring}
	e.client = e.newClient()
	return e
}

// newClient 返回带独立 cookie 罐、不跟随重定向的客户端。
func (e *env) newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) screenToken() string {
	b, err := os.ReadFile(e.tokenFn)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(bytes.TrimSpace(b))
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func noOrigin() reqOpt              { return func(r *http.Request) { r.Header.Del("Origin") } }

// do 发请求；默认带同源 Origin。body 非 nil 时编码为 JSON。
func (e *env) do(c *http.Client, method, path string, body any, opts ...reqOpt) (*http.Response, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Origin", e.srv.URL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

type errResp struct {
	Error struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func parseErr(t *testing.T, data []byte) errResp {
	t.Helper()
	var er errResp
	if err := json.Unmarshal(data, &er); err != nil {
		t.Fatalf("响应不是错误 JSON: %s", data)
	}
	return er
}

func (e *env) expectError(resp *http.Response, data []byte, status int, code string) errResp {
	e.t.Helper()
	if resp.StatusCode != status {
		e.t.Fatalf("状态码 = %d，期望 %d，响应 %s", resp.StatusCode, status, data)
	}
	er := parseErr(e.t, data)
	if er.Error.Code != code {
		e.t.Fatalf("code = %q，期望 %q", er.Error.Code, code)
	}
	return er
}

// setup 完成首次设置并返回已登录的客户端。
func (e *env) setup() *http.Client {
	e.t.Helper()
	code, _, err := e.deps.SetupCodes.Generate(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	c := e.newClient()
	resp, data := e.do(c, "POST", "/api/setup", map[string]any{"setup_code": code, "password": testPassword})
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("setup 失败: %d %s", resp.StatusCode, data)
	}
	return c
}

// trustLoopback 把回环地址设为受信任反代，便于用 X-Forwarded-* 模拟不同客户端。
func (e *env) trustLoopback() {
	e.t.Helper()
	s := e.deps.Settings.Get()
	s.TrustedProxies = []string{"127.0.0.1/32", "::1/128"}
	if err := e.deps.Settings.Update(context.Background(), s); err != nil {
		e.t.Fatal(err)
	}
}
