package api

import (
	"log/slog"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/settings"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

// PasswordHasher 是密码哈希与校验；生产环境使用 auth.Hasher。
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
}

// Deps 是 API 层依赖的各服务。新增路由需要的依赖时，在这里加字段，
// 并在 New 中的注册列表里加一行对应的 register 调用。
type Deps struct {
	Settings     *settings.Service
	Hasher       PasswordHasher
	Limiter      *auth.Limiter
	SetupCodes   *auth.SetupCodes
	Admins       *auth.Admins
	Sessions     *auth.Sessions
	ScreenTokens *auth.ScreenTokens
	Backups      *backup.Service
	Proxies      *proxies.Store
	Plugins      *plugins.Registry
}

// argonConcurrency 是同时进行的 argon2 运算上限，避免 64MiB×N 耗尽树莓派内存。
const argonConcurrency = 2

type server struct {
	Deps
	keyLocks *keyedMutex
	gate     *gate
}

// New 返回完整的路由（含请求来源解析与写方法 Origin 校验中间件）。
func New(d Deps) http.Handler {
	s := &server{Deps: d, keyLocks: newKeyedMutex(), gate: newGate(argonConcurrency)}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)
	s.registerSetup(mux)
	s.registerAuth(mux)
	s.registerSettings(mux)
	s.registerScreen(mux)
	s.registerBackup(mux)
	s.registerProxies(mux)
	s.registerPlugins(mux)
	// 后续路由在此追加；需要管理员权限的用 s.admin(...) 包装。

	h := httpx.RequireSameOrigin(mux)
	return httpx.WithRequestInfo(d.Settings.TrustedNets)(h)
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

// internalError 记录内部错误（不含任何敏感值）并回 500 internal。
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("处理请求失败", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, nil)
}
