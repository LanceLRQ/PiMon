package api

import (
	"errors"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.admin(s.getSettings))
	mux.HandleFunc("PUT /api/settings", s.admin(s.putSettings))
}

func (s *server) getSettings(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.Settings.Get())
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req model.Settings
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	if err := s.Settings.Update(r.Context(), req); err != nil {
		var fe model.FieldErrors
		if errors.As(err, &fe) {
			httpx.WriteValidationFailed(w, fe)
			return
		}
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.Settings.Get())
}
