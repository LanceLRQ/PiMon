// Package webui 把前端构建产物经 go:embed 打包进 hub，并以 SPA 方式提供：
// 文件存在则直接返回，否则回退到 index.html 交给前端路由。
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

// all: 前缀让 dist 里的 .gitkeep 也被收录，这样未构建前端时目录仍存在、Go 侧可编译。
//
//go:embed all:dist
var embedded embed.FS

// buildPlaceholder 是 index.html 里的 build 版本占位符，返回 index 时替换。
const buildPlaceholder = "__PIMON_BUILD__"

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNoCache   = "no-cache"
)

// notBuiltPage 是 index.html 缺失（前端未构建）时的说明页。
const notBuiltPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>PiMon</title>
</head>
<body style="font-family: sans-serif; max-width: 40rem; margin: 3rem auto; padding: 0 1rem;">
<h1>PiMon</h1>
<p>前端未构建，请运行 <code>make web</code>。</p>
<p>The web UI has not been built. Please run <code>make web</code>.</p>
</body>
</html>
`

// Embedded 返回打包进二进制的前端目录（dist 的内容作为根）。
func Embedded() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// dist 由 go:embed 保证存在，不可能走到这里。
		panic(err)
	}
	return sub
}

// reservedPrefixes 是不归前端处理的路径前缀；它们未匹配到处理器时应返回 JSON 404。
var reservedPrefixes = []string{"/api", "/ws", "/healthz", "/screen/auth"}

func reserved(p string) bool {
	for _, pre := range reservedPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// New 返回 SPA 处理器。build 是写入 index.html 占位符的版本号，替换结果只算一次。
func New(fsys fs.FS, build string) http.Handler {
	h := &spa{fsys: fsys}
	if raw, err := fs.ReadFile(fsys, "index.html"); err == nil {
		h.index = bytes.ReplaceAll(raw, []byte(buildPlaceholder), []byte(build))
	} else {
		h.index = []byte(notBuiltPage)
	}
	return h
}

type spa struct {
	fsys  fs.FS
	index []byte
}

func (h *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := path.Clean("/" + r.URL.Path)
	if reserved(p) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
		return
	}
	name := strings.TrimPrefix(p, "/")
	if name != "" && name != "index.html" {
		if data, err := fs.ReadFile(h.fsys, name); err == nil {
			h.serveFile(w, r, name, data)
			return
		}
		// 带哈希的资源丢失说明页面版本过旧，返回 HTML 会让浏览器把它当脚本解析，直接 404。
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	h.serveIndex(w, r)
}

func (h *spa) serveFile(w http.ResponseWriter, r *http.Request, name string, data []byte) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		// 根目录静态文件（favicon 等）文件名不带哈希，每次校验以便升级后立即生效。
		w.Header().Set("Cache-Control", cacheNoCache)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (h *spa) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheNoCache)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.index))
}
