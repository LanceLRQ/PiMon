package api

import (
	"errors"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/session", s.session)
	mux.HandleFunc("PUT /api/admin/password", s.admin(s.changePassword))
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	if !s.verifyAdminPassword(w, r, req.Password) {
		return
	}
	if err := s.issueSession(w, r, auth.KindAdmin); err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// verifyAdminPassword 在按客户端 IP 串行的区间内完成"查锁定→argon2 校验→记失败/成功"，
// 登录与改密码共用同一个限流 key。串行化保证并发请求无法在一个锁定周期内超过失败上限。
// 不通过时已写出响应并返回 false。
func (s *server) verifyAdminPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	key := limitKey(auth.KeyLogin, r)
	defer s.keyLocks.lock(key)()

	if d := s.Limiter.Locked(key); d > 0 {
		s.writeLocked(w, r, d)
		return false
	}
	hash, err := s.Admins.PasswordHash(r.Context())
	if errors.Is(err, auth.ErrNoAdmin) {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeSetupRequired, nil)
		return false
	}
	if err != nil {
		internalError(w, r, err)
		return false
	}
	ok, err := s.verify(r.Context(), hash, password)
	if err != nil {
		internalError(w, r, err)
		return false
	}
	if ok {
		s.Limiter.Success(key)
		return true
	}
	remaining := s.Limiter.Fail(key)
	if remaining == 0 {
		s.writeLocked(w, r, s.Limiter.Locked(key))
		return false
	}
	httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeInvalidPassword, map[string]any{"remaining": remaining})
	return false
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if token, _, ok, err := s.currentSession(r); err != nil {
		internalError(w, r, err)
		return
	} else if ok {
		if err := s.Sessions.Delete(r.Context(), token); err != nil {
			internalError(w, r, err)
			return
		}
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	Kind          string `json:"kind,omitempty"`
	NeedsSetup    bool   `json:"needs_setup"`
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	_, kind, ok, err := s.currentSession(r)
	if err != nil {
		internalError(w, r, err)
		return
	}
	exists, err := s.Admins.Exists(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	resp := sessionResponse{Authenticated: ok, NeedsSetup: !exists}
	if ok {
		resp.Kind = string(kind)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req changePasswordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	if passwordTooShort(req.NewPassword) {
		httpx.WriteValidationFailed(w, model.FieldErrors{"new_password": model.FieldOutOfRange})
		return
	}
	if !s.verifyAdminPassword(w, r, req.CurrentPassword) {
		return
	}
	hash, err := s.hash(ctx, req.NewPassword)
	if err != nil {
		internalError(w, r, err)
		return
	}
	// 改密码与吊销所有管理员会话在同一事务内完成，再给当前请求签发新会话。
	if err := s.Admins.ReplacePassword(ctx, hash); err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.issueSession(w, r, auth.KindAdmin); err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
