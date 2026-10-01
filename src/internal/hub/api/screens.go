package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerScreens(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/screens", s.admin(s.getLayout))
	mux.HandleFunc("PUT /api/screens", s.admin(s.saveLayout))
	mux.HandleFunc("POST /api/screens/rollback", s.admin(s.rollbackLayout))
	mux.HandleFunc("GET /api/screens/versions", s.admin(s.listLayoutVersions))
	mux.HandleFunc("GET /api/screens/versions/{version}", s.admin(s.getLayoutVersion))
	mux.HandleFunc("GET /api/screens/catalog", s.admin(s.getWidgetCatalog))
	mux.HandleFunc("GET /api/screens/resolved", s.admin(s.getResolvedLayout))
}

// writeLayoutError 把布局服务的错误映射为 HTTP 响应；无法识别的走 500。
func writeLayoutError(w http.ResponseWriter, r *http.Request, err error) {
	var inv *screens.InvalidError
	var conflict *screens.ConflictError
	switch {
	case errors.As(err, &inv):
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeLayoutInvalid, map[string]any{"problems": inv.Problems})
	case errors.As(err, &conflict):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeLayoutConflict, map[string]any{"latest_version": conflict.Latest})
	case errors.Is(err, screens.ErrVersionNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
	default:
		internalError(w, r, err)
	}
}

func (s *server) getLayout(w http.ResponseWriter, r *http.Request) {
	st, err := s.Screens.Current(r.Context())
	if err != nil {
		writeLayoutError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (s *server) saveLayout(w http.ResponseWriter, r *http.Request) {
	var req model.LayoutSaveRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	st, err := s.Screens.Save(r.Context(), req.BaseVersion, req.Layout, screens.SaveOptions{Source: model.LayoutSourceEdit})
	if err != nil {
		writeLayoutError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (s *server) rollbackLayout(w http.ResponseWriter, r *http.Request) {
	var req model.LayoutRollbackRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	st, err := s.Screens.Rollback(r.Context(), req.Version)
	if err != nil {
		writeLayoutError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (s *server) listLayoutVersions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Screens.Versions(r.Context())
	if err != nil {
		writeLayoutError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (s *server) getLayoutVersion(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
		return
	}
	st, err := s.Screens.Version(r.Context(), v)
	if err != nil {
		writeLayoutError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (s *server) getWidgetCatalog(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.Screens.Catalog())
}

// getResolvedLayout 返回解析后的当前布局（占位已绑定、带模板与展示状态），?lang=zh|en 决定标题语言。
func (s *server) getResolvedLayout(w http.ResponseWriter, r *http.Request) {
	res, err := s.Screens.Resolve(r.Context(), s.requestLang(r))
	if err != nil {
		writeLayoutError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
