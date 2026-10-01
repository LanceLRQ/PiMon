package ui

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// ServerMessageType 与 ClientMessageType 的声明在 msgtype.go（tygo 无法把它们生成为字面量联合，
// 因此该文件被排除，TS 联合类型在此直接给出，取值须与下面两组常量保持一致）。
//
//tygo:emit
var _ = `/** ServerMessageType 是服务端 → 客户端消息的 type 取值。 */
export type ServerMessageType = "snapshot" | "patch" | "pong" | "error" | "screen_control";
/** ClientMessageType 是客户端 → 服务端消息的 type 取值。 */
export type ClientMessageType = "subscribe" | "ping" | "viewport_report";`

// 服务端 → 客户端的消息类型。
const (
	TypeSnapshot ServerMessageType = "snapshot"
	TypePatch    ServerMessageType = "patch"
	TypePong     ServerMessageType = "pong"
	TypeError    ServerMessageType = "error"
	// TypeScreenControl 由 M1d 的屏幕控制使用，本期服务端不发送。
	TypeScreenControl ServerMessageType = "screen_control"
)

// 客户端 → 服务端的消息类型。
const (
	TypeSubscribe ClientMessageType = "subscribe"
	TypePing      ClientMessageType = "ping"
	// TypeViewportReport 由 M1d 的屏幕端使用，本期服务端收到后只视为活动、不处理。
	TypeViewportReport ClientMessageType = "viewport_report"
)

// 会话角色。
const (
	RoleAdmin  = "admin"
	RoleScreen = "screen"
)

// 订阅主题。
const (
	// TopicInstances 全部实例状态（仅管理员会话可订阅）。
	TopicInstances = "instances"
	// TopicSettings 全局设置：管理员得到完整设置，屏幕会话只得到屏幕需要的部分。
	TopicSettings = "settings"
)

// patch 的实体。
const (
	// EntityInstanceState 载荷为该实例的完整 Instance（与 GET /api/instances 列表项同形），前端按 id 覆盖。
	EntityInstanceState = "instance_state"
	// EntityInstanceRemoved 只带 id。
	EntityInstanceRemoved = "instance_removed"
	// EntitySettings 管理员会话的载荷为完整设置，屏幕会话的载荷为 ScreenSettings。
	EntitySettings = "settings"
)

// 协议级错误码（Error 消息的 code；连接不会因此断开）。
const (
	// ErrSubscribeDenied 当前会话无权订阅所请求的主题，details.topics 列出被拒的主题。
	ErrSubscribeDenied = "ws.subscribe_denied"
	// ErrBadMessage 消息不是合法 JSON，或 type 未知。
	ErrBadMessage = "ws.bad_message"
)

// ScreenSettings 是屏幕会话能看到的设置子集。
type ScreenSettings struct {
	Language      string `json:"language"`
	Timezone      string `json:"timezone"`
	ReduceEffects bool   `json:"reduce_effects"`
}

// Snapshot 是连接建立后（以及每次 subscribe 之后）下发的全量状态。
type Snapshot struct {
	Type ServerMessageType `json:"type"`
	// Build 是中枢的构建版本，与 index.html 注入的 pimon-build 同源；前端据此发现中枢升级。
	Build string `json:"build"`
	// ServerTime 是服务端当前时间，前端据此校正时钟偏差。
	ServerTime time.Time `json:"server_time"`
	// Role 是当前会话角色：admin 或 screen。
	Role string `json:"role"`
	// Topics 是当前生效的订阅主题。
	Topics []string `json:"topics"`
	// Settings 仅管理员会话有值。
	Settings *model.Settings `json:"settings,omitempty"`
	// ScreenSettings 仅屏幕会话有值。
	ScreenSettings *ScreenSettings `json:"screen_settings,omitempty"`
	// Instances 是订阅范围内的全部实例；未订阅 instances 时为空数组。
	Instances []model.Instance `json:"instances"`
}

// Patch 是按实体的增量更新。Entity 决定哪些字段有值。
type Patch struct {
	Type       ServerMessageType `json:"type"`
	ServerTime time.Time         `json:"server_time"`
	Entity     string            `json:"entity"`
	// ID 是被删除的实例 id（instance_removed）。
	ID             string          `json:"id,omitempty"`
	Instance       *model.Instance `json:"instance,omitempty"`
	Settings       *model.Settings `json:"settings,omitempty"`
	ScreenSettings *ScreenSettings `json:"screen_settings,omitempty"`
}

// Pong 是对 ping 的应答，带服务端时间供前端校正时钟。
type Pong struct {
	Type       ServerMessageType `json:"type"`
	ServerTime time.Time         `json:"server_time"`
}

// ErrorBody 是协议级错误的内容，形状与 REST 错误体一致。
type ErrorBody struct {
	Code    string         `json:"code"`
	Details map[string]any `json:"details"`
}

// ErrorMessage 是协议级错误消息；收到它后连接仍然可用。
type ErrorMessage struct {
	Type  ServerMessageType `json:"type"`
	Error ErrorBody         `json:"error"`
}

// ClientMessage 是客户端发来的消息，按 Type 取用对应字段。
type ClientMessage struct {
	Type ClientMessageType `json:"type"`
	// Topics 用于 subscribe：声明完整的订阅主题集合，服务端随后重发 snapshot。
	Topics []string `json:"topics,omitempty"`
}
