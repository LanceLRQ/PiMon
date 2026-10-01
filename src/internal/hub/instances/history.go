package instances

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// HistoryRecorder 是数值历史的接入点：每次成功采集（定时、手动、Streamer 上报）后，
// 服务把该实例的报告交给它。实现必须快速返回（只做内存攒批），且可被并发调用。
// 实例删除时历史随外键级联删除，不需要额外通知。
type HistoryRecorder interface {
	Record(instanceID string, at time.Time, rep *report.Report)
}

// NopHistory 是不记录任何历史的空实现。
type NopHistory struct{}

// Record 什么也不做。
func (NopHistory) Record(string, time.Time, *report.Report) {}
