package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const svgBody = `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8"/></svg>`

func writeLogo(t *testing.T, e *env, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(e.deps.DataDir, "logos")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o640); err != nil {
		t.Fatal(err)
	}
}

func screenClient(e *env) *http.Client {
	sc := e.newClient()
	if resp, _ := e.do(sc, "GET", "/screen/auth?token="+e.screenToken(), nil); resp.StatusCode != http.StatusFound {
		e.t.Fatalf("屏幕会话建立失败: %d", resp.StatusCode)
	}
	return sc
}

func TestLogoRequiresSession(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do(e.newClient(), "GET", "/api/logos/demo", nil)
	e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
}

func TestLogoServesSVGWithSandboxHeaders(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	writeLogo(t, e, "demo.svg", []byte(svgBody))
	for name, c := range map[string]*http.Client{"管理员": admin, "屏幕": screenClient(e)} {
		resp, data := e.do(c, "GET", "/api/logos/demo", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: 状态码 = %d %s", name, resp.StatusCode, data)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
			t.Fatalf("%s: Content-Type = %q", name, ct)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "sandbox; default-src 'none'" {
			t.Fatalf("%s: CSP = %q", name, csp)
		}
		if string(data) != svgBody {
			t.Fatalf("%s: 内容不一致", name)
		}
	}
}

func TestLogoServesPNG(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	png := []byte("\x89PNG\r\n\x1a\nxxxx")
	writeLogo(t, e, "demo.png", png)
	resp, data := e.do(admin, "GET", "/api/logos/demo", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || string(data) != string(png) {
		t.Fatalf("PNG 响应异常: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestLogoNotFound(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	resp, data := e.do(admin, "GET", "/api/logos/missing", nil)
	e.expectError(resp, data, http.StatusNotFound, "not_found")
}

func TestLogoRejectsTraversalAndBadIDs(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	// 数据目录里 logos 之外的文件不能被读到
	if err := os.WriteFile(filepath.Join(e.deps.DataDir, "secret.svg"), []byte(svgBody), 0o640); err != nil {
		t.Fatal(err)
	}
	writeLogo(t, e, "demo.svg", []byte(svgBody))
	for _, id := range []string{"..%2Fsecret", "%2E%2E%2Fsecret", "..", "a%2Fb", "a%5Cb", "Demo", "demo.svg", "demo%00", "-demo", "a b"} {
		resp, data := e.do(admin, "GET", "/api/logos/"+id, nil)
		// 路径里的 .. 会被路由器规整并重定向（307），其余一律 404；任何情况都不能读到文件
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("%q: 状态码 = %d %s", id, resp.StatusCode, data)
		}
	}
}

func TestLogoRejectsSymlink(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	target := filepath.Join(e.deps.DataDir, "outside.svg")
	if err := os.WriteFile(target, []byte(svgBody), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.deps.DataDir, "logos"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(e.deps.DataDir, "logos", "link.svg")); err != nil {
		t.Skip("不支持符号链接")
	}
	resp, data := e.do(admin, "GET", "/api/logos/link", nil)
	e.expectError(resp, data, http.StatusNotFound, "not_found")
}
