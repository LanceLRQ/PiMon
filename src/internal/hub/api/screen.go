package api

import (
	"log/slog"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerScreen(mux *http.ServeMux) {
	mux.HandleFunc("GET /screen/auth", s.screenAuth)
	mux.HandleFunc("POST /api/screen/token/reset", s.admin(s.resetScreenToken))
}

// screenAuth 用屏幕令牌换取屏幕会话 Cookie 并跳转到 /screen；错误与锁定回纯文本。
func (s *server) screenAuth(w http.ResponseWriter, r *http.Request) {
	key := limitKey(auth.KeyScreen, r)
	defer s.keyLocks.lock(key)()

	if d := s.Limiter.Locked(key); d > 0 {
		httpx.SetRetryAfter(w, d)
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	ok, err := s.ScreenTokens.Verify(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		if s.Limiter.Fail(key) == 0 {
			httpx.SetRetryAfter(w, s.Limiter.Locked(key))
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.Limiter.Success(key)
	if err := s.issueSession(w, r, auth.KindScreen); err != nil {
		internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/screen", http.StatusFound)
}

// resetScreenToken 轮换屏幕令牌（同时使旧屏幕会话失效）；响应体不含令牌。
func (s *server) resetScreenToken(w http.ResponseWriter, r *http.Request) {
	if err := s.ScreenTokens.Rotate(r.Context()); err != nil {
		internalError(w, r, err)
		return
	}
	// 事务已提交，旧屏幕会话已不存在：让屏幕端的 WebSocket 立即复核并断开。
	s.Sessions.NotifyRevoked()
	// 操作记录写失败不影响令牌已轮换的事实，只记日志。
	if _, err := s.ScreenState.RecordOp(r.Context(), model.ScreenActionTokenReset, nil, httpx.Info(r).ClientIP.String()); err != nil {
		slog.Error("记录屏幕令牌重置失败", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}
