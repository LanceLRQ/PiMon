package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
)

// fakeReferrers 是 proxies.Referrers 的测试替身。
type fakeReferrers struct {
	mu     sync.Mutex
	refs   map[string][]model.ProxyReferrer
	resets []string
}

func (f *fakeReferrers) ListByProxy(_ context.Context, id string) ([]model.ProxyReferrer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.ProxyReferrer(nil), f.refs[id]...), nil
}

func (f *fakeReferrers) ResetToDirect(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets = append(f.resets, id)
	delete(f.refs, id)
	return nil
}

func TestProxiesRequireAdmin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/proxies"}, {"POST", "/api/proxies"}, {"GET", "/api/proxies/x"},
		{"PUT", "/api/proxies/x"}, {"DELETE", "/api/proxies/x"}, {"POST", "/api/proxies/x/test"},
	} {
		resp, data := e.do(anon, c.m, c.p, map[string]any{})
		e.expectError(resp, data, http.StatusUnauthorized, httpx.CodeAuthRequired)
	}
}

func createProxy(t *testing.T, e *env, c *http.Client, body map[string]any) model.Proxy {
	t.Helper()
	resp, data := e.do(c, "POST", "/api/proxies", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d %s", resp.StatusCode, data)
	}
	var p model.Proxy
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProxiesCRUDAndAuthNotEchoed(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()

	resp, data := e.do(admin, "GET", "/api/proxies", nil)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("空列表 = %d %q", resp.StatusCode, data)
	}

	p := createProxy(t, e, admin, map[string]any{
		"name": "家里", "scheme": "socks5", "address": "10.0.0.1:1080", "remote_dns": true,
		"auth": map[string]any{"username": "alice", "password": "s3cret-pw"},
	})
	if p.Scheme != "socks5h" || !p.Auth.Set {
		t.Fatalf("p = %+v", p)
	}

	// 响应与库里都没有明文
	_, data = e.do(admin, "GET", "/api/proxies/"+p.ID, nil)
	if strings.Contains(string(data), "alice") || strings.Contains(string(data), "s3cret-pw") {
		t.Fatalf("GET 回显了认证: %s", data)
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if a, _ := raw["auth"].(map[string]any); a["set"] != true || len(a) != 1 {
		t.Fatalf("auth 应为 {\"set\":true}: %v", raw["auth"])
	}
	_, data = e.do(admin, "GET", "/api/proxies", nil)
	if strings.Contains(string(data), "s3cret-pw") {
		t.Fatalf("列表回显了认证: %s", data)
	}
	var enc string
	if err := e.db.QueryRow(`SELECT auth_enc FROM proxies WHERE id=?`, p.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || strings.Contains(enc, "alice") || strings.Contains(enc, "s3cret-pw") {
		t.Fatalf("库中认证应为密文: %q", enc)
	}

	// PUT 不带 auth 保留原值
	resp, data = e.do(admin, "PUT", "/api/proxies/"+p.ID, map[string]any{
		"name": "家里2", "scheme": "socks5h", "address": "10.0.0.2:1080",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	var up model.Proxy
	_ = json.Unmarshal(data, &up)
	if up.Name != "家里2" || up.Address != "10.0.0.2:1080" || !up.Auth.Set {
		t.Fatalf("up = %+v", up)
	}
	var enc2 string
	_ = e.db.QueryRow(`SELECT auth_enc FROM proxies WHERE id=?`, p.ID).Scan(&enc2)
	if enc2 != enc {
		t.Error("留空应保留原密文")
	}

	resp, data = e.do(admin, "DELETE", "/api/proxies/"+p.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "GET", "/api/proxies/"+p.ID, nil)
	e.expectError(resp, data, http.StatusNotFound, httpx.CodeProxyNotFound)
}

func TestProxiesNotFound(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	body := map[string]any{"name": "a", "scheme": "http", "address": "h:1"}
	for _, c := range []struct {
		m, p string
		b    any
	}{
		{"GET", "/api/proxies/nope", nil}, {"PUT", "/api/proxies/nope", body},
		{"DELETE", "/api/proxies/nope", nil}, {"POST", "/api/proxies/nope/test", nil},
	} {
		resp, data := e.do(admin, c.m, c.p, c.b)
		e.expectError(resp, data, http.StatusNotFound, httpx.CodeProxyNotFound)
	}
}

func TestProxiesValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	resp, data := e.do(admin, "POST", "/api/proxies", map[string]any{"name": "", "scheme": "ftp", "address": "x"})
	er := e.expectError(resp, data, http.StatusBadRequest, httpx.CodeValidationFail)
	fields, _ := er.Error.Details["fields"].(map[string]any)
	for _, k := range []string{"name", "scheme", "address"} {
		if fields[k] == nil {
			t.Errorf("缺少字段错误 %s: %v", k, fields)
		}
	}
	createProxy(t, e, admin, map[string]any{"name": "dup", "scheme": "http", "address": "h:1"})
	resp, data = e.do(admin, "POST", "/api/proxies", map[string]any{"name": "dup", "scheme": "http", "address": "h:2"})
	er = e.expectError(resp, data, http.StatusBadRequest, httpx.CodeValidationFail)
	if f, _ := er.Error.Details["fields"].(map[string]any); f["name"] != model.FieldDuplicate {
		t.Errorf("重名字段错误 = %v", f)
	}
	resp, data = e.do(admin, "POST", "/api/proxies", map[string]any{"name": "x", "bogus": 1})
	e.expectError(resp, data, http.StatusBadRequest, httpx.CodeInvalidJSON)
}

func TestProxiesDeleteInUseAndForce(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	p := createProxy(t, e, admin, map[string]any{"name": "used", "scheme": "http", "address": "h:1"})
	e.refs.refs[p.ID] = []model.ProxyReferrer{{ID: "i1", Name: "天气"}, {ID: "i2", Name: "检测"}}

	resp, data := e.do(admin, "DELETE", "/api/proxies/"+p.ID, nil)
	er := e.expectError(resp, data, http.StatusConflict, httpx.CodeProxyInUse)
	insts, _ := er.Error.Details["instances"].([]any)
	if len(insts) != 2 {
		t.Fatalf("details = %v", er.Error.Details)
	}
	first, _ := insts[0].(map[string]any)
	if first["id"] != "i1" || first["name"] != "天气" {
		t.Errorf("instances[0] = %v", first)
	}
	if resp, _ = e.do(admin, "GET", "/api/proxies/"+p.ID, nil); resp.StatusCode != http.StatusOK {
		t.Fatal("409 后代理应仍在")
	}

	resp, data = e.do(admin, "DELETE", "/api/proxies/"+p.ID+"?force=1", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("force DELETE = %d %s", resp.StatusCode, data)
	}
	if len(e.refs.resets) != 1 || e.refs.resets[0] != p.ID {
		t.Errorf("应把引用改为直连: %v", e.refs.resets)
	}
	resp, data = e.do(admin, "GET", "/api/proxies/"+p.ID, nil)
	e.expectError(resp, data, http.StatusNotFound, httpx.CodeProxyNotFound)
}

func TestProxiesTest(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	fake := proxytest.NewHTTP(t)
	p := createProxy(t, e, admin, map[string]any{"name": "t", "scheme": "http", "address": fake.Addr})

	resp, data := e.do(admin, "POST", "/api/proxies/"+p.ID+"/test", map[string]any{"url": "http://probe.example.invalid/204"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test = %d %s", resp.StatusCode, data)
	}
	var res model.ProxyTestResult
	_ = json.Unmarshal(data, &res)
	if !res.OK || res.Status != 204 || res.URL != "http://probe.example.invalid/204" {
		t.Fatalf("res = %+v", res)
	}
	if s := fake.Seen(); len(s) != 1 || s[0].Target != "http://probe.example.invalid/204" {
		t.Fatalf("代理未收到请求: %+v", s)
	}

	// 失败是 200 + ok=false
	dead := createProxy(t, e, admin, map[string]any{"name": "dead", "scheme": "http", "address": "127.0.0.1:1"})
	resp, data = e.do(admin, "POST", "/api/proxies/"+dead.ID+"/test", map[string]any{"url": "http://x.example.invalid/"})
	_ = json.Unmarshal(data, &res)
	if resp.StatusCode != http.StatusOK || res.OK || res.Error == "" {
		t.Fatalf("失败结果 = %d %s", resp.StatusCode, data)
	}

	// 目标非法 → validation.failed
	resp, data = e.do(admin, "POST", "/api/proxies/"+p.ID+"/test", map[string]any{"url": "ftp://x"})
	e.expectError(resp, data, http.StatusBadRequest, httpx.CodeValidationFail)
}
