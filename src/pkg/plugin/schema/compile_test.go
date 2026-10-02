package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// fromJSON 把 JSON 形式的字段定义解码为 []Field（不经 YAML 解码，re 未编译）。
func fromJSON(t *testing.T, src string) []Field {
	t.Helper()
	var fields []Field
	if err := json.Unmarshal([]byte(src), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestCompile预编译含子字段的正则(t *testing.T) {
	fields := fromJSON(t, `[
		{"Key":"name","Type":"string","Pattern":"^[a-z]+$"},
		{"Key":"plain","Type":"string"},
		{"Key":"rows","Type":"object_list","Fields":[{"Key":"host","Type":"string","Pattern":"^h\\d$"}]}
	]`)
	if err := Compile(fields); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields[0].re == nil || !fields[0].re.MatchString("abc") {
		t.Fatalf("顶层 pattern 未编译")
	}
	if fields[1].re != nil {
		t.Fatalf("无 pattern 的字段不应有正则")
	}
	if fields[2].Fields[0].re == nil || !fields[2].Fields[0].re.MatchString("h1") {
		t.Fatalf("object_list 子字段 pattern 未编译")
	}
}

func TestCompile非法正则返回出错路径(t *testing.T) {
	fields := fromJSON(t, `[
		{"Key":"ok","Type":"string"},
		{"Key":"rows","Type":"object_list","Fields":[{"Key":"x","Type":"string","Pattern":"("}]}
	]`)
	err := Compile(fields)
	if err == nil {
		t.Fatal("非法正则应返回错误")
	}
	if !strings.Contains(err.Error(), "config_schema[1].fields[0].pattern") {
		t.Fatalf("错误应指出路径，得到 %v", err)
	}
}

func TestCompile后校验不再写入字段(t *testing.T) {
	fields := fromJSON(t, `[{"Key":"name","Type":"string","Pattern":"^[a-z]+$"}]`)
	if err := Compile(fields); err != nil {
		t.Fatal(err)
	}
	before := fields[0].re
	if errs := Validate(fields, map[string]any{"name": "ABC"}); errs["name"] == "" {
		t.Fatalf("应按 pattern 拒绝，得到 %v", errs)
	}
	if fields[0].re != before {
		t.Fatal("校验不应替换已编译的正则")
	}
}
