package api

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/settings"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/setup", s.setup)
}

func (s *server) setupStatus(w http.ResponseWriter, r *http.Request) {
	exists, err := s.Admins.Exists(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"needs_setup": !exists})
}

type setupRequest struct {
	SetupCode string `json:"setup_code"`
	Password  string `json:"password"`
	Language  string `json:"language"`
	Timezone  string `json:"timezone"`
	AccessURL string `json:"access_url"`
}

// passwordTooShort 按字符（rune）数判断密码是否不足最小长度。
func passwordTooShort(p string) bool { return utf8.RuneCountInString(p) < auth.MinPasswordLen }

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exists, err := s.Admins.Exists(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if exists {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeSetupDone, nil)
		return
	}
	var req setupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}

	if !s.checkSetupCode(w, r, req.SetupCode) {
		return
	}

	// 密码与设置字段一并校验，一次返回全部字段错误。
	cur := s.Settings.Get()
	if req.Language != "" {
		cur.Language = req.Language
	}
	if req.Timezone != "" {
		cur.Timezone = req.Timezone
	}
	if req.AccessURL != "" {
		cur.AccessURL = req.AccessURL
	}
	fe := model.FieldErrors{}
	if passwordTooShort(req.Password) {
		fe["password"] = model.FieldOutOfRange
	}
	if verr := settings.Validate(cur); verr != nil {
		var sfe model.FieldErrors
		if !errors.As(verr, &sfe) {
			internalError(w, r, verr)
			return
		}
		for k, v := range sfe {
			fe[k] = v
		}
	}
	if len(fe) > 0 {
		httpx.WriteValidationFailed(w, fe)
		return
	}

	hash, err := s.hash(ctx, req.Password)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.Admins.Create(ctx, hash); err != nil {
		if errors.Is(err, auth.ErrAdminExists) {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeSetupDone, nil)
			return
		}
		internalError(w, r, err)
		return
	}
	if err := s.SetupCodes.Consume(ctx); err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.Settings.Update(ctx, cur); err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.issueSession(w, r, auth.KindAdmin); err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// checkSetupCode 在按客户端 IP 串行的区间内完成"查锁定→校验设置码→记失败"；
// 不通过时已写出响应并返回 false。
func (s *server) checkSetupCode(w http.ResponseWriter, r *http.Request, code string) bool {
	key := limitKey(auth.KeySetup, r)
	defer s.keyLocks.lock(key)()

	if d := s.Limiter.Locked(key); d > 0 {
		httpx.WriteLocked(w, d)
		return false
	}
	ok, err := s.SetupCodes.Verify(r.Context(), code)
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
		httpx.WriteLocked(w, s.Limiter.Locked(key))
		return false
	}
	httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeInvalidSetup, map[string]any{"remaining": remaining})
	return false
}
