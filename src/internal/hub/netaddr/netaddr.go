// Package netaddr 枚举本机可对外访问的地址，供设置码页与 install 打印访问地址共用。
package netaddr

import (
	"net"
	"net/url"
	"strings"
)

// MaxURLs 是候选访问地址的上限。
const MaxURLs = 3

// CandidateURLs 是纯函数：accessURL 非空时排第一；ips 里只取非回环、非链路本地的 IPv4，去重，总数不超过上限。
func CandidateURLs(accessURL, scheme, port string, ips []net.IP) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		if u == "" || seen[u] || len(out) >= MaxURLs {
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

// LANIPv4 枚举处于启用状态的网卡上的 IPv4 地址。
func LANIPv4() []net.IP {
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
