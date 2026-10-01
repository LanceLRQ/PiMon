package model

import "time"

// 屏幕主题取值：时段计划里只能使用内置主题或关屏。
const (
	// ThemeOff 表示该时段关屏。
	ThemeOff = "off"
	// ThemeAmbient 是默认主题。
	ThemeAmbient = "ambient"
	// ThemeMissionControl 是深色高密度主题。
	ThemeMissionControl = "mission-control"
	// ThemeIndustrial 是米白浅色主题。
	ThemeIndustrial = "industrial"
)

// 屏幕状态模式与原因。
const (
	ScreenModeOn  = "on"
	ScreenModeOff = "off"

	ScreenReasonSchedule  = "schedule"
	ScreenReasonRemoteOn  = "remote_on"
	ScreenReasonRemoteOff = "remote_off"
	ScreenReasonWake      = "wake"
)

// 屏幕控制与操作记录的动作。
const (
	ScreenActionRefresh    = "refresh"
	ScreenActionSwitch     = "switch"
	ScreenActionOn         = "on"
	ScreenActionOff        = "off"
	ScreenActionWake       = "wake"
	ScreenActionTokenReset = "token_reset"
)

// 时段计划校验问题的类别。
const (
	ScheduleProblemFormat  = "format"
	ScheduleProblemTheme   = "theme"
	ScheduleProblemEmpty   = "empty"
	ScheduleProblemOverlap = "overlap"
	ScheduleProblemGap     = "gap"
)

// SchedulePeriod 是时段计划中的一个时段，按全局时区的本地时刻计算。
// Start、End 为 HH:MM（00:00–23:59），区间左闭右开；End 不晚于 Start 表示跨日；
// 只有单个时段时 Start 等于 End 表示全天。
type SchedulePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
	// Theme 是内置主题 id 或 "off"（关屏）。
	Theme string `json:"theme"`
}

// Schedule 是屏幕时段计划：时段必须互不重叠并覆盖完整 24 小时。
type Schedule struct {
	Periods []SchedulePeriod `json:"periods"`
}

// ScheduleProblem 是时段计划校验失败的一项；重叠与缺口用 From、To 给出区间（HH:MM）。
type ScheduleProblem struct {
	Kind string `json:"kind"`
	// Period 是出问题的时段下标（仅 format、theme 类问题带）。
	Period *int   `json:"period,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// ScreenState 是屏幕当前状态，随 snapshot 下发，变化时发 screen_state patch。
type ScreenState struct {
	// Mode 为 on 或 off。
	Mode string `json:"mode"`
	// ThemeID 是亮屏时显示的主题；关屏时为唤醒后将使用的主题。
	ThemeID string `json:"theme_id"`
	// Until 是远程临时操作的到期时刻；没有临时操作时缺省。
	Until *time.Time `json:"until,omitempty"`
	// NextChange 是下一次模式或主题变化的时刻；不再变化时缺省。
	NextChange *time.Time `json:"next_change,omitempty"`
	// Reason 为 schedule、remote_on、remote_off 或 wake。
	Reason string `json:"reason"`
}

// Viewport 是屏幕上报并被采信的视口。
type Viewport struct {
	W   int     `json:"w"`
	H   int     `json:"h"`
	DPR float64 `json:"dpr"`
}

// ScreenStatus 是 GET /api/screen/status 的响应。
type ScreenStatus struct {
	State ScreenState `json:"state"`
	// Viewport 为首次被采信之前缺省。
	Viewport *Viewport `json:"viewport,omitempty"`
	// CoarsePointer 是屏幕上报的触摸（粗指针）能力；未上报时缺省。
	CoarsePointer *bool `json:"coarse_pointer,omitempty"`
}

// ScreenControlRequest 是 POST /api/screen/control 的请求体。
type ScreenControlRequest struct {
	// Action 为 refresh、switch、on、off、wake。
	Action string `json:"action"`
	// ScreenID 仅 switch 使用，必填。
	ScreenID string `json:"screen_id,omitempty"`
	// Minutes 仅 wake 使用，1–1440，缺省 30。
	Minutes int `json:"minutes,omitempty"`
}

// ScreenOp 是一条远程操作记录。
type ScreenOp struct {
	ID     int64          `json:"id"`
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
	// ClientIP 是发起者来源。
	ClientIP string `json:"client_ip"`
	// Delivered 表示一次性指令（refresh、switch）是否已送达屏幕；状态类操作随状态下发，记为 true。
	Delivered bool      `json:"delivered"`
	At        time.Time `json:"at"`
}

// ScreenControlResponse 是 POST /api/screen/control 的响应。
type ScreenControlResponse struct {
	Op    ScreenOp    `json:"op"`
	State ScreenState `json:"state"`
}

// SetupCodeReveal 是屏幕会话取到的明文设置码。
type SetupCodeReveal struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}
