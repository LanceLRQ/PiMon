package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	contentTypeJSON = "application/json; charset=utf-8"
	maxBodyBytes    = 1 << 20
)

// ErrInvalidJSON 表示请求体无法解码（语法错误、未知字段、尾随内容或超过大小上限）。
// 调用方用 errors.Is 判断后回 WriteInvalidJSON。
var ErrInvalidJSON = errors.New("invalid json body")

// WriteJSON 以 JSON 写出响应。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Details map[string]any `json:"details"`
}

// WriteError 写出统一格式的错误响应；details 为空时输出 {}。
func WriteError(w http.ResponseWriter, status int, code string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	WriteJSON(w, status, errorBody{Error: errorPayload{Code: code, Details: details}})
}

// WriteInvalidJSON 回 400 request.invalid_json。
func WriteInvalidJSON(w http.ResponseWriter) {
	WriteError(w, http.StatusBadRequest, CodeInvalidJSON, nil)
}

// WriteValidationFailed 回 400 validation.failed，details.fields 为字段错误。
func WriteValidationFailed(w http.ResponseWriter, fields model.FieldErrors) {
	WriteError(w, http.StatusBadRequest, CodeValidationFail, map[string]any{"fields": fields})
}

// SetRetryAfter 设置 Retry-After 头（秒数向上取整且至少为 1），返回所设秒数。
func SetRetryAfter(w http.ResponseWriter, d time.Duration) int {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	return secs
}

// WriteLocked 回 429 auth.locked，并设置 Retry-After；秒数向上取整且至少为 1。
// details 带锁定到期时刻 locked_until（RFC 3339，UTC，与秒数一致）和请求者自己的来源 IP
// client_ip（按受信任反代规则得出，取自 RequestInfo）。
func WriteLocked(w http.ResponseWriter, r *http.Request, now time.Time, retryAfter time.Duration) {
	secs := SetRetryAfter(w, retryAfter)
	details := map[string]any{
		"retry_after_seconds": secs,
		"locked_until":        now.Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339),
	}
	if ip := Info(r).ClientIP; ip.IsValid() {
		details["client_ip"] = ip.String()
	}
	WriteError(w, http.StatusTooManyRequests, CodeAuthLocked, details)
}

// DecodeJSON 解码请求体到 dst：上限 1 MiB，拒绝未知字段和尾随的多余 JSON。
// 任何失败都返回可用 errors.Is(err, ErrInvalidJSON) 识别的错误。
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: 请求体含有多余内容", ErrInvalidJSON)
	}
	return nil
}

// SecurityHeaders 给所有响应加基础安全头：禁止 MIME 嗅探、禁止被嵌入框架、引荐来源只在同源下带出。
// 不含 CSP：页面有内联防闪烁脚本，CSP 留待后续统一设计。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
