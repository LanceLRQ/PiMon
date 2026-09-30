package api

import (
	"errors"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func (s *server) registerProxies(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/proxies", s.admin(s.listProxies))
	mux.HandleFunc("POST /api/proxies", s.admin(s.createProxy))
	mux.HandleFunc("GET /api/proxies/{id}", s.admin(s.getProxy))
	mux.HandleFunc("PUT /api/proxies/{id}", s.admin(s.updateProxy))
	mux.HandleFunc("DELETE /api/proxies/{id}", s.admin(s.deleteProxy))
	mux.HandleFunc("POST /api/proxies/{id}/test", s.admin(s.testProxy))
}

// writeProxyError 把仓库错误映射为 HTTP 响应；无法识别的走 500。
func writeProxyError(w http.ResponseWriter, r *http.Request, err error) {
	var fe model.FieldErrors
	var inUse *proxies.InUseError
	switch {
	case errors.Is(err, proxies.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeProxyNotFound, nil)
	case errors.As(err, &fe):
		httpx.WriteValidationFailed(w, fe)
	case errors.As(err, &inUse):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeProxyInUse,
			map[string]any{"instances": inUse.Referrers})
	default:
		internalError(w, r, err)
	}
}

func (s *server) listProxies(w http.ResponseWriter, r *http.Request) {
	list, err := s.Proxies.List(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (s *server) getProxy(w http.ResponseWriter, r *http.Request) {
	p, err := s.Proxies.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeProxyError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *server) createProxy(w http.ResponseWriter, r *http.Request) {
	var in model.ProxyInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	p, err := s.Proxies.Create(r.Context(), in)
	if err != nil {
		writeProxyError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (s *server) updateProxy(w http.ResponseWriter, r *http.Request) {
	var in model.ProxyInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteInvalidJSON(w)
		return
	}
	p, err := s.Proxies.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeProxyError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// deleteProxy 删除代理；被引用时回 409，带 ?force=1 则先把引用改为直连再删除。
func (s *server) deleteProxy(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	if err := s.Proxies.Delete(r.Context(), r.PathValue("id"), force); err != nil {
		writeProxyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testProxy 经代理请求目标地址；请求失败也回 200，由 ok 与 error 字段表达。
func (s *server) testProxy(w http.ResponseWriter, r *http.Request) {
	var req model.ProxyTestRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			httpx.WriteInvalidJSON(w)
			return
		}
	}
	res, err := s.Proxies.Test(r.Context(), r.PathValue("id"), req.URL)
	if err != nil {
		writeProxyError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
