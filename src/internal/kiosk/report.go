package kiosk

import "github.com/LanceLRQ/PiMon/src/pkg/model"

// reportSources 是 FillReport 取数的来源，各项都是并发安全的读取函数。
type reportSources struct {
	Touch func() TouchResult
	Idle  func() *model.KioskIdleCheck
	// PID 返回运行中的 Chromium 组长 pid，未运行为 0。
	PID func() int
	// RSS 返回进程组 RSS 合计字节，读不到返回 false。
	RSS func(pgid int) (int64, bool)
}

// Fill 把触摸检测、息屏检查与 Chromium RSS 合并进上报；未知的项保持 nil（null）。
func (s reportSources) Fill(r *model.KioskReport) {
	r.Touchscreen = s.Touch().Touchscreen()
	r.IdleCheck = s.Idle()
	r.ChromiumRSSBytes = nil
	if pid := s.PID(); pid > 0 {
		if v, ok := s.RSS(pid); ok {
			r.ChromiumRSSBytes = &v
		}
	}
}
