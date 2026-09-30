package api

import (
	"errors"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

func (s *server) registerBackup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backups", s.admin(s.listBackups))
	mux.HandleFunc("POST /api/backups", s.admin(s.createBackup))
	mux.HandleFunc("GET /api/backups/{name}", s.admin(s.downloadBackup))
}

func (s *server) listBackups(w http.ResponseWriter, r *http.Request) {
	list, err := s.Backups.List()
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (s *server) createBackup(w http.ResponseWriter, r *http.Request) {
	info, err := s.Backups.Create(r.Context(), backup.ReasonManual)
	if errors.Is(err, backup.ErrExists) {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeBackupExists, nil)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, info)
}

// downloadBackup 下载备份包。备份包内含密钥文件，故仅管理员可取。
func (s *server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	f, info, err := s.Backups.Open(r.PathValue("name"))
	if errors.Is(err, backup.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, nil)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+info.Name+`"`)
	http.ServeContent(w, r, info.Name, info.CreatedAt, f)
}
