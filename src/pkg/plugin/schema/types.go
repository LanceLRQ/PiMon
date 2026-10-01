package schema

import (
	"regexp"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/i18n"
)

// Type 是 config_schema 字段类型。
type Type string

// 14 种字段类型。
const (
	TypeString     Type = "string"
	TypeText       Type = "text"
	TypeNumber     Type = "number"
	TypeBoolean    Type = "boolean"
	TypeEnum       Type = "enum"
	TypeSecret     Type = "secret"
	TypeSecretURL  Type = "secret_url"
	TypeURL        Type = "url"
	TypeProxy      Type = "proxy"
	TypeDuration   Type = "duration"
	TypeList       Type = "list"
	TypeKV         Type = "kv"
	TypeObjectList Type = "object_list"
	TypeLookup     Type = "lookup"
)

// Types 按设计文档顺序列出全部字段类型。
var Types = []Type{
	TypeString, TypeText, TypeNumber, TypeBoolean, TypeEnum, TypeSecret, TypeSecretURL,
	TypeURL, TypeProxy, TypeDuration, TypeList, TypeKV, TypeObjectList, TypeLookup,
}

// Valid 报告 t 是否为 14 种类型之一。
func (t Type) Valid() bool {
	for _, x := range Types {
		if x == t {
			return true
		}
	}
	return false
}

// Option 是 enum 字段的一个选项。
type Option struct {
	Value string
	Title i18n.Text
}

// Condition 是 visible_when 中的一项：字段 Key 的有效值等于 Values 中任意一个。
type Condition struct {
	Key    string
	Values []any
}

// Field 是 config_schema 中的一个字段定义。
type Field struct {
	Key      string
	Type     Type
	Title    i18n.Text
	Help     i18n.Text
	Required bool
	Default  any // 已规范化：数字统一为 float64
	Min, Max *float64
	Pattern  string

	VisibleWhen []Condition // 全部成立才显示

	Options []Option // enum

	AllowQuery      bool // url
	AllowPublicHTTP bool // url
	FollowRedirects bool // url：由执行端读取

	SecretValues bool    // kv：值为密钥
	Fields       []Field // object_list 的子字段

	// Line 是字段定义在 plugin.yaml 中的行号（从 1 起）。
	Line int

	re *regexp.Regexp // Pattern 编译结果
}

// Issue 是解析或自检时发现的一个问题。
type Issue struct {
	Line    int    // yaml 行号，0 表示未知
	Path    string // 问题所在位置，如 config_schema[2].key
	Message string
}
