package model

// 历史数据的档位名称。
const (
	HistoryTierRaw  = "raw"
	HistoryTier5Min = "5m"
	HistoryTier1H   = "1h"
)

// HistoryPoint 是历史曲线上的一个点。T 为时间（Unix 毫秒）：原始档是采样时刻，
// 聚合档是桶起点；原始档的 Avg、Min、Max 三者相等。
type HistoryPoint struct {
	T   int64   `json:"t"`
	Avg float64 `json:"avg"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// HistoryResult 是 GET /api/instances/:id/history 的响应。
type HistoryResult struct {
	InstanceID string `json:"instance_id"`
	Item       string `json:"item"`
	// Field 是实际查询的字段（请求省略时为解析出的默认字段；该数据项没有历史时为空串）。
	Field string `json:"field"`
	// Range 原样回显请求的时间范围。
	Range string `json:"range"`
	// Tier 是按范围选中的档位：raw、5m、1h。
	Tier string `json:"tier"`
	// From、To 是查询的时间窗口（Unix 毫秒）。
	From   int64          `json:"from"`
	To     int64          `json:"to"`
	Points []HistoryPoint `json:"points"`
}
