package model

import "time"

// 设备角色取值。
const (
	DeviceRoleServer = "server"
	DeviceRoleNAS    = "nas"
	DeviceRoleDev    = "dev"
	DeviceRoleVPS    = "vps"
)

// 设备连接方向取值。
const (
	// DeviceDialIn 表示 agent 主动连中枢。
	DeviceDialIn = "dial_in"
	// DeviceDialOut 表示中枢主动连 agent（公网 VPS）。
	DeviceDialOut = "dial_out"
)

// 设备在线状态取值。
const (
	PresenceOnline       = "online"
	PresenceDisconnected = "disconnected"
	PresenceOffline      = "offline"
	PresencePending      = "pending"
	// PresenceDormant 是允许离线的设备离线时对外显示的状态。
	PresenceDormant = "dormant"
)

// DeviceIncompatible 描述设备 agent 与中枢协议不兼容的原因。
type DeviceIncompatible struct {
	Reason string `json:"reason"`
	// RequiredMin 是要求对端至少具备的协议版本。
	RequiredMin int `json:"required_min"`
}

// Device 是设备列表项与详情。凭据、凭据哈希与密文都不在此结构中。
// Presence、ConnError、ClockSkewMs 是运行时值，由中枢在内存中填写，不入库。
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Direction string `json:"direction"`
	// Address 拨出设备为 wss://host:port；拨入设备为最近一次连接的来源 IP（只读）。
	Address      string `json:"address"`
	Hostname     string `json:"hostname"`
	ProxyID      string `json:"proxy_id"`
	AllowOffline bool   `json:"allow_offline"`
	Maintenance  bool   `json:"maintenance"`
	AgentVersion string `json:"agent_version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	// ProtocolVersion 为 0 表示从未握手。
	ProtocolVersion int                 `json:"protocol_version"`
	Incompatible    *DeviceIncompatible `json:"incompatible" tstype:"DeviceIncompatible | null,required"`
	Presence        string              `json:"presence"`
	ConnError       string              `json:"conn_error,omitempty"`
	ClockSkewMs     *int64              `json:"clock_skew_ms" tstype:"number | null,required"`
	LastSeenAt      *time.Time          `json:"last_seen_at" tstype:"string | null,required"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// DeviceInput 是更新设备的请求体。Address 与 ProxyID 只对拨出设备生效，缺省表示不改。
type DeviceInput struct {
	Name         string  `json:"name"`
	Role         string  `json:"role"`
	AllowOffline bool    `json:"allow_offline"`
	Maintenance  bool    `json:"maintenance"`
	Address      *string `json:"address,omitempty"`
	ProxyID      *string `json:"proxy_id,omitempty"`
}

// PairingRequest 是生成拨入配对码的请求体。AllowOffline 缺省时按角色决定（dev 默认开）。
type PairingRequest struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	AllowOffline    *bool  `json:"allow_offline,omitempty"`
	AutoHostMetrics *bool  `json:"auto_host_metrics,omitempty"`
}

// PairingResult 是拨入配对码的响应。
type PairingResult struct {
	Code        string    `json:"code"`
	ExpiresAt   time.Time `json:"expires_at"`
	URLs        []string  `json:"urls"`
	Fingerprint string    `json:"fingerprint"`
	// Command 是一行安装命令，Steps 是分步命令。
	Command string   `json:"command"`
	Steps   []string `json:"steps"`
}

// DialOutRequest 是拨出配对（中枢主动连 agent）的请求体。
type DialOutRequest struct {
	Address         string `json:"address"`
	Fingerprint     string `json:"fingerprint"`
	Code            string `json:"code"`
	ProxyID         string `json:"proxy_id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	AllowOffline    *bool  `json:"allow_offline,omitempty"`
	AutoHostMetrics *bool  `json:"auto_host_metrics,omitempty"`
}

// InstanceRef 是设备影响分析中引用的实例。
type InstanceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RuleRef 是设备影响分析中引用的告警规则（M4 前恒为空）。
type RuleRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DeviceImpact 是吊销或重新配对某设备前展示的影响范围。
type DeviceImpact struct {
	Instances []InstanceRef `json:"instances"`
	Screens   []ScreenRef   `json:"screens"`
	Rules     []RuleRef     `json:"rules"`
}

// AgentInfo 是 GET /api/agent/info 的响应：agent 通道的接入信息。
type AgentInfo struct {
	// AgentPort 是通道实际监听的端口；通道未启用时为 0。
	AgentPort int `json:"agent_port"`
	// Fingerprint 是中枢证书公钥的 SPKI 指纹（SHA256:...）。
	Fingerprint          string `json:"fingerprint"`
	HubVersion           string `json:"hub_version"`
	ProtocolVersion      int    `json:"protocol_version"`
	MinCompatibleVersion int    `json:"min_compatible_version"`
	// Platforms 是中枢内嵌的 agent 平台（如 linux-amd64），没有内嵌时为空数组。
	Platforms []string `json:"platforms"`
	// ReleaseURLTemplate 是 GitHub Releases 的下载地址模板（<os>、<arch> 为占位）；开发构建为空串。
	ReleaseURLTemplate string `json:"release_url_template"`
	// ReleaseChecksums 是 Release 产物的 SHA-256（键 os-arch）；开发构建或没有内嵌时为空对象。
	ReleaseChecksums map[string]string `json:"release_checksums"`
}
