package api

import (
	"errors"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/history"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerHistory(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/history", s.instanceHistory)
}

// instanceHistory 查询实例某个数据项的数值历史：
// ?item=&field=&range=，field 省略取默认字段，range 为 1h、24h、7d 这样的时长，档位由范围自动选择。
// 管理员会话可查任意实例；屏幕会话只能查当前布局引用的实例（图表小组件取数），否则 403。
func (s *server) instanceHistory(w http.ResponseWriter, r *http.Request) {
	_, kind, ok, err := s.currentSession(r)
	switch {
	case err != nil:
		internalError(w, r, err)
		return
	case !ok:
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
		return
	case kind == auth.KindScreen:
		referenced, err := s.Screens.InstanceReferenced(r.Context(), r.PathValue("id"))
		if err != nil {
			internalError(w, r, err)
			return
		}
		if !referenced {
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeAuthForbidden, nil)
			return
		}
	case kind != auth.KindAdmin:
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeAuthForbidden, nil)
		return
	}
	q := r.URL.Query()
	res, err := s.History.Query(r.Context(), history.Query{
		InstanceID: r.PathValue("id"), Item: q.Get("item"), Field: q.Get("field"), Range: q.Get("range"),
	})
	var fe model.FieldErrors
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusOK, res)
	case errors.Is(err, history.ErrInstanceNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeInstanceNotFound, nil)
	case errors.As(err, &fe):
		httpx.WriteValidationFailed(w, fe)
	default:
		internalError(w, r, err)
	}
}
