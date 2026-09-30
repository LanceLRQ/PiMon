// Package model 定义前后端共享的数据模型（前端 TS 类型由此生成）。
package model

// Settings 是全局设置，整份存为一行 JSON。
type Settings struct {
	// Language 界面语言：zh 或 en。
	Language string `json:"language"`
	// Timezone IANA 时区名，如 Asia/Shanghai。
	Timezone string `json:"timezone"`
	// AccessURL 对外访问地址；为空表示未配置。
	AccessURL string `json:"access_url"`
	// TrustedProxies 受信任反代，单个 IP 或 CIDR；序列化时始终为数组。
	TrustedProxies []string `json:"trusted_proxies"`
	// HTTPSEnabled 是否启用 HTTPS；修改后需重启才生效。
	HTTPSEnabled bool `json:"https_enabled"`
	// ReduceEffects 降低屏幕特效。
	ReduceEffects bool              `json:"reduce_effects"`
	Retention     RetentionSettings `json:"retention"`
	Backup        BackupSettings    `json:"backup"`
}

// RetentionSettings 历史数据保留期。
type RetentionSettings struct {
	// RawHours 原始采样保留小时数（1–168）。
	RawHours int `json:"raw_hours"`
	// FiveMinDays 5 分钟聚合保留天数（1–365）。
	FiveMinDays int `json:"five_min_days"`
	// HourDays 1 小时聚合保留天数（1–1825）。
	HourDays int `json:"hour_days"`
}

// BackupSettings 每日备份设置。
type BackupSettings struct {
	// DailyAt 每日备份时刻，HH:MM。
	DailyAt string `json:"daily_at"`
	// Keep 保留份数（1–30）。
	Keep int `json:"keep"`
}
