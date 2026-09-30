// Package cfg 提供内置插件读取 runtime.Input.Config 的小工具。
// 配置经 schema 校验规范化：数字是 float64，时长是字符串；缺省或类型不符时返回默认值。
package cfg

import (
	"time"
)

// String 读取字符串字段。
func String(c map[string]any, key string) string {
	s, _ := c[key].(string)
	return s
}

// Number 读取数字字段，缺省返回 def。
func Number(c map[string]any, key string, def float64) float64 {
	switch v := c[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return def
}

// Bool 读取布尔字段，缺省返回 def。
func Bool(c map[string]any, key string, def bool) bool {
	if v, ok := c[key].(bool); ok {
		return v
	}
	return def
}

// Duration 读取时长字段（形如 "10s"），缺省或不合法返回 def。
func Duration(c map[string]any, key string, def time.Duration) time.Duration {
	if s, ok := c[key].(string); ok {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return def
}
