package settings

import (
	"strings"
	"time"
)

const localtimePath = "/etc/localtime"

// DetectTimezone 探测系统时区：优先 TZ 环境变量，其次 /etc/localtime 链接中
// "zoneinfo/" 之后的部分，都不可用（或不是合法时区）时返回 UTC。
// getenv 与 readlink 可注入，便于测试。
func DetectTimezone(getenv func(string) string, readlink func(string) (string, error)) string {
	if tz := strings.TrimPrefix(getenv("TZ"), ":"); validZone(tz) {
		return tz
	}
	if target, err := readlink(localtimePath); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			if tz := target[i+len("zoneinfo/"):]; validZone(tz) {
				return tz
			}
		}
	}
	return "UTC"
}

// validZone 要求非空、不是 Local 占位，且能被 LoadLocation 加载。
func validZone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
