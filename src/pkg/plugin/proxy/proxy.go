// Package proxy 把代理 URL 转成内置插件用的 http.Transport / 拨号器，以及 exec 插件用的环境变量。
//
// 支持 scheme：http、https（走 Transport.Proxy）、socks5（本地解析域名后把 IP 交给代理）、
// socks5h（把域名交给代理端解析）。"直连"是特殊值：空串或 "direct"，对应的 *Proxy 为 nil
// 或 Direct()，所有方法对 nil 安全。本包不依赖 hub 内部包，agent 也可复用。
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// DirectValue 是表示直连的特殊取值（空串同样表示直连）。
const DirectValue = "direct"

// ErrRawTCPUnsupported 表示所选代理不支持原始 TCP 拨号。
// HTTP/HTTPS 代理只能转发 HTTP 请求（本项目不实现 CONNECT），
// 原始 TCP 探测必须选 socks 代理，否则会绕过代理直连、暴露出口 IP。
var ErrRawTCPUnsupported = errors.New("http 代理不支持原始 TCP，请选 socks 代理")

const dialTimeout = 10 * time.Second

// Proxy 是解析后的代理；nil 表示直连。
type Proxy struct {
	u *url.URL
}

// Direct 返回直连（nil）。
func Direct() *Proxy { return nil }

// Parse 解析代理 URL，形如 scheme://[user:pass@]host:port；空串或 "direct" 为直连。
// 用户名与密码按 URL 规则做百分号编码。不允许带路径、查询或片段。
func Parse(raw string) (*Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, DirectValue) {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("代理地址格式不正确")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("不支持的代理协议 %q", u.Scheme)
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("代理地址不能带路径、查询或片段")
	}
	if err := ValidateHostPort(u.Host); err != nil {
		return nil, err
	}
	u.Path = ""
	return &Proxy{u: u}, nil
}

// ValidateHostPort 校验 host:port（端口 1–65535，主机非空）。
func ValidateHostPort(hostport string) error {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || host == "" {
		return errors.New("代理地址必须是 主机:端口")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("代理端口必须在 1–65535 之间")
	}
	return nil
}

// IsDirect 报告是否直连。
func (p *Proxy) IsDirect() bool { return p == nil || p.u == nil }

// URL 返回含认证的完整 URL，直连为空串。含密码，不得写日志。
func (p *Proxy) URL() string {
	if p.IsDirect() {
		return ""
	}
	return p.u.String()
}

// Redacted 返回密码被隐藏的 URL，可用于日志。
func (p *Proxy) Redacted() string {
	if p.IsDirect() {
		return ""
	}
	return p.u.Redacted()
}

// Transport 返回一个新的 http.Transport：直连时不设代理；
// http/https 代理用 Transport.Proxy；socks5/socks5h 用 DialContext。
func (p *Proxy) Transport() *http.Transport {
	tr := &http.Transport{
		// 显式置空，避免被进程环境里的 HTTP_PROXY 悄悄带偏。
		Proxy:                 nil,
		DialContext:           p.DialContext(),
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		DisableKeepAlives:     true,
	}
	if !p.IsDirect() && (p.u.Scheme == "http" || p.u.Scheme == "https") {
		tr.Proxy = http.ProxyURL(p.u)
		// 连接代理本身用普通拨号。
		tr.DialContext = (&net.Dialer{Timeout: dialTimeout}).DialContext
	}
	return tr
}

// DialContext 返回原始 TCP 拨号函数。直连为普通拨号；socks5 先本地解析域名再拨 IP，
// socks5h 把域名交给代理；http/https 代理不支持原始 TCP，拨号直接返回
// ErrRawTCPUnsupported 且不发起任何连接。HTTP 请求请用 Transport()。
func (p *Proxy) DialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	base := &net.Dialer{Timeout: dialTimeout}
	if p.IsDirect() {
		return base.DialContext
	}
	if p.u.Scheme == "http" || p.u.Scheme == "https" {
		return func(context.Context, string, string) (net.Conn, error) {
			return nil, ErrRawTCPUnsupported
		}
	}
	var auth *xproxy.Auth
	if p.u.User != nil {
		pw, _ := p.u.User.Password()
		auth = &xproxy.Auth{User: p.u.User.Username(), Password: pw}
	}
	localResolve := p.u.Scheme == "socks5"
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d, err := xproxy.SOCKS5("tcp", p.u.Host, auth, base)
		if err != nil {
			return nil, err
		}
		if localResolve {
			addr, err = resolveLocally(ctx, addr)
			if err != nil {
				return nil, err
			}
		}
		if cd, ok := d.(xproxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)
	}
}

// resolveLocally 把 host:port 里的域名解析为 IP:port；已是 IP 则原样返回。
func resolveLocally(ctx context.Context, addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if net.ParseIP(host) != nil {
		return addr, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("无法解析 %s", host)
	}
	return net.JoinHostPort(ips[0].IP.String(), port), nil
}

// Env 返回 exec 插件用的环境变量（KEY=VALUE，大小写两套）。
// 走代理时给出 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY；直连时置 NO_PROXY=*，
// 防止继承自 hub 进程环境的代理变量生效。含密码，不得写日志。
func (p *Proxy) Env() []string {
	if p.IsDirect() {
		return []string{"NO_PROXY=*", "no_proxy=*"}
	}
	v := p.u.String()
	var env []string
	for _, k := range []string{"ALL_PROXY", "HTTPS_PROXY", "HTTP_PROXY"} {
		env = append(env, k+"="+v, strings.ToLower(k)+"="+v)
	}
	return env
}
