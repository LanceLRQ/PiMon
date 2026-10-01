package model

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// InstanceInput 是创建与更新实例的请求体。
type InstanceInput struct {
	// PluginID 创建时必填；更新时可省略，出现且与原值不同视为非法（实例不能换插件）。
	PluginID string `json:"plugin_id,omitempty"`
	Name     string `json:"name"`
	// Config 是完整配置；密钥字段更新时缺省、为空串或回显的 {"set":true} 表示保留原值。
	Config map[string]any `json:"config"`
	// IntervalSeconds 是刷新间隔覆盖，0 表示沿用 manifest 的默认间隔。
	IntervalSeconds int `json:"interval_seconds"`
}

// Instance 是实例列表项，也是详情与运行结果的公共部分。
type Instance struct {
	ID       string `json:"id"`
	PluginID string `json:"plugin_id"`
	Name     string `json:"name"`
	// RunsOn 本期固定为 hub。
	RunsOn string `json:"runs_on"`
	// IntervalSeconds 是覆盖值（0 为未覆盖）；EffectiveIntervalSeconds 是实际生效的间隔。
	IntervalSeconds          int  `json:"interval_seconds"`
	EffectiveIntervalSeconds int  `json:"effective_interval_seconds"`
	Paused                   bool `json:"paused"`
	// DisplayState 是统一展示状态（设计 3.5）；暂停的实例不做过期判定。
	DisplayState string `json:"display_state"`
	// Summary 与 ReportStatus 取自当前报告，从未成功采集时为空。
	Summary       string     `json:"summary"`
	ReportStatus  string     `json:"report_status"`
	ReportStale   bool       `json:"report_stale"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	// LastError 是最近一次失败的（已脱敏）文字，最近一次成功后为空。
	LastError string `json:"last_error,omitempty"`
	Failures  int    `json:"failures"`
	// Issue 说明实例为何无法运行（插件消失、配置不完整、代理不可用等），可运行时为空。
	Issue     string    `json:"issue,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InstanceDetail 是单个实例的详情。
type InstanceDetail struct {
	Instance
	// Config 已脱敏：已设置的密钥显示为 {"set":true}。
	Config map[string]any `json:"config"`
	// Problems 是配置与当前插件 schema 不符的字段（按路径）。
	Problems FieldErrors `json:"problems,omitempty"`
	// Report 是当前报告，采集失败时为保留的旧值（已标记过期）；从未成功时为 null。
	Report *report.Report `json:"report"`
}

// InstanceRunResult 是「保存并测试」的响应：同步运行一次的报告与运行后的实例状态。
type InstanceRunResult struct {
	Instance Instance       `json:"instance"`
	Report   *report.Report `json:"report"`
}

// ScreenRef 是引用某实例的 screen。
type ScreenRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// InstanceDeleteResult 是删除实例的响应。screens 表在 M1d 才有，本期恒为空。
type InstanceDeleteResult struct {
	AffectedScreens []ScreenRef `json:"affected_screens"`
}
