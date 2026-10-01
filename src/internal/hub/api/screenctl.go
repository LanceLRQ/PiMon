package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screenstate"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerScreenCtl(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/schedule", s.admin(s.getSchedule))
	mux.HandleFunc("PUT /api/schedule", s.admin(s.putSchedule))
	mux.HandleFunc("GET /api/screen/status", s.admin(s.getScreenStatus))
	mux.HandleFunc("POST /api/screen/control", s.admin(s.screenControl))
	mux.HandleFunc("GET /api/screen/ops", s.admin(s.listScreenOps))
	mux.HandleFunc("GET /api/screen/setup-code", s.revealSetupCode)
}

func (s *server) getSchedule(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.ScreenState.Schedule())
}

func (s *server) putSchedule(w http.ResponseWriter, r *http.Request) {
	var req model.Schedule
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	if err := s.ScreenState.SetSchedule(r.Context(), req); err != nil {
		var inv *screenstate.InvalidError
		if errors.As(err, &inv) {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeScheduleInvalid, map[string]any{"problems": inv.Problems})
			return
		}
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.ScreenState.Schedule())
}

func (s *server) getScreenStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.ScreenState.Status()
	if st.Viewport != nil {
		g := screens.RecommendGrid(*st.Viewport)
		st.RecommendedGrid = &g
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (s *server) screenControl(w http.ResponseWriter, r *http.Request) {
	var req model.ScreenControlRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	// switch 的目标必须是当前布局里存在的 screen。
	if req.Action == model.ScreenActionSwitch && req.ScreenID != "" {
		st, err := s.Screens.Current(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		if !slices.ContainsFunc(st.Layout.Screens, func(sc model.LayoutScreen) bool { return sc.ID == req.ScreenID }) {
			httpx.WriteValidationFailed(w, model.FieldErrors{"screen_id": model.FieldInvalid})
			return
		}
	}
	resp, err := s.ScreenState.Control(r.Context(), req, httpx.Info(r).ClientIP.String())
	if err != nil {
		var fe model.FieldErrors
		if errors.As(err, &fe) {
			httpx.WriteValidationFailed(w, fe)
			return
		}
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *server) listScreenOps(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	ops, err := s.ScreenState.Ops(r.Context(), limit)
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ops)
}

// revealSetupCode 向屏幕会话显示明文设置码，让没有键盘的显示器也能完成首次设置；
// 仅屏幕会话可调：已有管理员 409 setup.already_done，没有有效且可显示的设置码 404。
func (s *server) revealSetupCode(w http.ResponseWriter, r *http.Request) {
	// 响应含明文设置码，不允许任何缓存。
	w.Header().Set("Cache-Control", "no-store")
	_, kind, ok, err := s.currentSession(r)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
		return
	}
	if kind != auth.KindScreen {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeAuthForbidden, nil)
		return
	}
	exists, err := s.Admins.Exists(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	if exists {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeSetupDone, nil)
		return
	}
	code, exp, found, err := s.SetupCodes.Reveal(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, model.SetupCodeReveal{Code: code, ExpiresAt: exp})
}
