package kiosk

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
)

// loadLocation 解析 IANA 时区；空串或无法解析时退回本地时区（后者同时返回错误供记日志）。
func loadLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local, err
	}
	return loc, nil
}

// nextDailyRestart 返回严格晚于 now 的下一次 loc 时区 at（HH:MM）时刻。
func nextDailyRestart(now time.Time, loc *time.Location, at string) (time.Time, error) {
	return backup.NextRun(now, loc, at)
}
