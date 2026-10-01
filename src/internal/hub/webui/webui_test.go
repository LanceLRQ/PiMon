package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func fakeDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte(`<meta name="pimon-build" content="__PIMON_BUILD__"><div id="root"></div>`)},
		"favicon.svg":       {Data: []byte("<svg/>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
	}
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestIndexReplacesBuildAndNoCache(t *testing.T) {
	h := New(fakeDist(), "v1.2.3")
	rec := get(h, "GET", "/")
	if rec.Code != 200 {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `content="v1.2.3"`) || strings.Contains(body, "__PIMON_BUILD__") {
		t.Fatalf("占位符未替换: %s", body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index 缓存头 %q", cc)
	}
}

func TestDeepLinkFallsBackToIndex(t *testing.T) {
	h := New(fakeDist(), "b")
	for _, p := range []string{"/instances/3", "/settings", "/index.html", "/some/deep/link"} {
		rec := get(h, "GET", p)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("%s 未回退到 index: %d", p, rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s 缓存头不对", p)
		}
	}
}

func TestHeadOnIndex(t *testing.T) {
	rec := get(New(fakeDist(), "b"), "HEAD", "/x")
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD 应 200 且无 body: %d %d", rec.Code, rec.Body.Len())
	}
}

func TestStaticAssetsCache(t *testing.T) {
	h := New(fakeDist(), "b")
	rec := get(h, "GET", "/assets/app-abc.js")
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("资源应原样返回: %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("资源缓存头 %q", cc)
	}
	rec = get(h, "GET", "/favicon.svg")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("根目录静态文件应 no-cache: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestMissingAssetIs404NotIndex(t *testing.T) {
	rec := get(New(fakeDist(), "b"), "GET", "/assets/gone-123.js")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), `id="root"`) {
		t.Fatalf("丢失的哈希资源应 404: %d", rec.Code)
	}
}

func TestReservedPathsAreJSON404(t *testing.T) {
	h := New(fakeDist(), "b")
	for _, p := range []string{"/api/nope", "/api", "/ws", "/healthz/x", "/screen/auth/y"} {
		rec := get(h, "GET", p)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Fatalf("%s 应 JSON 404: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestNonGetIsNotIndex(t *testing.T) {
	rec := get(New(fakeDist(), "b"), "POST", "/instances")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), `id="root"`) {
		t.Fatalf("POST 不应返回 index: %d", rec.Code)
	}
}

func TestPathTraversalStaysInside(t *testing.T) {
	rec := get(New(fakeDist(), "b"), "GET", "/assets/../index.html")
	if rec.Code != 200 {
		t.Fatalf("清理后应得 index: %d", rec.Code)
	}
}

func TestMissingIndexShowsNotice(t *testing.T) {
	h := New(fstest.MapFS{}, "b")
	rec := get(h, "GET", "/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "make web") || !strings.Contains(body, "前端未构建") || !strings.Contains(body, "not been built") {
		t.Fatalf("说明页不对: %d %s", rec.Code, body)
	}
}

func TestEmbeddedCompiles(t *testing.T) {
	if Embedded() == nil {
		t.Fatal("Embedded 不应为 nil")
	}
}

func TestScreenServiceWorkerScript(t *testing.T) {
	dist := fakeDist()
	dist["screen/sw.js"] = &fstest.MapFile{Data: []byte("self.addEventListener('fetch', () => {})")}
	h := New(dist, "b")
	rec := get(h, "GET", "/screen/sw.js")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "addEventListener") {
		t.Fatalf("sw.js 应原样返回而不是回退到 index: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("sw.js Content-Type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("sw.js 必须每次校验，缓存头 %q", cc)
	}
	// 脚本在 /screen/ 下，要把作用域放宽到 /screen（不带斜杠）必须有这个头
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Fatalf("Service-Worker-Allowed %q", got)
	}
	// 其它静态文件不带该头
	if get(h, "GET", "/favicon.svg").Header().Get("Service-Worker-Allowed") != "" {
		t.Fatal("只有 sw.js 允许放宽作用域")
	}
}
