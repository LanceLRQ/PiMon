package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup/remote"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// registerBackupRemote 注册远端备份的 4 组路由（管理员）。字面量路径 /api/backups/remote 比 /api/backups/{name} 更具体，
// 不会冲突，合法的本地备份名也不可能叫 remote。
func (s *server) registerBackupRemote(mux *http.ServeMux) {
	if s.RemoteBackups == nil {
		return
	}
	mux.HandleFunc("GET /api/backups/remote", s.admin(s.getBackupRemote))
	mux.HandleFunc("PUT /api/backups/remote", s.admin(s.putBackupRemote))
	mux.HandleFunc("POST /api/backups/remote/test", s.admin(s.testBackupRemote))
	mux.HandleFunc("POST /api/backups/remote/upload", s.admin(s.uploadBackupRemote))
	mux.HandleFunc("GET /api/backups/remote/uploads", s.admin(s.listBackupUploads))
}

func (s *server) getBackupRemote(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.RemoteBackups.Get(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, cfg)
}

// putBackupRemote 保存配置。密钥留空保留原值；响应的 passphrase_changed 为真时前端提示「口令丢失将无法恢复已加密的备份」。
func (s *server) putBackupRemote(w http.ResponseWriter, r *http.Request) {
	var in model.BackupRemoteInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	cfg, err := s.RemoteBackups.Put(r.Context(), in)
	if err != nil {
		writeRemoteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, cfg)
}

// testBackupRemote 用请求体中的配置测试连接（未保存也可测，密钥留空用已保存的值）。
func (s *server) testBackupRemote(w http.ResponseWriter, r *http.Request) {
	var in model.BackupRemoteInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	res, err := s.RemoteBackups.Test(r.Context(), in)
	if err != nil {
		writeRemoteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// uploadBackupRemote 立即上传最新的本地备份并同步返回上传记录。上传跟随服务生命周期而非请求：
// 客户端或反代断开时上传继续并照常记录，此时无需（也无法）再写响应。
func (s *server) uploadBackupRemote(w http.ResponseWriter, r *http.Request) {
	rec, err := s.RemoteBackups.UploadNow(r.Context())
	if err != nil {
		if r.Context().Err() != nil && errors.Is(err, context.Canceled) {
			return
		}
		writeRemoteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (s *server) listBackupUploads(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			httpx.WriteValidationFailed(w, model.FieldErrors{"limit": model.FieldInvalid})
			return
		}
		limit = n
	}
	list, err := s.RemoteBackups.Uploads(r.Context(), limit)
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// writeRemoteError 把远端备份服务的错误映射为 API 错误码。*remote.Error 的消息已脱敏，不含密码与口令。
func writeRemoteError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		fe model.FieldErrors
		re *remote.Error
	)
	switch {
	case errors.As(err, &fe):
		httpx.WriteValidationFailed(w, fe)
	case errors.Is(err, remote.ErrDisabled):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeBackupRemoteDisabled, nil)
	case errors.Is(err, remote.ErrNoLocal):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeBackupNoLocal, nil)
	case errors.Is(err, remote.ErrShuttingDown):
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeShuttingDown, nil)
	case errors.As(err, &re):
		httpx.WriteError(w, http.StatusBadGateway, httpx.CodeBackupRemoteFailed,
			map[string]any{"reason": string(re.Reason), "message": re.Message})
	default:
		internalError(w, r, err)
	}
}
