package report

import (
	"errors"
	"strings"
)

// Key 是数据项键名的解析结果。固定键如 quota.5h；动态集合成员如 disk[/vol1]、
// container[nginx]；disk[*] 是规则中使用的通配（只出现在引用里，不会是报告中的 key）。
type Key struct {
	Prefix   string // 固定键的全名，或动态集合的前缀（disk）
	Member   string // 动态成员（/vol1）；通配时为 "*"
	Dynamic  bool
	Wildcard bool
}

// ParseKey 解析键名。成员取第一个 "[" 到末尾 "]" 之间的全部内容，可含 "]" 与 "["
// 以外的任意字符（如路径）；前缀与成员均不得为空，"]" 之后不得有多余字符。
func ParseKey(s string) (Key, error) {
	if s == "" {
		return Key{}, errors.New("键名为空")
	}
	i := strings.IndexByte(s, '[')
	if i < 0 {
		return Key{Prefix: s}, nil
	}
	if i == 0 {
		return Key{}, errors.New("动态键名缺少前缀")
	}
	if !strings.HasSuffix(s, "]") {
		return Key{}, errors.New("动态键名必须以 ] 结尾")
	}
	member := s[i+1 : len(s)-1]
	if member == "" {
		return Key{}, errors.New("动态键名的成员为空")
	}
	return Key{Prefix: s[:i], Member: member, Dynamic: true, Wildcard: member == "*"}, nil
}

// Matches 判断具体键名 concrete 是否被 k 命中：通配命中同前缀的全部动态成员，
// 其余情形要求与 k 完全相同；固定键不会命中动态成员。
func (k Key) Matches(concrete string) bool {
	c, err := ParseKey(concrete)
	if err != nil {
		return false
	}
	if k.Wildcard {
		return c.Dynamic && !c.Wildcard && c.Prefix == k.Prefix
	}
	return c == k
}

// Ref 是结构化字段引用；Field 为空表示取默认字段。
type Ref struct {
	Item  string
	Field string
}

// Find 按完整键名查找数据项，不存在返回 nil。
func (r Report) Find(key string) *Item {
	for i := range r.Items {
		if r.Items[i].Key == key {
			return &r.Items[i]
		}
	}
	return nil
}

// Select 按键名或通配（disk[*]）选出数据项，保持报告中的顺序；模式非法时返回空。
func (r Report) Select(pattern string) []Item {
	k, err := ParseKey(pattern)
	if err != nil {
		return nil
	}
	var out []Item
	for _, it := range r.Items {
		if k.Matches(it.Key) {
			out = append(out, it)
		}
	}
	return out
}

// Lookup 解析引用：返回数据项与具体字段名。引用的数据项不在报告中（缺失）时返回 nil, "", nil，
// 由调用方显示为“未知”；字段不属于该类型时报错；table 省略 field 合法，返回的字段为空。
// ref.Item 必须是具体键名，通配请先用 Select 展开。
func (r Report) Lookup(ref Ref) (*Item, string, error) {
	it := r.Find(ref.Item)
	if it == nil {
		return nil, "", nil
	}
	if ref.Field == "" && it.Type == TypeTable {
		return it, "", nil
	}
	f, err := ResolveField(it.Type, ref.Field)
	if err != nil {
		return it, "", err
	}
	return it, f, nil
}
