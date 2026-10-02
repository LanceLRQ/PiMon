package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxSetupURLs 是设置码响应里候选访问地址的上限。
const maxSetupURLs = 3

// setupURLs 给屏幕端设置码页提供手机访问地址：设置里的对外访问地址优先，
// 其后是本机局域网 IPv4 地址加实际监听端口。屏幕（kiosk）总是走回环地址，页面自己看不到局域网地址，只能由 hub 告诉它。
func (s *server) setupURLs(r *http.Request) []string {
	cur := s.Settings.Get()
	scheme := "http"
	if cur.HTTPSEnabled {
		scheme = "https"
	}
	return candidateURLs(cur.AccessURL, scheme, s.listenPort(r), s.localIPs())
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
	return lanIPv4()
}

// candidateURLs 是纯函数：accessURL 非空时排第一；ips 里只取非回环、非链路本地的 IPv4，去重，总数不超过上限。
func candidateURLs(accessURL, scheme, port string, ips []net.IP) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		if u == "" || seen[u] || len(out) >= maxSetupURLs {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	if a := strings.TrimRight(strings.TrimSpace(accessURL), "/"); a != "" {
		add(a)
	}
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
			continue
		}
		u := url.URL{Scheme: scheme, Host: v4.String()}
		if port != "" {
			u.Host = net.JoinHostPort(v4.String(), port)
		}
		add(u.String())
	}
	return out
}

// lanIPv4 枚举处于启用状态的网卡上的 IPv4 地址。
func lanIPv4() []net.IP {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, n.IP)
			}
		}
	}
	return out
}
