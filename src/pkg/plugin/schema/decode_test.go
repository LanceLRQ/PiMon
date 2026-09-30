package schema

import (
	"strings"
	"testing"
)

func TestDecodeAllTypes(t *testing.T) {
	src := `
- {key: a, type: string, title: {zh: 甲, en: A}, required: true, pattern: "^[a-z]+$", min: 1, max: 8}
- {key: b, type: text}
- {key: c, type: number, default: 3, min: 0, max: 10}
- {key: d, type: boolean, default: true}
- {key: e, type: enum, options: [x, {value: y, title: {zh: 为, en: Why}}]}
- {key: f, type: secret}
- {key: g, type: secret_url}
- {key: h, type: url, allow_query: true, allow_public_http: true, follow_redirects: true}
- {key: i, type: proxy}
- {key: j, type: duration, default: 30s}
- {key: k, type: list}
- {key: l, type: kv, secret_values: true}
- {key: m, type: object_list, fields: [{key: n, type: string}]}
- {key: o, type: lookup}
`
	fs := decode(t, src)
	if len(fs) != 14 {
		t.Fatalf("应解析 14 个字段，得到 %d", len(fs))
	}
	a := fs[0]
	if a.Key != "a" || a.Type != TypeString || !a.Required || a.Title.Get("zh") != "甲" || a.Line != lineOf(t, src, "key: a") {
		t.Errorf("字段 a 解析错误: %+v", a)
	}
	if a.Min == nil || *a.Min != 1 || a.Max == nil || *a.Max != 8 || a.Pattern != "^[a-z]+$" {
		t.Errorf("min/max/pattern 解析错误: %+v", a)
	}
	if len(fs[4].Options) != 2 || fs[4].Options[0].Value != "x" || fs[4].Options[1].Title.Get("en") != "Why" {
		t.Errorf("enum options 解析错误: %+v", fs[4].Options)
	}
	if !fs[7].AllowQuery || !fs[7].AllowPublicHTTP || !fs[7].FollowRedirects {
		t.Errorf("url 选项解析错误: %+v", fs[7])
	}
	if !fs[11].SecretValues {
		t.Error("kv 的 secret_values 未解析")
	}
	if len(fs[12].Fields) != 1 || fs[12].Fields[0].Key != "n" {
		t.Errorf("object_list 子字段解析错误: %+v", fs[12].Fields)
	}
}

func TestDecodeKeepsOrder(t *testing.T) {
	fs := decode(t, "- {key: z, type: string}\n- {key: a, type: string}\n")
	if fs[0].Key != "z" || fs[1].Key != "a" {
		t.Fatalf("应保持声明顺序: %+v", fs)
	}
}

func TestDecodeIssues(t *testing.T) {
	cases := []struct {
		name, src, needle, msg string
	}{
		{"重复 key", "- {key: a, type: string}\n- {key: a, type: number}\n", "key: a, type: number", "重复"},
		{"非法 type", "- {key: a, type: string}\n- {key: b, type: bogus}\n", "type: bogus", "type"},
		{"缺 key", "- {type: string}\n", "type: string", "key"},
		{"缺 type", "- {key: a}\n", "key: a", "type"},
		{"未知属性", "- {key: a, type: string, colour: red}\n", "colour", "未知"},
		{"enum 无 options", "- {key: a, type: enum}\n", "type: enum", "options"},
		{"pattern 非法", "- {key: a, type: string, pattern: \"(\"}\n", "pattern", "pattern"},
		{"min 大于 max", "- {key: a, type: number, min: 5, max: 1}\n", "min: 5", "min"},
		{"visible_when 引用未知字段", "- {key: a, type: string, visible_when: {nope: x}}\n", "visible_when", "nope"},
		{"object_list 无子字段", "- {key: a, type: object_list}\n", "type: object_list", "fields"},
		{"secret_values 只用于 kv", "- {key: a, type: string, secret_values: true}\n", "secret_values", "secret_values"},
		{"key 格式", "- {key: \"bad key\", type: string}\n", "bad key", "key"},
		{"默认值不合法", "- {key: a, type: number, default: x}\n", "default: x", "default"},
		{"enum 默认值不在选项内", "- {key: a, type: enum, options: [p, q], default: z}\n", "default: z", "default"},
		{"子字段重复", "- {key: a, type: object_list, fields: [{key: n, type: string}, {key: n, type: string}]}\n", "", "重复"},
	}
	for _, c := range cases {
		_, issues := decodeSrc(t, c.src)
		if len(issues) == 0 {
			t.Errorf("%s: 应报告问题", c.name)
			continue
		}
		found := false
		for _, is := range issues {
			if strings.Contains(is.Message, c.msg) || strings.Contains(is.Path, c.msg) {
				found = true
				if c.needle != "" && is.Line != lineOf(t, c.src, c.needle) {
					t.Errorf("%s: 行号 %d，期望 %d (%+v)", c.name, is.Line, lineOf(t, c.src, c.needle), is)
				}
			}
		}
		if !found {
			t.Errorf("%s: 未找到含 %q 的问题: %+v", c.name, c.msg, issues)
		}
	}
}

func TestDecodeNotSequence(t *testing.T) {
	_, issues := decodeSrc(t, "a: 1\n")
	if len(issues) == 0 {
		t.Fatal("非序列应报错")
	}
}

func TestDecodeVisibleWhenMustBeScalar(t *testing.T) {
	cases := []struct{ name, src, needle string }{
		{"条件值为映射", "- {key: a, type: enum, options: [x]}\n- {key: b, type: string, visible_when: {a: {k: 1}}}\n", "visible_when"},
		{"条件值列表含列表", "- {key: a, type: enum, options: [x]}\n- {key: b, type: string, visible_when: {a: [[1]]}}\n", "visible_when"},
		{"引用 lookup 字段", "- {key: a, type: lookup}\n- {key: b, type: string, visible_when: {a: x}}\n", "visible_when"},
		{"引用 kv 字段", "- {key: a, type: kv}\n- {key: b, type: string, visible_when: {a: x}}\n", "visible_when"},
		{"引用 list 字段", "- {key: a, type: list}\n- {key: b, type: string, visible_when: {a: x}}\n", "visible_when"},
	}
	for _, c := range cases {
		_, issues := decodeSrc(t, c.src)
		if len(issues) == 0 {
			t.Errorf("%s: 应报告问题", c.name)
			continue
		}
		if issues[0].Line != lineOf(t, c.src, c.needle) {
			t.Errorf("%s: 行号 %d 不对: %+v", c.name, issues[0].Line, issues)
		}
	}
	// 引用 enum、boolean、string、number 合法
	decode(t, "- {key: a, type: enum, options: [x]}\n- {key: n, type: number}\n- {key: s, type: string}\n- {key: b, type: boolean}\n- {key: z, type: string, visible_when: {a: x, n: 1, s: q, b: true}}\n")
}
