package report

import "time"

// DisplayState 是实例/小组件的统一展示状态（设计 3.5）。
type DisplayState string

// 展示状态，共 10 个取值；前六个是覆盖 report.status 的状态，后四个对应 report.status。
const (
	DisplayUnconfigured DisplayState = "unconfigured" // 占位，尚未绑定实例
	DisplayBroken       DisplayState = "broken"       // 引用失效
	DisplayOffline      DisplayState = "offline"      // 设备离线
	DisplayMaintenance  DisplayState = "maintenance"  // 维护中
	DisplayError        DisplayState = "error"        // 采集失败：采集没做成
	DisplayStale        DisplayState = "stale"        // 过期
	DisplayOK           DisplayState = "ok"
	DisplayWarning      DisplayState = "warning"
	DisplayCritical     DisplayState = "critical"
	DisplayUnknown      DisplayState = "unknown" // 插件判断不出，与 error 不同
)

// StaleFactor 是过期判定的间隔倍数：超过刷新间隔的 3 倍仍未成功才算过期。
const StaleFactor = 3

// DisplayInput 是计算展示状态所需的事实，由调用方（实例仓库、屏幕、agent）收集。
type DisplayInput struct {
	Unconfigured bool // 占位，尚未绑定实例
	Broken       bool // 引用失效（实例已删、设备已吊销、插件消失或尺寸不再支持）
	Offline      bool // 设备离线（M1 不会出现）
	Maintenance  bool // 设备维护模式（M1 不会出现）
	LastFailed   bool // 最近一次采集失败
	// LastSuccessAt 是最后一次成功采集（Streamer 为最后一次 emit）的 Unix 毫秒；0 表示从未成功。
	LastSuccessAt int64
	// Interval 是刷新间隔；StaleAfter > 0 时（Streamer 按 manifest 声明）以它为过期阈值，忽略 Interval。
	Interval   time.Duration
	StaleAfter time.Duration
	// ReportStatus 是最近一份成功报告的 status；空或非法按 unknown 处理。
	ReportStatus Status
}

// ComputeDisplayState 按优先级取第一个成立的状态：
// 未配置 > 引用失效 > 离线 > 维护 > 采集失败 > 过期 > report.status。
// now 由调用方传入（生产用 clock.Now()，测试用假时钟）。
func ComputeDisplayState(in DisplayInput, now time.Time) DisplayState {
	switch {
	case in.Unconfigured:
		return DisplayUnconfigured
	case in.Broken:
		return DisplayBroken
	case in.Offline:
		return DisplayOffline
	case in.Maintenance:
		return DisplayMaintenance
	case in.LastFailed:
		return DisplayError
	case IsStale(in.LastSuccessAt, in.Interval, in.StaleAfter, now):
		return DisplayStale
	}
	switch in.ReportStatus {
	case StatusOK:
		return DisplayOK
	case StatusWarning:
		return DisplayWarning
	case StatusCritical:
		return DisplayCritical
	}
	return DisplayUnknown
}

// IsStale 判断数据是否过期：距最后一次成功严格超过阈值才算（恰好等于不算）。
// 阈值为 staleAfter（> 0 时），否则为 StaleFactor × interval。
// 从未成功（lastSuccessAt <= 0），或阈值无法确定（两者都 <= 0）时返回 false，由其它状态表达。
func IsStale(lastSuccessAt int64, interval, staleAfter time.Duration, now time.Time) bool {
	if lastSuccessAt <= 0 {
		return false
	}
	threshold := staleAfter
	if threshold <= 0 {
		threshold = StaleFactor * interval
	}
	if threshold <= 0 {
		return false
	}
	return now.UnixMilli()-lastSuccessAt > threshold.Milliseconds()
}
