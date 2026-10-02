package api

import (
	"net"
	"net/http"

	"github.com/LanceLRQ/PiMon/src/internal/hub/netaddr"
)

// setupURLs 给屏幕端设置码页提供手机访问地址：设置里的对外访问地址优先，
// 其后是本机局域网 IPv4 地址加实际监听端口。屏幕（kiosk）总是走回环地址，页面自己看不到局域网地址，只能由 hub 告诉它。
func (s *server) setupURLs(r *http.Request) []string {
	cur := s.Settings.Get()
	scheme := "http"
	if cur.HTTPSEnabled {
		scheme = "https"
	}
	return netaddr.CandidateURLs(cur.AccessURL, scheme, s.listenPort(r), s.localIPs())
}

// listenPort 取实际监听端口；拿不到时退回请求 Host 里的端口（反代之后可能是反代的端口，尽力而为）。
func (s *server) listenPort(r *http.Request) string {
	if s.ListenAddr != nil {
		if _, port, err := net.SplitHostPort(s.ListenAddr()); err == nil && port != "" {
			return port
		}
	}
	if _, port, err := net.SplitHostPort(r.Host); err == nil {
		return port
	}
	return ""
}

func (s *server) localIPs() []net.IP {
	if s.LocalIPs != nil {
		return s.LocalIPs()
	}
	return netaddr.LANIPv4()
}
