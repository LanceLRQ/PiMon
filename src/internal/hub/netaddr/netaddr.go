// Package netaddr 枚举本机可对外访问的地址，供设置码页与 install 打印访问地址共用。
package netaddr

import (
	"net"
	"net/url"
	"strings"
)

// MaxURLs 是候选访问地址的上限。
const MaxURLs = 3

// urlSet 按插入顺序收集不重复的地址，总数不超过 MaxURLs。
type urlSet struct {
	out  []string
	seen map[string]bool
}

func (u *urlSet) add(v string) {
	if v == "" || u.seen[v] || len(u.out) >= MaxURLs {
		return
	}
	if u.seen == nil {
		u.seen = map[string]bool{}
	}
	u.seen[v] = true
	u.out = append(u.out, v)
}

// lanIPv4s 只留下非回环、非链路本地、非未指定的 IPv4。
func lanIPv4s(ips []net.IP) []net.IP {
	var out []net.IP
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
			continue
		}
		out = append(out, v4)
	}
	return out
}

// CandidateURLs 是纯函数：accessURL 非空时排第一；ips 里只取非回环、非链路本地的 IPv4，去重，总数不超过上限。
func CandidateURLs(accessURL, scheme, port string, ips []net.IP) []string {
	var set urlSet
	if a := strings.TrimRight(strings.TrimSpace(accessURL), "/"); a != "" {
		set.add(a)
	}
	for _, ip := range lanIPv4s(ips) {
		u := url.URL{Scheme: scheme, Host: ip.String()}
		if port != "" {
			u.Host = net.JoinHostPort(ip.String(), port)
		}
		set.add(u.String())
	}
	return set.out
}

// AgentURLs 是纯函数，返回 agent 通道的候选地址（wss://主机:port）：accessURL 非空且可解析时，
// 取它的主机名排第一（网页端口换成 agent 通道端口）；其后是 ips 里的局域网 IPv4（过滤规则同 CandidateURLs）。
// 去重，总数不超过 MaxURLs。
func AgentURLs(accessURL, port string, ips []net.IP) []string {
	var set urlSet
	if a := strings.TrimSpace(accessURL); a != "" {
		if u, err := url.Parse(a); err == nil && u.Hostname() != "" {
			set.add((&url.URL{Scheme: "wss", Host: net.JoinHostPort(u.Hostname(), port)}).String())
		}
	}
	for _, ip := range lanIPv4s(ips) {
		set.add((&url.URL{Scheme: "wss", Host: net.JoinHostPort(ip.String(), port)}).String())
	}
	return set.out
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
