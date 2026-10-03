package api

import (
	_ "embed"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/hub/agentdist"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

// installScript 是一行安装脚本模板，@HUB_URL@ 在响应时替换为本次请求的网页地址。
//
//go:embed install.sh.tmpl
var installScript string

var (
	platformRe = regexp.MustCompile(`^(linux|darwin)-(amd64|arm64)$`)
	// 拼进 shell 脚本的 Host 只允许主机名、IPv4/IPv6 字面量与端口用到的字符。
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.:\-\[\]]+$`)
)

// registerAgentDist 注册 agent 的公开分发路由：它们只提供公开的二进制、校验和与安装脚本，
// 不含任何凭据，所以免登录（不经 s.admin）。
func (s *server) registerAgentDist(mux *http.ServeMux) {
	mux.HandleFunc("GET /agent/download/{platform}", s.agentDownload)
	mux.HandleFunc("GET /agent/checksums", s.agentChecksums)
	mux.HandleFunc("GET /agent/install.sh", s.agentInstallScript)
}

func (s *server) agentDownload(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if s.AgentDist == nil || !platformRe.MatchString(platform) {
		writeBinaryUnavailable(w)
		return
	}
	rc, size, err := s.AgentDist.Open(platform)
	if err != nil {
		if errors.Is(err, agentdist.ErrUnavailable) {
			writeBinaryUnavailable(w)
			return
		}
		internalError(w, r, err)
		return
	}
	defer func() { _ = rc.Close() }()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="pimon-agent-`+platform+`"`)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rc)
	}
}

func (s *server) agentChecksums(w http.ResponseWriter, r *http.Request) {
	if s.AgentDist == nil || len(s.AgentDist.Platforms()) == 0 {
		writeBinaryUnavailable(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method != http.MethodHead {
		_, _ = w.Write(s.AgentDist.SumsFile())
	}
}

func (s *server) agentInstallScript(w http.ResponseWriter, r *http.Request) {
	info := httpx.Info(r)
	if !hostRe.MatchString(info.Host) || (info.Scheme != "http" && info.Scheme != "https") {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidationFail, map[string]any{"fields": map[string]any{"host": "invalid"}})
		return
	}
	body := strings.ReplaceAll(installScript, "@HUB_URL@", info.Scheme+"://"+info.Host)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

func writeBinaryUnavailable(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, httpx.CodeAgentBinaryUnavailable, nil)
}
