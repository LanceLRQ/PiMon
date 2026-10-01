package report

import (
	"fmt"
	"slices"
	"strings"
)

// IsType 报告 t 是否为已知数据项类型。
func IsType(t string) bool { return slices.Contains(ItemTypes, t) }

// FieldsOf 返回类型 t 的字段集（副本）；未知类型返回 nil。
func FieldsOf(t string) []string {
	f, ok := typeFields[t]
	if !ok {
		return nil
	}
	return slices.Clone(f)
}

// DefaultField 返回类型 t 的默认字段；table 与未知类型没有默认字段，返回 false。
func DefaultField(t string) (string, bool) {
	f, ok := typeDefault[t]
	return f, ok
}

// ValidField 报告 field 是否属于类型 t 的字段集。
func ValidField(t, field string) bool {
	return slices.Contains(typeFields[t], field)
}

// ResolveField 把字段引用中的 field 解析为具体字段：省略时取默认字段，
// 显式给出时必须属于类型 t 的字段集。
func ResolveField(t, field string) (string, error) {
	if !IsType(t) {
		return "", fmt.Errorf("未知的数据项类型 %q", t)
	}
	if field == "" {
		def, ok := DefaultField(t)
		if !ok {
			return "", fmt.Errorf("类型 %s 没有默认字段，引用时必须写明 field", t)
		}
		return def, nil
	}
	if !ValidField(t, field) {
		return "", fmt.Errorf("字段 %q 不属于类型 %s，可用：%s", field, t, strings.Join(typeFields[t], "、"))
	}
	return field, nil
}
