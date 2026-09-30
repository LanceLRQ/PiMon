package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

func newReq(remote string, hdr map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://pi.local/x", nil)
	r.RemoteAddr = remote
	for k, vs := range hdr {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return r
}

func TestResolve(t *testing.T) {
	proxy := prefixes(t, "127.0.0.0/8", "10.0.0.0/8", "::1/128")
	tests := []struct {
		name   string
		remote string
		hdr    map[string][]string
		tls    bool
		ip     string
		scheme string
		host   string
	}{
		{"直连忽略伪造 XFF/Proto/Host", "203.0.113.9:5000", map[string][]string{
			"X-Forwarded-For": {"1.2.3.4"}, "X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"evil.com"}},
			false, "203.0.113.9", "http", "pi.local"},
		{"直连 TLS 记为 https", "203.0.113.9:5000", nil, true, "203.0.113.9", "https", "pi.local"},
		{"受信任反代单跳", "127.0.0.1:5000", map[string][]string{"X-Forwarded-For": {"198.51.100.7"}},
			false, "198.51.100.7", "http", "pi.local"},
		{"受信任反代无 XFF 回落到对端", "127.0.0.1:5000", nil, false, "127.0.0.1", "http", "pi.local"},
		{"多跳链路跳过受信任地址", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-For": {"9.9.9.9, 198.51.100.7", "10.1.2.3"}},
			false, "198.51.100.7", "http", "pi.local"},
		{"客户端伪造的左侧条目不被采信", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-For": {"6.6.6.6, 198.51.100.7"}},
			false, "198.51.100.7", "http", "pi.local"},
		{"链中有非法地址即停", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-For": {"5.5.5.5, garbage, 10.1.2.3"}},
			false, "10.1.2.3", "http", "pi.local"},
		{"最右即非法地址回落到对端", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-For": {"5.5.5.5, garbage"}},
			false, "127.0.0.1", "http", "pi.local"},
		{"全部受信任取最左", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-For": {"10.0.0.5, 10.0.0.6"}},
			false, "10.0.0.5", "http", "pi.local"},
		{"IPv6 对端与客户端", "[::1]:5000", map[string][]string{
			"X-Forwarded-For": {"2001:db8::1"}},
			false, "2001:db8::1", "http", "pi.local"},
		{"IPv4-mapped 对端被还原后判断受信任", "[::ffff:127.0.0.1]:5000", map[string][]string{
			"X-Forwarded-For": {"198.51.100.7"}},
			false, "198.51.100.7", "http", "pi.local"},
		{"Proto 与 Host 覆盖", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-Proto": {"HTTPS"}, "X-Forwarded-Host": {"pimon.example.com"}},
			false, "127.0.0.1", "https", "pimon.example.com"},
		{"非法 Proto 被忽略", "127.0.0.1:5000", map[string][]string{
			"X-Forwarded-Proto": {"javascript"}},
			false, "127.0.0.1", "http", "pi.local"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReq(tc.remote, tc.hdr)
			if tc.tls {
				r = httptest.NewRequest(http.MethodGet, "https://pi.local/x", nil)
				r.RemoteAddr = tc.remote
			}
			got := Resolve(r, proxy)
			if got.ClientIP != netip.MustParseAddr(tc.ip) {
				t.Errorf("ClientIP = %v, want %v", got.ClientIP, tc.ip)
			}
			if got.Scheme != tc.scheme {
				t.Errorf("Scheme = %q, want %q", got.Scheme, tc.scheme)
			}
			if got.Host != tc.host {
				t.Errorf("Host = %q, want %q", got.Host, tc.host)
			}
		})
	}
}

func TestResolveNoTrustedNets(t *testing.T) {
	r := newReq("127.0.0.1:1", map[string][]string{"X-Forwarded-For": {"1.2.3.4"}})
	if got := Resolve(r, nil).ClientIP; got != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("ClientIP = %v", got)
	}
}

func TestMiddlewareAndInfo(t *testing.T) {
	h := WithRequestInfo(func() []netip.Prefix { return prefixes(t, "127.0.0.0/8") })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(Info(r).ClientIP.String()))
		}))
	r := newReq("127.0.0.1:1", map[string][]string{"X-Forwarded-For": {"198.51.100.7"}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Body.String() != "198.51.100.7" {
		t.Errorf("body = %q", w.Body.String())
	}
	if Info(httptest.NewRequest("GET", "/", nil)) != (RequestInfo{}) {
		t.Error("未经中间件时 Info 应为零值")
	}
}

func TestCheckOrigin(t *testing.T) {
	info := RequestInfo{Scheme: "https", Host: "Pimon.Example.com"}
	tests := []struct {
		name   string
		origin string
		set    bool
		want   bool
	}{
		{"匹配", "https://pimon.example.com", true, true},
		{"忽略大小写", "HTTPS://PIMON.example.COM", true, true},
		{"协议不同", "http://pimon.example.com", true, false},
		{"主机不同", "https://evil.com", true, false},
		{"带路径不匹配", "https://pimon.example.com/", true, false},
		{"缺失", "", false, false},
		{"空值", "", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.set {
				r.Header.Set("Origin", tc.origin)
			}
			if got := CheckOrigin(r, info); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRequireSameOrigin(t *testing.T) {
	h := WithRequestInfo(func() []netip.Prefix { return nil })(
		RequireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))
	do := func(method, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://pi.local/x", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if w := do(m, ""); w.Code != http.StatusNoContent {
			t.Errorf("%s 不应校验: %d", m, w.Code)
		}
	}
	if w := do(http.MethodPost, "http://pi.local"); w.Code != http.StatusNoContent {
		t.Errorf("同源 POST 应放行: %d", w.Code)
	}
	w := do(http.MethodPost, "http://evil.com")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"origin.mismatch"`) {
		t.Errorf("跨源 POST: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodDelete, ""); w.Code != http.StatusForbidden {
		t.Errorf("缺失 Origin 的 DELETE 应拒绝: %d", w.Code)
	}
}
