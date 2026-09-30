package schema

import (
	"fmt"
	"sort"
)

// secretKind 区分密钥叶子的形态。
type secretKind int

const (
	secretScalar  secretKind = iota // secret / secret_url
	secretKVValue                   // kv(secret_values) 的某个值
)

// SecretPatterns 返回 schema 中所有密钥位置的静态模式：api_key、headers.*、accounts[].token。
func SecretPatterns(fields []Field) []string {
	return appendPatterns(nil, fields, "")
}

func appendPatterns(out []string, fields []Field, prefix string) []string {
	for _, f := range fields {
		path := joinPath(prefix, f.Key)
		switch {
		case f.Type == TypeSecret || f.Type == TypeSecretURL:
			out = append(out, path)
		case f.Type == TypeKV && f.SecretValues:
			out = append(out, path+".*")
		case f.Type == TypeObjectList:
			out = appendPatterns(out, f.Fields, path+"[]")
		}
	}
	return out
}

// walkSecrets 遍历 cfg 中的密钥叶子：标量密钥无论是否存在都会被访问，
// kv 密钥按现有键访问，object_list 按现有元素下标递归。
func walkSecrets(fields []Field, cfg map[string]any, prefix string, visit func(path string, kind secretKind, container map[string]any, key string)) {
	for i := range fields {
		f := &fields[i]
		path := joinPath(prefix, f.Key)
		switch {
		case f.Type == TypeSecret || f.Type == TypeSecretURL:
			visit(path, secretScalar, cfg, f.Key)
		case f.Type == TypeKV && f.SecretValues:
			m, _ := cfg[f.Key].(map[string]any)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				visit(path+"."+k, secretKVValue, m, k)
			}
		case f.Type == TypeObjectList:
			items, _ := cfg[f.Key].([]any)
			for idx, it := range items {
				if m, ok := it.(map[string]any); ok {
					walkSecrets(f.Fields, m, fmt.Sprintf("%s[%d]", path, idx), visit)
				}
			}
		}
	}
}

// isKeepMarker 判断密钥值是否表示「保留原值」：缺省、nil、空串或回显的 {"set": true}。
func isKeepMarker(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case map[string]any:
		s, ok := x["set"].(bool)
		return ok && s && len(x) == 1
	}
	return false
}

// SecretPaths 返回 cfg 中已有值的密钥的具体路径（api_key、headers.X-Token、accounts[1].token）。
func SecretPaths(fields []Field, cfg map[string]any) []string {
	var out []string
	walkSecrets(fields, cfg, "", func(path string, kind secretKind, c map[string]any, key string) {
		v, present := c[key]
		if (kind == secretKVValue && present) || (kind == secretScalar && !isKeepMarker(v)) {
			out = append(out, path)
		}
	})
	return out
}

// Split 把完整配置拆成普通配置与密钥（键为密钥的具体路径）。
// 普通配置里：标量密钥被移除，kv 密钥值以 nil 占位以保留键名。入参不会被修改。
func Split(fields []Field, cfg map[string]any) (plain map[string]any, secrets map[string]any) {
	plain = deepCopy(cfg)
	secrets = map[string]any{}
	walkSecrets(fields, plain, "", func(path string, kind secretKind, c map[string]any, key string) {
		v, present := c[key]
		switch kind {
		case secretScalar:
			if present && !isKeepMarker(v) {
				secrets[path] = v
			}
			delete(c, key)
		case secretKVValue:
			if present && v != nil {
				secrets[path] = v
			}
			c[key] = nil
		}
	})
	return plain, secrets
}

// Merge 是 Split 的逆操作：把密钥按路径放回普通配置，返回新的完整配置。
func Merge(fields []Field, plain map[string]any, secrets map[string]any) map[string]any {
	out := deepCopy(plain)
	if out == nil {
		out = map[string]any{}
	}
	walkSecrets(fields, out, "", func(path string, _ secretKind, c map[string]any, key string) {
		if v, ok := secrets[path]; ok {
			c[key] = v
		}
	})
	return out
}

// Redact 返回可回显给前端的配置：已设置的密钥一律替换为 {"set": true}，永不含明文。
func Redact(fields []Field, cfg map[string]any) map[string]any {
	out := deepCopy(cfg)
	walkSecrets(fields, out, "", func(_ string, kind secretKind, c map[string]any, key string) {
		v, present := c[key]
		if !present {
			return
		}
		if (kind == secretScalar && !isKeepMarker(v)) || (kind == secretKVValue && v != nil) {
			c[key] = map[string]any{"set": true}
		}
	})
	return out
}

// KeepSecrets 实现密钥的「保留原值」语义：incoming 中的密钥若缺省、为空串、nil 或
// 回显的 {"set": true}，就取 existing 里同一路径的值（existing 里没有则视为未设置）。
// kv 密钥按键匹配，object_list 内的密钥按元素下标匹配。入参不会被修改。
func KeepSecrets(fields []Field, incoming, existing map[string]any) map[string]any {
	_, old := Split(fields, existing)
	out := deepCopy(incoming)
	if out == nil {
		out = map[string]any{}
	}
	walkSecrets(fields, out, "", func(path string, _ secretKind, c map[string]any, key string) {
		if !isKeepMarker(c[key]) {
			return
		}
		if v, ok := old[path]; ok {
			c[key] = v
		} else {
			delete(c, key)
		}
	})
	return out
}

func deepCopy(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopy(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopyValue(e)
		}
		return out
	}
	return v
}
