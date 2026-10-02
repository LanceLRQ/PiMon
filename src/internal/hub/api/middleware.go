package api

import (
	"net/http"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

const (
	screenSessionMaxAge = 10 * 365 * 24 * 3600
	adminSessionMaxAge  = int(auth.AdminSessionTTL / 1e9)
)

// currentSession 从 Cookie 识别当前会话；没有 Cookie 或会话无效时 ok 为 false。
func (s *server) currentSession(r *http.Request) (token string, kind auth.SessionKind, ok bool, err error) {
	c, cerr := r.Cookie(auth.CookieName)
	if cerr != nil || c.Value == "" {
		return "", "", false, nil
	}
	kind, ok, err = s.Sessions.Lookup(r.Context(), c.Value)
	if err != nil || !ok {
		return "", "", false, err
	}
	return c.Value, kind, true, nil
}

// admin 包装需要管理员会话的 handler：无会话 401 auth.required，屏幕会话 403 auth.forbidden。
func (s *server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, kind, ok, err := s.currentSession(r)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
			return
		}
		if kind != auth.KindAdmin {
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeAuthForbidden, nil)
			return
		}
		next(w, r)
	}
}

// setSessionCookie 写会话 Cookie；maxAge 小于 0 表示清除。Secure 取决于解析出的协议。
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   httpx.Info(r).Scheme == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

// issueSession 创建会话并写入 Cookie。
func (s *server) issueSession(w http.ResponseWriter, r *http.Request, kind auth.SessionKind) error {
	token, err := s.Sessions.Create(r.Context(), kind)
	if err != nil {
		return err
	}
	maxAge := adminSessionMaxAge
	if kind == auth.KindScreen {
		maxAge = screenSessionMaxAge
	}
	setSessionCookie(w, r, token, maxAge)
	return nil
}

// limitKey 生成限流键，规则见 httpx.LimitKey。
func limitKey(prefix string, r *http.Request) string { return httpx.LimitKey(prefix, r) }

// writeLocked 回 auth.locked，锁定到期时刻按限流器的时钟计算。
func (s *server) writeLocked(w http.ResponseWriter, r *http.Request, d time.Duration) {
	httpx.WriteLocked(w, r, s.Limiter.Now(), d)
}
