package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

func validRole(r string) bool { return r == RoleHub || r == RoleAgent }

// ---- 握手 ----

// Auth 是拨号方的认证帧（长期凭据）。
type Auth struct {
	Credential string `json:"credential"`
	Role       string `json:"role"`
}

func (Auth) MsgType() Type { return TypeAuth }
func (m Auth) Validate() error {
	if m.Credential == "" {
		return invalid(TypeAuth, "credential", "不能为空")
	}
	if !validRole(m.Role) {
		return invalid(TypeAuth, "role", "必须是 hub 或 agent")
	}
	return nil
}

// AuthOK 是认证成功的应答。
type AuthOK struct {
	DeviceID string `json:"device_id"`
}

func (AuthOK) MsgType() Type { return TypeAuthOK }
func (m AuthOK) Validate() error {
	if m.DeviceID == "" {
		return invalid(TypeAuthOK, "device_id", "不能为空")
	}
	return nil
}

// AuthFail 是认证失败的应答，发送后监听方关闭连接。
type AuthFail struct {
	Reason string `json:"reason"`
}

func (AuthFail) MsgType() Type { return TypeAuthFail }
func (m AuthFail) Validate() error {
	if m.Reason == "" {
		return invalid(TypeAuthFail, "reason", "不能为空")
	}
	return nil
}

// Pair 是拨号方的配对帧（配对码换凭据）。
type Pair struct {
	Code     string `json:"code"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

func (Pair) MsgType() Type { return TypePair }
func (m Pair) Validate() error {
	if m.Code == "" {
		return invalid(TypePair, "code", "不能为空")
	}
	if !validRole(m.Role) {
		return invalid(TypePair, "role", "必须是 hub 或 agent")
	}
	return nil
}

// ListenerInfo 是配对成功时监听方告知的自身信息。
type ListenerInfo struct {
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

// PairOK 是配对成功的应答，等同 auth_ok，会话随后进入 hello。
type PairOK struct {
	Credential   string       `json:"credential"`
	DeviceID     string       `json:"device_id"`
	ListenerInfo ListenerInfo `json:"listener_info"`
}

func (PairOK) MsgType() Type { return TypePairOK }
func (m PairOK) Validate() error {
	if m.Credential == "" {
		return invalid(TypePairOK, "credential", "不能为空")
	}
	if m.DeviceID == "" {
		return invalid(TypePairOK, "device_id", "不能为空")
	}
	return nil
}

// ---- hello 与版本 ----

// PluginManifest 是 agent 上一个可用插件的 manifest；协议包不解析内层，由 hub 入库前校验。
type PluginManifest struct {
	// Origin 是插件来源，如 builtin、exec。
	Origin   string          `json:"origin"`
	Manifest json.RawMessage `json:"manifest"`
}

func validateManifests(t Type, field string, ms []PluginManifest) error {
	for i, p := range ms {
		if len(p.Manifest) == 0 || string(p.Manifest) == "null" {
			return invalid(t, fmt.Sprintf("%s[%d].manifest", field, i), "不能为空")
		}
	}
	return nil
}

// Hello 是认证后 agent 发给 hub 的第一条消息。
type Hello struct {
	ProtocolVersion      int              `json:"protocol_version"`
	MinCompatibleVersion int              `json:"min_compatible_version"`
	AgentVersion         string           `json:"agent_version"`
	OS                   string           `json:"os"`
	Arch                 string           `json:"arch"`
	Hostname             string           `json:"hostname"`
	Plugins              []PluginManifest `json:"plugins"`
}

func (Hello) MsgType() Type { return TypeHello }
func (m Hello) Validate() error {
	if m.ProtocolVersion < 1 {
		return invalid(TypeHello, "protocol_version", "必须 ≥ 1")
	}
	if m.MinCompatibleVersion < 1 || m.MinCompatibleVersion > m.ProtocolVersion {
		return invalid(TypeHello, "min_compatible_version", "必须在 1 到 protocol_version 之间")
	}
	if m.AgentVersion == "" {
		return invalid(TypeHello, "agent_version", "不能为空")
	}
	return validateManifests(TypeHello, "plugins", m.Plugins)
}

// HelloOK 是 hub 对 hello 的接受应答。
type HelloOK struct {
	HubVersion string    `json:"hub_version"`
	ServerTime time.Time `json:"server_time"`
}

func (HelloOK) MsgType() Type { return TypeHelloOK }
func (m HelloOK) Validate() error {
	if m.HubVersion == "" {
		return invalid(TypeHelloOK, "hub_version", "不能为空")
	}
	if m.ServerTime.IsZero() {
		return invalid(TypeHelloOK, "server_time", "不能为空")
	}
	return nil
}

// Incompatible 是 hub 对 hello 的拒绝，随后断开。reason 为 version_too_old 或 version_too_new。
type Incompatible struct {
	Reason string `json:"reason"`
	// RequiredMin 是需要升级的一方应达到的最低协议版本。
	RequiredMin int    `json:"required_min"`
	HubVersion  string `json:"hub_version"`
}

func (Incompatible) MsgType() Type { return TypeIncompatible }
func (m Incompatible) Validate() error {
	if m.Reason != ReasonVersionTooOld && m.Reason != ReasonVersionTooNew {
		return invalid(TypeIncompatible, "reason", "必须是 version_too_old 或 version_too_new")
	}
	if m.RequiredMin < 1 {
		return invalid(TypeIncompatible, "required_min", "必须 ≥ 1")
	}
	return nil
}

// PluginsChanged 是 agent 的全量可用插件 manifest。
type PluginsChanged struct {
	Plugins []PluginManifest `json:"plugins"`
}

func (PluginsChanged) MsgType() Type { return TypePluginsChanged }
func (m PluginsChanged) Validate() error {
	return validateManifests(TypePluginsChanged, "plugins", m.Plugins)
}

// ---- 实例下发 ----

// AssignedInstance 是 hub 下发给 agent 的一个实例。Secrets 与 ProxyURL 含敏感信息，格式化与日志中脱敏。
type AssignedInstance struct {
	ID       string            `json:"id"`
	PluginID string            `json:"plugin_id"`
	Config   map[string]any    `json:"config,omitempty"`
	Secrets  map[string]string `json:"secrets,omitempty"`
	// ProxyURL 是已解析的代理 URL，agent 用 proxy.Parse 还原；空表示直连。
	ProxyURL   string `json:"proxy_url,omitempty"`
	IntervalMs int64  `json:"interval_ms"`
	TimeoutMs  int64  `json:"timeout_ms"`
	ConfigHash string `json:"config_hash"`
	// ContentHash 是实例配置内容的摘要（hub 实例行的 config_hash），可选：只在它变化时 agent 清空
	// 实例的上次报告与私有 state（与 hub 的 resetState 一致）；间隔、代理、插件版本变化不改变它。
	// 为空表示未知（旧版 hub），agent 不据此清空。
	ContentHash string `json:"content_hash,omitempty"`
}

// AssignedInstances 是一组下发实例；单独记录时 LogValue 逐实例脱敏（JSON 处理器不会展开明文）。
type AssignedInstances []AssignedInstance

// Assign 是 hub 下发的全量实例集合。
type Assign struct {
	Version   int64             `json:"version"`
	Instances AssignedInstances `json:"instances"`
}

func (Assign) MsgType() Type { return TypeAssign }
func (m Assign) Validate() error {
	if m.Version < 1 {
		return invalid(TypeAssign, "version", "必须 ≥ 1")
	}
	seen := make(map[string]struct{}, len(m.Instances))
	for i, in := range m.Instances {
		p := fmt.Sprintf("instances[%d].", i)
		if in.ID == "" {
			return invalid(TypeAssign, p+"id", "不能为空")
		}
		if _, dup := seen[in.ID]; dup {
			return invalid(TypeAssign, p+"id", "重复")
		}
		seen[in.ID] = struct{}{}
		if in.PluginID == "" {
			return invalid(TypeAssign, p+"plugin_id", "不能为空")
		}
		if in.IntervalMs < 0 {
			return invalid(TypeAssign, p+"interval_ms", "不能为负")
		}
		if in.TimeoutMs < 0 {
			return invalid(TypeAssign, p+"timeout_ms", "不能为负")
		}
	}
	return nil
}

// assign_result 的逐实例状态。
const (
	AssignStarted       = "started"
	AssignPluginMissing = "plugin_missing"
	AssignInvalidConfig = "invalid_config"
	AssignIncompatible  = "incompatible"
)

// AssignResultItem 是一个实例的下发结果。
type AssignResultItem struct {
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// AssignResult 是 agent 对某个 assign 版本的逐实例应答。
type AssignResult struct {
	Version int64              `json:"version"`
	Results []AssignResultItem `json:"results"`
}

func (AssignResult) MsgType() Type { return TypeAssignResult }
func (m AssignResult) Validate() error {
	if m.Version < 1 {
		return invalid(TypeAssignResult, "version", "必须 ≥ 1")
	}
	for i, r := range m.Results {
		p := fmt.Sprintf("results[%d].", i)
		if r.InstanceID == "" {
			return invalid(TypeAssignResult, p+"instance_id", "不能为空")
		}
		switch r.Status {
		case AssignStarted, AssignPluginMissing, AssignInvalidConfig, AssignIncompatible:
		default:
			return invalid(TypeAssignResult, p+"status", "取值非法")
		}
	}
	return nil
}

// ---- 采集结果 ----

// RunOutcome.ErrorKind 的取值。
const (
	ErrorKindTimeout     = "timeout"
	ErrorKindFailed      = "failed"
	ErrorKindBusy        = "busy"
	ErrorKindNotAssigned = "not_assigned"
)

// RunOutcome 是一次运行的结果：成功时带 Report，失败时带 Error 与 ErrorKind，二者有且仅有一个。
// 协议不带插件私有 state，发送方须在发出前清空 Report.State。
type RunOutcome struct {
	Report      *report.Report `json:"report,omitempty"`
	Error       string         `json:"error,omitempty"`
	ErrorKind   string         `json:"error_kind,omitempty"`
	DurationMs  int64          `json:"duration_ms"`
	CollectedAt time.Time      `json:"collected_at"`
	// ConfigHash 是产生本结果时实例的下发摘要（AssignedInstance.ConfigHash），可选：
	// hub 与最近一次下发的摘要不一致时丢弃（旧配置的结果）；为空视为当前配置，兼容不填写的 agent。
	ConfigHash string `json:"config_hash,omitempty"`
}

// check 返回出错字段与原因，合法时字段为空。
func (o RunOutcome) check() (field, reason string) {
	hasErr := o.ErrorKind != "" || o.Error != ""
	if o.Report == nil && !hasErr {
		return "report", "成功报告与错误必须有其一"
	}
	if o.Report != nil && hasErr {
		return "report", "不能同时带报告与错误"
	}
	if o.Report != nil && o.Report.State != "" {
		return "report.state", "协议不携带插件私有 state"
	}
	if hasErr {
		if o.Error == "" {
			return "error", "有 error_kind 时不能为空"
		}
		switch o.ErrorKind {
		case ErrorKindTimeout, ErrorKindFailed, ErrorKindBusy, ErrorKindNotAssigned:
		default:
			return "error_kind", "取值非法"
		}
	}
	if o.DurationMs < 0 {
		return "duration_ms", "不能为负"
	}
	return "", ""
}

func (o RunOutcome) validate(t Type) error {
	if f, r := o.check(); f != "" {
		return invalid(t, f, r)
	}
	return nil
}

// Report 是 agent 周期性上报的一次采集结果；Seq 单调递增（从 1 起），用于去重。
type Report struct {
	InstanceID string `json:"instance_id"`
	Seq        int64  `json:"seq"`
	RunOutcome
}

func (Report) MsgType() Type { return TypeReport }
func (m Report) Validate() error {
	if m.InstanceID == "" {
		return invalid(TypeReport, "instance_id", "不能为空")
	}
	if m.Seq < 1 {
		return invalid(TypeReport, "seq", "必须 ≥ 1")
	}
	return m.validate(TypeReport)
}

// Event 是 agent 上报的事件。Seq 是 agent 持久化队列里的单调序号，可选，缺省为 0。
type Event struct {
	ID   string          `json:"id"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data,omitempty"`
	At   time.Time       `json:"at"`
	Seq  int64           `json:"seq,omitempty"`
}

// eventIDMaxBytes 是事件 id 的长度上限。
const eventIDMaxBytes = 128

func (Event) MsgType() Type { return TypeEvent }
func (m Event) Validate() error {
	if m.ID == "" {
		return invalid(TypeEvent, "id", "不能为空")
	}
	if len(m.ID) > eventIDMaxBytes {
		return invalid(TypeEvent, "id", "过长")
	}
	if m.Seq < 0 {
		return invalid(TypeEvent, "seq", "必须 ≥ 0")
	}
	if m.Kind == "" {
		return invalid(TypeEvent, "kind", "不能为空")
	}
	return nil
}

// Ack 是 hub 对事件的确认，本期只定义结构。
type Ack struct {
	IDs []string `json:"ids"`
}

func (Ack) MsgType() Type { return TypeAck }
func (m Ack) Validate() error {
	if len(m.IDs) == 0 {
		return invalid(TypeAck, "ids", "不能为空")
	}
	return nil
}

// ---- 请求/响应 ----

// RunNow 请求 agent 立即运行一个实例。
type RunNow struct {
	RequestID  string `json:"request_id"`
	InstanceID string `json:"instance_id"`
}

func (RunNow) MsgType() Type             { return TypeRunNow }
func (m RunNow) GetRequestID() string    { return m.RequestID }
func (m *RunNow) SetRequestID(id string) { m.RequestID = id }
func (m RunNow) Validate() error {
	if m.RequestID == "" {
		return invalid(TypeRunNow, "request_id", "不能为空")
	}
	if m.InstanceID == "" {
		return invalid(TypeRunNow, "instance_id", "不能为空")
	}
	return nil
}

// RunResult 是 run_now 的结果，结构同 Report 的运行结果。
type RunResult struct {
	RequestID  string `json:"request_id"`
	InstanceID string `json:"instance_id"`
	RunOutcome
}

func (RunResult) MsgType() Type          { return TypeRunResult }
func (m RunResult) GetRequestID() string { return m.RequestID }
func (m RunResult) Validate() error {
	if m.RequestID == "" {
		return invalid(TypeRunResult, "request_id", "不能为空")
	}
	if m.InstanceID == "" {
		return invalid(TypeRunResult, "instance_id", "不能为空")
	}
	return m.validate(TypeRunResult)
}

// LogsGet 请求 agent 最近的日志。Limit 为 0 表示由 agent 取默认条数；MinLevel 为空表示不过滤。
type LogsGet struct {
	RequestID string `json:"request_id"`
	Limit     int    `json:"limit"`
	MinLevel  string `json:"min_level"`
}

func (LogsGet) MsgType() Type             { return TypeLogsGet }
func (m LogsGet) GetRequestID() string    { return m.RequestID }
func (m *LogsGet) SetRequestID(id string) { m.RequestID = id }
func (m LogsGet) Validate() error {
	if m.RequestID == "" {
		return invalid(TypeLogsGet, "request_id", "不能为空")
	}
	if m.Limit < 0 {
		return invalid(TypeLogsGet, "limit", "不能为负")
	}
	switch m.MinLevel {
	case "", "debug", "info", "warn", "error":
	default:
		return invalid(TypeLogsGet, "min_level", "取值非法")
	}
	return nil
}

// Logs 是 logs_get 的应答。
type Logs struct {
	RequestID string           `json:"request_id"`
	Entries   []model.LogEntry `json:"entries"`
}

func (Logs) MsgType() Type          { return TypeLogs }
func (m Logs) GetRequestID() string { return m.RequestID }
func (m Logs) Validate() error {
	if m.RequestID == "" {
		return invalid(TypeLogs, "request_id", "不能为空")
	}
	return nil
}

// UpgradeInfoGet 是 agent 请求升级信息。
type UpgradeInfoGet struct {
	RequestID string `json:"request_id"`
}

func (UpgradeInfoGet) MsgType() Type             { return TypeUpgradeInfoGet }
func (m UpgradeInfoGet) GetRequestID() string    { return m.RequestID }
func (m *UpgradeInfoGet) SetRequestID(id string) { m.RequestID = id }
func (m UpgradeInfoGet) Validate() error {
	if m.RequestID == "" {
		return invalid(TypeUpgradeInfoGet, "request_id", "不能为空")
	}
	return nil
}

// UpgradeInfo 是 hub 的升级信息。Agents 的键为 os-arch，值为 SHA-256 十六进制，无内嵌二进制时为空。
type UpgradeInfo struct {
	RequestID  string            `json:"request_id"`
	HubVersion string            `json:"hub_version"`
	WebPort    int               `json:"web_port"`
	Agents     map[string]string `json:"agents"`
}

func (UpgradeInfo) MsgType() Type          { return TypeUpgradeInfo }
func (m UpgradeInfo) GetRequestID() string { return m.RequestID }
func (m UpgradeInfo) Validate() error {
	if m.RequestID == "" {
		return invalid(TypeUpgradeInfo, "request_id", "不能为空")
	}
	if m.HubVersion == "" {
		return invalid(TypeUpgradeInfo, "hub_version", "不能为空")
	}
	if m.WebPort < 1 || m.WebPort > 65535 {
		return invalid(TypeUpgradeInfo, "web_port", "必须在 1 到 65535 之间")
	}
	return nil
}

// ---- 心跳与断开 ----

// Ping 是心跳，带发送时刻。
type Ping struct {
	SentAt time.Time `json:"sent_at"`
}

func (Ping) MsgType() Type { return TypePing }
func (m Ping) Validate() error {
	if m.SentAt.IsZero() {
		return invalid(TypePing, "sent_at", "不能为空")
	}
	return nil
}

// Pong 是对 ping 的应答：Echo 回显 ping 的 sent_at，SentAt 是 pong 的发送时刻；
// ping 发起方据此算时钟偏移 = pong.sent_at − (echo + 收到时刻)/2。
type Pong struct {
	Echo   time.Time `json:"echo"`
	SentAt time.Time `json:"sent_at"`
}

func (Pong) MsgType() Type { return TypePong }
func (m Pong) Validate() error {
	if m.Echo.IsZero() {
		return invalid(TypePong, "echo", "不能为空")
	}
	if m.SentAt.IsZero() {
		return invalid(TypePong, "sent_at", "不能为空")
	}
	return nil
}

// Bye 是主动断开前的原因（revoked、shutdown、replaced 等）。
type Bye struct {
	Reason string `json:"reason"`
}

func (Bye) MsgType() Type { return TypeBye }
func (m Bye) Validate() error {
	if m.Reason == "" {
		return invalid(TypeBye, "reason", "不能为空")
	}
	return nil
}
