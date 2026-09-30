package store

import "time"

// FormatTime 把时间格式化为 UTC 的 RFC3339Nano 字符串，数据库中的时间列统一使用该格式。
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// ParseTime 解析 FormatTime 产出的字符串，返回 UTC 时间。
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
