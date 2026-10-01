package model

import "time"

// 代理可用位置取值。本期只用于数据与表单过滤，按位置真正限制在 M2（agent）才有意义。
const (
	// ProxyLocationHub 表示仅中枢所在网络可用。
	ProxyLocationHub = "hub"
	// ProxyLocationLAN 表示仅局域网内的 agent 可用。
	ProxyLocationLAN = "lan"
	// ProxyLocationAny 表示任意位置可用。
	ProxyLocationAny = "any"
)

// 代理协议取值。
const (
	ProxySchemeHTTP    = "http"
	ProxySchemeHTTPS   = "https"
	ProxySchemeSOCKS5  = "socks5"
	ProxySchemeSOCKS5H = "socks5h"
)

// Proxy 是全局代理列表中的一项（GET 响应）。认证永不回显，只给出是否已设置。
type Proxy struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Scheme 是 http | https | socks5 | socks5h。
	Scheme string `json:"scheme"`
	// Address 是 host:port。
	Address string `json:"address"`
	// RemoteDNS 表示域名由代理端解析：socks5h 恒为 true，http/https 代理天然如此也为 true，
	// 仅 socks5（本地解析）为 false。
	RemoteDNS bool `json:"remote_dns"`
	// Location 是可用位置：hub | lan | any。
	Location string `json:"location"`
	// Auth 只表示是否设置了认证。
	Auth ProxyAuthState `json:"auth"`
	// Referrers 是引用该代理的监控实例；没有引用时为空数组。
	Referrers []ProxyReferrer `json:"referrers"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ProxyAuthState 是认证的回显形式：{"set": true}。
type ProxyAuthState struct {
	Set bool `json:"set"`
}

// ProxyAuthInput 是提交的代理认证。
type ProxyAuthInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ProxyInput 是创建与更新代理的请求体。
type ProxyInput struct {
	Name    string `json:"name"`
	Scheme  string `json:"scheme"`
	Address string `json:"address"`
	// RemoteDNS 对 socks5 有意义：为 true 时等同于 socks5h。其余协议忽略。
	RemoteDNS bool `json:"remote_dns"`
	// Location 缺省为 any。
	Location string `json:"location"`
	// Auth 缺省（或用户名密码均为空）表示保留原值；新建时表示无认证。
	Auth *ProxyAuthInput `json:"auth,omitempty"`
	// ClearAuth 为 true 时清除已保存的认证（不得与非空 Auth 同时提交）。
	ClearAuth bool `json:"clear_auth,omitempty"`
}

// ProxyReferrer 是引用某个代理的监控实例。
type ProxyReferrer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProxyTestRequest 是代理测试请求体；URL 缺省为 https://www.google.com/generate_204。
type ProxyTestRequest struct {
	URL string `json:"url,omitempty"`
}

// ProxyTestResult 是代理测试结果。请求失败不是 HTTP 错误，而是 OK=false 并给出 Error。
type ProxyTestResult struct {
	OK bool `json:"ok"`
	// URL 是实际请求的目标地址。
	URL string `json:"url"`
	// LatencyMS 是从发起到收到响应头的耗时，毫秒；失败时为 0。
	LatencyMS int64 `json:"latency_ms"`
	// Status 是目标返回的 HTTP 状态码；失败时为 0。
	Status int `json:"status"`
	// Error 是失败原因，成功时为空。
	Error string `json:"error,omitempty"`
}
