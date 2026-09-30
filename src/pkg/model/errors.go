package model

import (
	"sort"
	"strings"
)

// 字段错误码。
const (
	FieldInvalid    = "invalid"
	FieldOutOfRange = "out_of_range"
)

// FieldErrors 是校验失败时按字段给出的错误：键为点号路径
// （如 retention.raw_hours、trusted_proxies[1]），值为 FieldInvalid 或 FieldOutOfRange。
type FieldErrors map[string]string

// Error 实现 error 接口，键按字典序输出以保证稳定。
func (e FieldErrors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e[k])
	}
	return "校验失败: " + strings.Join(parts, ", ")
}
