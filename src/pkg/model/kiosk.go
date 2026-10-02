package model

import "time"

// KioskIdleCheck 是 kiosk 的息屏检查结果：任一为 true 都表示系统可能自行息屏，需要告警。
type KioskIdleCheck struct {
	// User 为 true 表示用户 labwc autostart 里有 swayidle 行。
	User bool `json:"user"`
	// Greeter 为 true 表示 greeter 的 labwc autostart 里有 swayidle 行。
	Greeter bool `json:"greeter"`
	// System 为 true 表示系统 labwc autostart 里有 swayidle 行。
	System bool `json:"system"`
	// SwayidleRunning 为 true 表示 swayidle 进程正在运行。
	SwayidleRunning bool      `json:"swayidle_running"`
	CheckedAt       time.Time `json:"checked_at"`
}

// KioskReport 是 kiosk 守护进程经 WebSocket 上报的自身状态，每次上报整份覆盖。
type KioskReport struct {
	// Version 是 kiosk 所属二进制的构建版本。
	Version string `json:"version"`
	// ChromiumStartedAt 是当前 Chromium 进程的启动时刻；未在运行为 null。
	ChromiumStartedAt *time.Time `json:"chromium_started_at"`
	// Restarts 是 kiosk 启动以来 Chromium 被重新拉起的次数。
	Restarts int `json:"restarts"`
	// BackoffUntil 是退避中的下次拉起时刻；不在退避中为 null。
	BackoffUntil *time.Time `json:"backoff_until"`
	// Touchscreen 是 udev 检测到触摸屏的结果；未检测为 null。
	Touchscreen *bool `json:"touchscreen"`
	// ChromiumRSSBytes 是 Chromium 进程组的 RSS 合计字节；未采集为 null。
	ChromiumRSSBytes *int64 `json:"chromium_rss_bytes"`
	// IdleCheck 是最近一次息屏检查的结果；未检查为 null。
	IdleCheck *KioskIdleCheck `json:"idle_check"`
}

// KioskStatus 是中枢保存的 kiosk 状态：最近一次上报的内容加中枢维护的字段。
type KioskStatus struct {
	// Online 表示此刻有 kiosk 连接；断开后保留最后一次上报的内容。
	Online bool `json:"online"`
	// LastReportAt 是最近一次收到上报的时刻；连上后尚未上报时为 null。
	LastReportAt *time.Time `json:"last_report_at"`
	// NextRestart 是下一次每日重启的时刻；未开启每日重启时为 null。
	NextRestart *time.Time `json:"next_restart"`

	Version           string          `json:"version"`
	ChromiumStartedAt *time.Time      `json:"chromium_started_at"`
	Restarts          int             `json:"restarts"`
	BackoffUntil      *time.Time      `json:"backoff_until"`
	Touchscreen       *bool           `json:"touchscreen"`
	ChromiumRSSBytes  *int64          `json:"chromium_rss_bytes"`
	IdleCheck         *KioskIdleCheck `json:"idle_check"`
}
