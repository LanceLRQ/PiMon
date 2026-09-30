// Package httpx 解析请求来源（真实 IP、协议、Host）、校验 Origin，并提供统一的 JSON 与错误响应。
package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RequestInfo 是经受信任反代规则解析后的请求来源。
type RequestInfo struct {
	ClientIP netip.Addr
	Scheme   string
	Host     string
}

type infoKey struct{}

// Resolve 解析请求来源。只有对端地址落在 trusted 网段内时才采信 X-Forwarded-*；
// 不处理 RFC 7239 的 Forwarded 头。
func Resolve(r *http.Request, trusted []netip.Prefix) RequestInfo {
	info := RequestInfo{Scheme: "http", Host: r.Host}
	if r.TLS != nil {
		info.Scheme = "https"
	}
	peer := parseAddr(r.RemoteAddr)
	info.ClientIP = peer
	if !inNets(peer, trusted) {
		return info
	}

	// 从右往左跳过受信任地址，第一个不受信任的即客户端；遇到非法地址就停。
	entries := splitList(strings.Join(r.Header.Values("X-Forwarded-For"), ","))
	for i := len(entries) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(entries[i])
		if err != nil {
			break
		}
		addr = addr.Unmap()
		info.ClientIP = addr
		if !inNets(addr, trusted) {
			break
		}
	}

	if v := firstValue(r.Header.Get("X-Forwarded-Proto")); strings.EqualFold(v, "http") || strings.EqualFold(v, "https") {
		info.Scheme = strings.ToLower(v)
	}
	if v := firstValue(r.Header.Get("X-Forwarded-Host")); v != "" {
		info.Host = v
	}
	return info
}

// WithRequestInfo 返回把 RequestInfo 放进 context 的中间件。
// trusted 每次请求调用一次，以便设置在运行时变更后立即生效。
func WithRequestInfo(trusted func() []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), infoKey{}, Resolve(r, trusted()))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Info 取出中间件放入的 RequestInfo；未经中间件时返回零值。
func Info(r *http.Request) RequestInfo {
	info, _ := r.Context().Value(infoKey{}).(RequestInfo)
	return info
}

// CheckOrigin 要求 Origin 头严格等于「协议://Host」（忽略大小写）；缺少 Origin 视为不通过。
func CheckOrigin(r *http.Request, info RequestInfo) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	return strings.EqualFold(origin, info.Scheme+"://"+info.Host)
}

// RequireSameOrigin 对 GET、HEAD、OPTIONS 之外的方法校验 Origin，失败回 403 origin.mismatch。
// 依赖前置的 WithRequestInfo。
func RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !CheckOrigin(r, Info(r)) {
				WriteError(w, http.StatusForbidden, CodeOriginMismatch, nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func parseAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func inNets(addr netip.Addr, nets []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range nets {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstValue(s string) string {
	if list := splitList(s); len(list) > 0 {
		return list[0]
	}
	return ""
}
