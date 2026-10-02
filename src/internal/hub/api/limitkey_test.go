package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

func limitKeyFor(t *testing.T, remote string) string {
	t.Helper()
	var got string
	h := httpx.WithRequestInfo(func() []netip.Prefix { return nil })(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = limitKey("login:", r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestLimitKey_IPv4保持原地址(t *testing.T) {
	if got := limitKeyFor(t, "192.168.1.7:5000"); got != "login:192.168.1.7" {
		t.Fatalf("key = %q", got)
	}
}

func TestLimitKey_IPv6去zone并取64位前缀(t *testing.T) {
	a := limitKeyFor(t, "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:5000")
	b := limitKeyFor(t, "[2001:db8:1:2::1]:6000")
	c := limitKeyFor(t, "[2001:db8:1:3::1]:6000")
	if a != "login:2001:db8:1:2::/64" || a != b {
		t.Fatalf("同一 /64 应同键: a=%q b=%q", a, b)
	}
	if a == c {
		t.Fatal("不同 /64 不应同键")
	}
}

func TestLimitKey_IPv6链路本地zone被去除(t *testing.T) {
	a := limitKeyFor(t, "[fe80::1%eth0]:5000")
	b := limitKeyFor(t, "[fe80::2%wlan0]:5000")
	if a != b || a != "login:fe80::/64" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestLimitKey_IPv4映射地址按IPv4(t *testing.T) {
	if got := limitKeyFor(t, "[::ffff:10.0.0.5]:5000"); got != "login:10.0.0.5" {
		t.Fatalf("key = %q", got)
	}
}
