package schema

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// CheckURL 按 url 字段的规则检查地址：仅 http/https、必须有主机、禁止内嵌凭据；
// 查询参数需 allowQuery，公网主机使用 http 需 allowPublicHTTP。
func CheckURL(raw string, allowQuery, allowPublicHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("只支持 http 与 https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("缺少主机")
	}
	if u.User != nil {
		return errors.New("地址不能内嵌凭据")
	}
	if !allowQuery && (u.RawQuery != "" || u.ForceQuery) {
		return errors.New("地址不能带查询参数")
	}
	if u.Scheme == "http" && !allowPublicHTTP && isPublicHost(host) {
		return errors.New("公网地址必须使用 https")
	}
	return nil
}

// isPublicHost 判断主机是否属于公网：内网/回环/链路本地/CGNAT 地址、
// localhost、单标签主机名与常见内网后缀视为非公网。
func isPublicHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
			!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !cgnat.Contains(ip)
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || !strings.Contains(h, ".") {
		return false
	}
	for _, suf := range []string{".localhost", ".local", ".lan", ".internal", ".home.arpa", ".localdomain"} {
		if strings.HasSuffix(h, suf) {
			return false
		}
	}
	return true
}
