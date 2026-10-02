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
	// TypeScreenControl 是一次性屏幕指令（refresh、switch），只发给屏幕会话，立即入队。
	TypeScreenControl ServerMessageType = "screen_control"
)

// 客户端 → 服务端的消息类型。
const (
	TypeSubscribe ClientMessageType = "subscribe"
	TypePing      ClientMessageType = "ping"
	// TypeViewportReport 是屏幕端的状态上报（viewport、触摸能力、当前 screen），仅屏幕会话有效。
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
	// TopicLayout 屏幕布局：管理员得到原始布局与版本，屏幕会话得到解析后的布局。
	TopicLayout = "layout"
	// TopicScreenState 屏幕当前状态（亮灭、主题、下一次变化）：两种角色同形。
	TopicScreenState = "screen_state"
	// TopicScreenData 当前布局引用的实例的展示状态与最新数据项：屏幕会话默认订阅，管理员可订阅做预览。
	TopicScreenData = "screen_data"
)

// patch 的实体。
const (
	// EntityInstanceState 载荷为该实例的完整 Instance（与 GET /api/instances 列表项同形），前端按 id 覆盖。
	EntityInstanceState = "instance_state"
	// EntityInstanceRemoved 只带 id。
	EntityInstanceRemoved = "instance_removed"
	// EntitySettings 管理员会话的载荷为完整设置，屏幕会话的载荷为 ScreenSettings。
	EntitySettings = "settings"
	// EntityLayout 管理员会话的载荷为 Layout（原始布局与版本），屏幕会话的载荷为 ResolvedLayout。
	EntityLayout = "layout"
	// EntityScreenState 载荷为 ScreenState。
	EntityScreenState = "screen_state"
	// EntityScreenData 载荷为发生变化的实例的 ScreenData，客户端按 instance_id 合并覆盖。
	EntityScreenData = "screen_data"
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
	Language      string                      `json:"language"`
	Timezone      string                      `json:"timezone"`
	ReduceEffects bool                        `json:"reduce_effects"`
	Screen        model.ScreenDisplaySettings `json:"screen"`
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
	// Layout 是原始布局与版本，仅管理员会话且订阅了 layout 时有值。
	Layout *model.LayoutState `json:"layout,omitempty"`
	// ResolvedLayout 是解析后的布局，仅屏幕会话且订阅了 layout 时有值。
	ResolvedLayout *model.ResolvedLayout `json:"resolved_layout,omitempty"`
	// ScreenState 是屏幕当前状态，订阅了 screen_state 时有值。
	ScreenState *model.ScreenState `json:"screen_state,omitempty"`
	// ScreenData 是当前布局引用的实例数据，订阅了 screen_data 时有值（没有引用时为空数组）。
	ScreenData *[]model.ScreenInstanceData `json:"screen_data,omitempty" tstype:"ScreenInstanceData[]"`
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
	// Layout 仅 layout 实体、管理员会话有值；ResolvedLayout 仅 layout 实体、屏幕会话有值。
	Layout         *model.LayoutState    `json:"layout,omitempty"`
	ResolvedLayout *model.ResolvedLayout `json:"resolved_layout,omitempty"`
	// ScreenState 仅 screen_state 实体有值。
	ScreenState *model.ScreenState `json:"screen_state,omitempty"`
	// ScreenData 仅 screen_data 实体有值：本次发生变化的实例，客户端按 instance_id 覆盖。
	ScreenData []model.ScreenInstanceData `json:"screen_data,omitempty"`
}

// ScreenControl 是发给屏幕会话的一次性指令：refresh 让屏幕重新加载，switch 让屏幕切到 screen_id。
type ScreenControl struct {
	Type       ServerMessageType `json:"type"`
	ServerTime time.Time         `json:"server_time"`
	Action     string            `json:"action"`
	// ScreenID 仅 switch 有值。
	ScreenID string `json:"screen_id,omitempty"`
	// OpID 是对应的操作记录 id。
	OpID int64 `json:"op_id"`
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
	// 以下三项用于 viewport_report，仅屏幕会话有效（管理员会话发来的被忽略）：
	// Viewport 是当前视口（CSS 像素），CoarsePointer 是是否有触摸（粗指针）能力，CurrentScreen 是正在显示的 screen id。
	// 三者都可缺省，缺省的不更新。
	Viewport      *model.Viewport `json:"viewport,omitempty"`
	CoarsePointer *bool           `json:"coarse_pointer,omitempty"`
	CurrentScreen string          `json:"current_screen,omitempty"`
}
