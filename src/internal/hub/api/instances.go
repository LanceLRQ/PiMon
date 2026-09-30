package api

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

func (s *server) registerInstances(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances", s.admin(s.listInstances))
	mux.HandleFunc("POST /api/instances", s.admin(s.createInstance))
	mux.HandleFunc("GET /api/instances/{id}", s.admin(s.getInstance))
	mux.HandleFunc("PUT /api/instances/{id}", s.admin(s.updateInstance))
	mux.HandleFunc("DELETE /api/instances/{id}", s.admin(s.deleteInstance))
	mux.HandleFunc("POST /api/instances/{id}/run", s.admin(s.runInstance))
	mux.HandleFunc("POST /api/instances/{id}/pause", s.admin(s.pauseInstance))
	mux.HandleFunc("POST /api/instances/{id}/resume", s.admin(s.resumeInstance))
	mux.HandleFunc("POST /api/instances/{id}/copy", s.admin(s.copyInstance))
}

// writeInstanceError 把实例服务的错误映射为 HTTP 响应；无法识别的走 500。
// 采集错误（run.*）的文字已由运行时脱敏，放进 details.message 供前端展示。
func writeInstanceError(w http.ResponseWriter, r *http.Request, err error) {
	var fe model.FieldErrors
	switch {
	case errors.Is(err, instances.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeInstanceNotFound, nil)
	case errors.Is(err, instances.ErrPluginNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodePluginNotFound, nil)
	case errors.As(err, &fe):
		httpx.WriteValidationFailed(w, fe)
	case errors.Is(err, instances.ErrRunBusy):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeRunBusy, nil)
	case errors.Is(err, runtime.ErrTimeout):
		httpx.WriteError(w, http.StatusGatewayTimeout, httpx.CodeRunTimeout, map[string]any{"message": err.Error()})
	case errors.Is(err, runtime.ErrFailed):
		httpx.WriteError(w, http.StatusBadGateway, httpx.CodeRunFailed, map[string]any{"message": err.Error()})
	default:
		internalError(w, r, err)
	}
}

func (s *server) listInstances(w http.ResponseWriter, r *http.Request) {
	list, err := s.Instances.List(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (s *server) getInstance(w http.ResponseWriter, r *http.Request) {
	d, err := s.Instances.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (s *server) createInstance(w http.ResponseWriter, r *http.Request) {
	var in model.InstanceInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	d, err := s.Instances.Create(r.Context(), in)
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (s *server) updateInstance(w http.ResponseWriter, r *http.Request) {
	var in model.InstanceInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	d, err := s.Instances.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// deleteInstance 删除实例。有 screen 引用且没带 ?confirm=1 时回 409 并列出受影响的 screen，
// 供前端二次确认；本期 screens 表还不存在，恒无影响，直接删除。
func (s *server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	affected, err := s.Instances.AffectedScreens(r.Context(), id)
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	if len(affected) > 0 && r.URL.Query().Get("confirm") != "1" {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeInstanceInUse, map[string]any{"screens": affected})
		return
	}
	res, err := s.Instances.Delete(r.Context(), id)
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// runInstance 是「保存并测试」：同步运行一次，把报告作为响应返回。
func (s *server) runInstance(w http.ResponseWriter, r *http.Request) {
	res, err := s.Instances.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (s *server) pauseInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.Instances.Pause(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, inst)
}

func (s *server) resumeInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.Instances.Resume(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, inst)
}

// copyInstance 复制实例，名称加上按语言的「副本」后缀；密钥置空，需重新填写。
func (s *server) copyInstance(w http.ResponseWriter, r *http.Request) {
	src, err := s.Instances.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	suffix := " (副本)"
	if s.requestLang(r) == "en" {
		suffix = " (copy)"
	}
	name := truncateRunes(src.Name, 100-utf8.RuneCountInString(suffix)) + suffix
	d, err := s.Instances.Copy(r.Context(), src.ID, name)
	if err != nil {
		writeInstanceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
