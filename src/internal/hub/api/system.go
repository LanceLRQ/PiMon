package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 日志接口的条数：缺省 200，上限为缓冲容量（调用方取不到更多）。
const defaultLogLimit = 200

func (s *server) registerSystem(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system", s.admin(s.getSystem))
	mux.HandleFunc("GET /api/system/logs", s.admin(s.getSystemLogs))
}

func (s *server) getSystem(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.System.Info())
}

// parseLogLevel 解析 ?level=；缺省为 debug（不过滤）。
func parseLogLevel(v string) (slog.Level, bool) {
	switch v {
	case "", "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}

func (s *server) getSystemLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fe := model.FieldErrors{}
	level, ok := parseLogLevel(q.Get("level"))
	if !ok {
		fe["level"] = model.FieldInvalid
	}
	limit := defaultLogLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fe["limit"] = model.FieldInvalid
		} else {
			limit = n
		}
	}
	if len(fe) > 0 {
		httpx.WriteValidationFailed(w, fe)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.System.Logs(level, limit))
}
