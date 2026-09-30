package schema

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

type cfg = map[string]any

func wantErrs(t *testing.T, fields []Field, in cfg, want model.FieldErrors) {
	t.Helper()
	got := Validate(fields, in)
	if len(got) != len(want) {
		t.Fatalf("错误集合不符: 得到 %v，期望 %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("字段 %s: 得到 %q，期望 %q（全部: %v）", k, got[k], v, got)
		}
	}
}

func TestValidateRequiredAndDefaults(t *testing.T) {
	fs := decode(t, `
- {key: name, type: string, required: true}
- {key: retries, type: number, required: true, default: 3}
`)
	wantErrs(t, fs, cfg{}, model.FieldErrors{"name": model.FieldRequired})
	wantErrs(t, fs, cfg{"name": ""}, model.FieldErrors{"name": model.FieldRequired})
	wantErrs(t, fs, cfg{"name": "x"}, nil)
	clean, errs := Prepare(fs, cfg{"name": "x"})
	if errs != nil || clean["retries"] != float64(3) {
		t.Fatalf("应补默认值: %v %v", clean, errs)
	}
}

func TestValidateString(t *testing.T) {
	fs := decode(t, `- {key: s, type: string, min: 2, max: 4, pattern: "^[a-z]+$"}`)
	wantErrs(t, fs, cfg{"s": "abc"}, nil)
	wantErrs(t, fs, cfg{"s": "a"}, model.FieldErrors{"s": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"s": "abcde"}, model.FieldErrors{"s": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"s": "AB1"}, model.FieldErrors{"s": model.FieldPattern})
	wantErrs(t, fs, cfg{"s": 12}, model.FieldErrors{"s": model.FieldInvalid})
	wantErrs(t, fs, cfg{"unknown": 1}, model.FieldErrors{"unknown": model.FieldInvalid})
}

func TestValidateNumberBooleanEnum(t *testing.T) {
	fs := decode(t, `
- {key: n, type: number, min: 1, max: 10}
- {key: b, type: boolean}
- {key: e, type: enum, options: [x, y]}
`)
	wantErrs(t, fs, cfg{"n": 5, "b": true, "e": "x"}, nil)
	wantErrs(t, fs, cfg{"n": 5.5}, nil)
	wantErrs(t, fs, cfg{"n": 0}, model.FieldErrors{"n": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"n": 11}, model.FieldErrors{"n": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"n": "5"}, model.FieldErrors{"n": model.FieldInvalid})
	wantErrs(t, fs, cfg{"b": "yes"}, model.FieldErrors{"b": model.FieldInvalid})
	wantErrs(t, fs, cfg{"e": "z"}, model.FieldErrors{"e": model.FieldInvalid})
}

func TestValidateDuration(t *testing.T) {
	fs := decode(t, `- {key: d, type: duration, min: 10, max: 3600}`)
	wantErrs(t, fs, cfg{"d": "30s"}, nil)
	wantErrs(t, fs, cfg{"d": "1s"}, model.FieldErrors{"d": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"d": "2h"}, model.FieldErrors{"d": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"d": "abc"}, model.FieldErrors{"d": model.FieldInvalid})
}

func TestValidateSecretsAndProxyAndLookup(t *testing.T) {
	fs := decode(t, `
- {key: k, type: secret, required: true}
- {key: w, type: secret_url}
- {key: p, type: proxy}
- {key: l, type: lookup, required: true}
`)
	wantErrs(t, fs, cfg{"k": "s", "l": "1"}, nil)
	wantErrs(t, fs, cfg{"k": "s", "l": map[string]any{"id": "1", "name": "北京"}}, nil)
	wantErrs(t, fs, cfg{"l": "1"}, model.FieldErrors{"k": model.FieldRequired})
	wantErrs(t, fs, cfg{"k": "s"}, model.FieldErrors{"l": model.FieldRequired})
	wantErrs(t, fs, cfg{"k": "s", "l": "1", "w": "not a url"}, model.FieldErrors{"w": model.FieldInvalid})
	wantErrs(t, fs, cfg{"k": "s", "l": "1", "w": "https://example.com/hook/abc?token=1"}, nil)
	wantErrs(t, fs, cfg{"k": "s", "l": "1", "p": "proxy-1"}, nil)
	wantErrs(t, fs, cfg{"k": "s", "l": "1", "p": 3}, model.FieldErrors{"p": model.FieldInvalid})
}

func TestValidateURLRules(t *testing.T) {
	strict := decode(t, `- {key: u, type: url}`)
	loose := decode(t, `- {key: u, type: url, allow_query: true, allow_public_http: true}`)
	cases := []struct {
		name  string
		f     []Field
		value string
		ok    bool
	}{
		{"公网 https", strict, "https://api.example.com/v1/balance", true},
		{"公网 http 默认拒绝", strict, "http://api.example.com/", false},
		{"公网 http 显式允许", loose, "http://api.example.com/", true},
		{"内网 http 默认允许", strict, "http://192.168.1.10:8080/x", true},
		{"回环 http 允许", strict, "http://localhost:9000", true},
		{"单标签主机名视为内网", strict, "http://nas/status", true},
		{".local 视为内网", strict, "http://pi.local/", true},
		{"CGNAT 视为内网", strict, "http://100.64.1.2/", true},
		{"公网 IP http 拒绝", strict, "http://8.8.8.8/", false},
		{"查询参数默认拒绝", strict, "https://api.example.com/?k=v", false},
		{"查询参数显式允许", loose, "https://api.example.com/?k=v", true},
		{"内嵌凭据拒绝", loose, "https://user:pass@api.example.com/", false},
		{"非 http 协议", strict, "ftp://example.com/", false},
		{"缺主机", strict, "https:///path", false},
		{"垃圾", strict, "::", false},
	}
	for _, c := range cases {
		errs := Validate(c.f, cfg{"u": c.value})
		if c.ok && errs != nil {
			t.Errorf("%s: 应通过，得到 %v", c.name, errs)
		}
		if !c.ok && errs["u"] != model.FieldInvalid {
			t.Errorf("%s: 应为 invalid，得到 %v", c.name, errs)
		}
	}
}

func TestValidateListKV(t *testing.T) {
	fs := decode(t, `
- {key: tags, type: list, min: 1, max: 2, pattern: "^[a-z]+$"}
- {key: headers, type: kv, secret_values: true, max: 2}
`)
	wantErrs(t, fs, cfg{"tags": []any{"a", "b"}}, nil)
	wantErrs(t, fs, cfg{"tags": []any{}}, nil) // 非必填，空列表视为未填
	wantErrs(t, fs, cfg{"tags": []any{"a", "b", "c"}}, model.FieldErrors{"tags": model.FieldOutOfRange})
	wantErrs(t, fs, cfg{"tags": []any{"a", "B"}}, model.FieldErrors{"tags[1]": model.FieldPattern})
	wantErrs(t, fs, cfg{"tags": []any{"a", 1}}, model.FieldErrors{"tags[1]": model.FieldInvalid})
	wantErrs(t, fs, cfg{"tags": "a"}, model.FieldErrors{"tags": model.FieldInvalid})
	wantErrs(t, fs, cfg{"headers": map[string]any{"X-Token": "t"}}, nil)
	wantErrs(t, fs, cfg{"headers": map[string]any{"X-Token": 1}}, model.FieldErrors{"headers.X-Token": model.FieldInvalid})
	wantErrs(t, fs, cfg{"headers": map[string]any{"": "t"}}, model.FieldErrors{"headers": model.FieldInvalid})
	wantErrs(t, fs, cfg{"headers": map[string]any{"a": "1", "b": "2", "c": "3"}}, model.FieldErrors{"headers": model.FieldOutOfRange})
}

func TestValidateObjectList(t *testing.T) {
	fs := decode(t, `
- key: targets
  type: object_list
  required: true
  fields:
    - {key: name, type: string, required: true}
    - {key: url, type: url, required: true}
`)
	good := cfg{"targets": []any{map[string]any{"name": "a", "url": "https://example.com"}}}
	wantErrs(t, fs, good, nil)
	bad := cfg{"targets": []any{
		map[string]any{"name": "a", "url": "https://example.com"},
		map[string]any{"name": "b"},
		map[string]any{"name": "c", "url": "http://example.com"},
		"oops",
	}}
	wantErrs(t, fs, bad, model.FieldErrors{
		"targets[1].url": model.FieldRequired,
		"targets[2].url": model.FieldInvalid,
		"targets[3]":     model.FieldInvalid,
	})
	wantErrs(t, fs, cfg{}, model.FieldErrors{"targets": model.FieldRequired})
	wantErrs(t, fs, cfg{"targets": []any{map[string]any{"name": "a", "url": "https://e.com", "extra": 1}}},
		model.FieldErrors{"targets[0].extra": model.FieldInvalid})
}

func TestVisibleWhen(t *testing.T) {
	fs := decode(t, `
- {key: mode, type: enum, options: [a, b, c], default: a}
- {key: only_a, type: string, required: true, visible_when: {mode: a}}
- {key: b_or_c, type: number, visible_when: {mode: [b, c]}, min: 1}
- {key: chained, type: string, required: true, visible_when: {only_a: hi}}
`)
	// mode=a：only_a 可见且必填；b_or_c 不可见，其非法值被忽略
	wantErrs(t, fs, cfg{"mode": "a", "b_or_c": -5}, model.FieldErrors{"only_a": model.FieldRequired})
	clean, errs := Prepare(fs, cfg{"mode": "a", "only_a": "hi", "b_or_c": -5, "chained": "x"})
	if errs != nil {
		t.Fatalf("不应有错误: %v", errs)
	}
	if _, ok := clean["b_or_c"]; ok {
		t.Errorf("不可见字段不应保存: %v", clean)
	}
	if clean["only_a"] != "hi" || clean["chained"] != "x" {
		t.Errorf("可见字段应保留: %v", clean)
	}
	// 默认值参与条件求值：未传 mode 按默认 a
	wantErrs(t, fs, cfg{}, model.FieldErrors{"only_a": model.FieldRequired})
	// mode=b：only_a 不可见（不再必填）、连带 chained 不可见；b_or_c 可见并校验 min
	wantErrs(t, fs, cfg{"mode": "b", "b_or_c": 0}, model.FieldErrors{"b_or_c": model.FieldOutOfRange})
	clean, errs = Prepare(fs, cfg{"mode": "b", "only_a": "hi", "chained": "x"})
	if errs != nil {
		t.Fatalf("不应有错误: %v", errs)
	}
	if _, ok := clean["only_a"]; ok {
		t.Errorf("only_a 不可见应被丢弃: %v", clean)
	}
	if _, ok := clean["chained"]; ok {
		t.Errorf("依赖不可见字段的 chained 也应被丢弃: %v", clean)
	}
}

func TestVisibleWhenInObjectList(t *testing.T) {
	fs := decode(t, `
- key: items
  type: object_list
  fields:
    - {key: kind, type: enum, options: [http, tcp]}
    - {key: path, type: string, required: true, visible_when: {kind: http}}
`)
	wantErrs(t, fs, cfg{"items": []any{map[string]any{"kind": "tcp"}}}, nil)
	wantErrs(t, fs, cfg{"items": []any{map[string]any{"kind": "http"}}}, model.FieldErrors{"items[0].path": model.FieldRequired})
}

// 手工构造的字段（绕过解析期检查）在求值时也不能 panic。
func TestVisibleWhenNeverPanics(t *testing.T) {
	fs := []Field{
		{Key: "l", Type: TypeLookup},
		{Key: "x", Type: TypeString, VisibleWhen: []Condition{{Key: "l", Values: []any{map[string]any{"a": 1}, []any{1}}}}},
	}
	wantErrs(t, fs, cfg{"l": map[string]any{"a": 1}, "x": "v"}, nil)
	fs2 := []Field{
		{Key: "k", Type: TypeList},
		{Key: "x", Type: TypeString, VisibleWhen: []Condition{{Key: "k", Values: []any{[]any{"a"}}}}},
	}
	wantErrs(t, fs2, cfg{"k": []any{"a"}}, nil)
}

func TestSecretURLRules(t *testing.T) {
	fs := decode(t, `- {key: w, type: secret_url}`)
	cases := []struct {
		value string
		ok    bool
	}{
		{"https://hooks.example.com/abc?token=1", true},
		{"http://hooks.example.com/abc", false},
		{"https://user:pw@hooks.example.com/abc", false},
		{"http://192.168.1.5:8080/hook?k=v", true},
		{"http://gotify.local/message", true},
		{"ftp://hooks.example.com/", false},
		{"not a url", false},
	}
	for _, c := range cases {
		errs := Validate(fs, cfg{"w": c.value})
		if c.ok && errs != nil {
			t.Errorf("%s 应通过: %v", c.value, errs)
		}
		if !c.ok && errs["w"] != model.FieldInvalid {
			t.Errorf("%s 应为 invalid: %v", c.value, errs)
		}
	}
}
