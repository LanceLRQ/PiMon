package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

// 品牌标识：数据目录 logos/<plugin_id>.svg|png 可为插件提供自定义标识。
// 本期不内置任何第三方 logo，文件不存在时前端回落到字标徽章。

// logoMaxBytes 是单个标识文件的大小上限，超过按不存在处理。
const logoMaxBytes = 1 << 20

// logoIDPattern 限定 plugin_id 的字符集：小写字母、数字、连字符与下划线，首字符为字母或数字。
var logoIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func (s *server) registerLogos(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logos/{plugin_id}", s.getLogo)
}

// getLogo 返回插件的自定义标识，管理员会话与屏幕会话都可读。
// SVG 响应带 CSP sandbox，即使被直接打开也不会执行脚本或加载外部资源。
func (s *server) getLogo(w http.ResponseWriter, r *http.Request) {
	_, kind, ok, err := s.currentSession(r)
	switch {
	case err != nil:
		internalError(w, r, err)
		return
	case !ok:
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
		return
	case kind != auth.KindAdmin && kind != auth.KindScreen:
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeAuthForbidden, nil)
		return
	}
	id := r.PathValue("plugin_id")
	if s.DataDir == "" || !logoIDPattern.MatchString(id) {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
		return
	}
	dir := filepath.Join(s.DataDir, "logos")
	for _, f := range []struct{ ext, mime string }{{".svg", "image/svg+xml"}, {".png", "image/png"}} {
		data, found := readLogoFile(filepath.Join(dir, id+f.ext))
		if !found {
			continue
		}
		h := w.Header()
		h.Set("Content-Type", f.mime)
		h.Set("Cache-Control", "private, max-age=300")
		if f.ext == ".svg" {
			h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
}

// readLogoFile 只读取普通文件（拒绝符号链接与目录）且不超过大小上限。
func readLogoFile(path string) ([]byte, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > logoMaxBytes {
		return nil, false
	}
	f, err := os.Open(path) //nolint:gosec // 路径由校验过的 plugin_id 与固定目录拼成
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, logoMaxBytes+1))
	if err != nil || len(data) > logoMaxBytes {
		return nil, false
	}
	return data, true
}
