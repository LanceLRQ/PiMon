package schema

import (
	"reflect"
	"sort"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const secretSrc = `
- {key: account, type: string}
- {key: api_key, type: secret}
- {key: hook, type: secret_url}
- {key: headers, type: kv, secret_values: true}
- {key: labels, type: kv}
- key: accounts
  type: object_list
  fields:
    - {key: name, type: string}
    - {key: token, type: secret}
`

func fullConfig() map[string]any {
	return map[string]any{
		"account": "me",
		"api_key": "K1",
		"hook":    "https://h.example.com/x",
		"headers": map[string]any{"X-Token": "T", "X-Id": "I"},
		"labels":  map[string]any{"env": "prod"},
		"accounts": []any{
			map[string]any{"name": "a", "token": "TA"},
			map[string]any{"name": "b", "token": "TB"},
		},
	}
}

func TestSecretPatterns(t *testing.T) {
	got := SecretPatterns(decode(t, secretSrc))
	sort.Strings(got)
	want := []string{"accounts[].token", "api_key", "headers.*", "hook"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("得到 %v，期望 %v", got, want)
	}
}

func TestSecretPaths(t *testing.T) {
	got := SecretPaths(decode(t, secretSrc), fullConfig())
	sort.Strings(got)
	want := []string{"accounts[0].token", "accounts[1].token", "api_key", "headers.X-Id", "headers.X-Token", "hook"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("得到 %v，期望 %v", got, want)
	}
}

func TestSplitMergeRoundTrip(t *testing.T) {
	fs := decode(t, secretSrc)
	in := fullConfig()
	plain, secrets := Split(fs, in)

	if _, ok := plain["api_key"]; ok {
		t.Errorf("普通配置不应含密钥: %v", plain)
	}
	if _, ok := plain["hook"]; ok {
		t.Errorf("普通配置不应含 secret_url: %v", plain)
	}
	if plain["account"] != "me" {
		t.Errorf("普通字段应保留: %v", plain)
	}
	hdr := plain["headers"].(map[string]any)
	if v, ok := hdr["X-Token"]; !ok || v != nil {
		t.Errorf("kv 密钥值应以 nil 占位保留键名: %v", hdr)
	}
	if plain["labels"].(map[string]any)["env"] != "prod" {
		t.Errorf("非密钥 kv 应原样保留: %v", plain["labels"])
	}
	if plain["accounts"].([]any)[0].(map[string]any)["name"] != "a" {
		t.Errorf("对象列表普通字段应保留: %v", plain["accounts"])
	}
	if secrets["api_key"] != "K1" || secrets["headers.X-Token"] != "T" || secrets["accounts[1].token"] != "TB" || len(secrets) != 6 {
		t.Errorf("密钥集合不符: %v", secrets)
	}
	if in["api_key"] != "K1" {
		t.Error("Split 不应修改入参")
	}
	if !reflect.DeepEqual(Merge(fs, plain, secrets), in) {
		t.Errorf("Merge 应还原原配置: %v", Merge(fs, plain, secrets))
	}
}

func TestRedact(t *testing.T) {
	fs := decode(t, secretSrc)
	out := Redact(fs, fullConfig())
	set := map[string]any{"set": true}
	if !reflect.DeepEqual(out["api_key"], set) || !reflect.DeepEqual(out["hook"], set) {
		t.Errorf("密钥应回显 {set:true}: %v", out)
	}
	if !reflect.DeepEqual(out["headers"].(map[string]any)["X-Token"], set) {
		t.Errorf("kv 密钥值应回显 {set:true}: %v", out["headers"])
	}
	if !reflect.DeepEqual(out["accounts"].([]any)[1].(map[string]any)["token"], map[string]any{"set": true, "ref": 1}) {
		t.Errorf("对象列表密钥应回显 {set:true, ref:原下标}: %v", out["accounts"])
	}
	if out["account"] != "me" {
		t.Errorf("普通字段不变: %v", out)
	}
	// 未设置的密钥不回显 set
	out = Redact(fs, map[string]any{"account": "me"})
	if _, ok := out["api_key"]; ok {
		t.Errorf("未设置的密钥不应出现: %v", out)
	}
}

func TestKeepSecrets(t *testing.T) {
	fs := decode(t, secretSrc)
	existing := fullConfig()
	incoming := map[string]any{
		"account": "changed",
		"api_key": "",                          // 留空 -> 保留
		"hook":    map[string]any{"set": true}, // 回显值 -> 保留
		"headers": map[string]any{
			"X-Token": nil,   // 保留
			"X-Id":    "NEW", // 改写
			"X-Extra": "E",   // 新增
		},
		"accounts": []any{
			map[string]any{"name": "a", "token": map[string]any{"set": true, "ref": 0}},
			map[string]any{"name": "b", "token": "NEWB"},
		},
	}
	out, errs := KeepSecrets(fs, incoming, existing)
	if errs != nil {
		t.Fatalf("不应有错误: %v", errs)
	}
	if out["api_key"] != "K1" || out["hook"] != "https://h.example.com/x" {
		t.Errorf("标量密钥应保留原值: %v", out)
	}
	h := out["headers"].(map[string]any)
	if h["X-Token"] != "T" || h["X-Id"] != "NEW" || h["X-Extra"] != "E" || len(h) != 3 {
		t.Errorf("kv 密钥合并错误: %v", h)
	}
	acc := out["accounts"].([]any)
	if acc[0].(map[string]any)["token"] != "TA" || acc[1].(map[string]any)["token"] != "NEWB" {
		t.Errorf("对象列表密钥合并错误: %v", acc)
	}
	if out["account"] != "changed" {
		t.Errorf("普通字段取新值: %v", out)
	}
	if incoming["api_key"] != "" {
		t.Error("KeepSecrets 不应修改入参")
	}
	out, errs = KeepSecrets(fs, map[string]any{"api_key": ""}, map[string]any{})
	if _, ok := out["api_key"]; ok || errs != nil {
		t.Errorf("无原值时留空应视为未设置: %v %v", out, errs)
	}
}

func refItem(name string, ref int) map[string]any {
	return map[string]any{"name": name, "token": map[string]any{"set": true, "ref": ref}}
}

func tokens(out map[string]any) []any {
	var r []any
	for _, it := range out["accounts"].([]any) {
		r = append(r, it.(map[string]any)["token"])
	}
	return r
}

func TestKeepSecretsObjectListRef(t *testing.T) {
	fs := decode(t, secretSrc)
	// 删除第一个元素：剩下的元素带着原下标 1
	out, errs := KeepSecrets(fs, map[string]any{"accounts": []any{refItem("b", 1)}}, fullConfig())
	if errs != nil || !reflect.DeepEqual(tokens(out), []any{"TB"}) {
		t.Errorf("删除第一个元素后应仍取到 TB: %v %v", tokens(out), errs)
	}
	// 交换顺序
	out, errs = KeepSecrets(fs, map[string]any{"accounts": []any{refItem("b", 1), refItem("a", 0)}}, fullConfig())
	if errs != nil || !reflect.DeepEqual(tokens(out), []any{"TB", "TA"}) {
		t.Errorf("交换顺序后应按 ref 取值: %v %v", tokens(out), errs)
	}
	// ref 越界
	_, errs = KeepSecrets(fs, map[string]any{"accounts": []any{refItem("a", 5)}}, fullConfig())
	if errs["accounts[0].token"] != model.FieldRequired {
		t.Errorf("ref 越界应报 required: %v", errs)
	}
	// 留空且无 ref：该字段非必填时保持未设置，不报错
	_, errs = KeepSecrets(fs, map[string]any{"accounts": []any{map[string]any{"name": "x"}}}, fullConfig())
	if errs != nil {
		t.Errorf("非必填密钥留空且无 ref 不应报错: %v", errs)
	}
	// 必填密钥留空且无 ref、ref 指向的旧元素缺该密钥：报 required
	req := decode(t, `
- key: accounts
  type: object_list
  fields:
    - {key: token, type: secret, required: true}
`)
	_, errs = KeepSecrets(req, map[string]any{"accounts": []any{map[string]any{}}}, map[string]any{})
	if errs["accounts[0].token"] != model.FieldRequired {
		t.Errorf("必填密钥无 ref 应报 required: %v", errs)
	}
	old := map[string]any{"accounts": []any{map[string]any{}}}
	_, errs = KeepSecrets(req, map[string]any{"accounts": []any{map[string]any{"token": map[string]any{"set": true, "ref": 0}}}}, old)
	if errs["accounts[0].token"] != model.FieldRequired {
		t.Errorf("旧元素无该密钥应报 required: %v", errs)
	}
}
