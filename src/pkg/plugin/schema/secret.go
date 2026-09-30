package schema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
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

// secretLeaf 描述 walkSecrets 访问到的一个密钥叶子。
type secretLeaf struct {
	path      string
	kind      secretKind
	field     *Field
	container map[string]any
	key       string
	// 在 object_list 元素内时：listPath 是最内层列表的路径，idx 是元素下标；否则 idx 为 -1。
	listPath string
	idx      int
}

// refPath 返回同一叶子在「原下标 ref 的元素」中的路径。
func (l secretLeaf) refPath(ref int) string {
	cur := fmt.Sprintf("%s[%d]", l.listPath, l.idx)
	return fmt.Sprintf("%s[%d]", l.listPath, ref) + strings.TrimPrefix(l.path, cur)
}

// walkSecrets 遍历 cfg 中的密钥叶子：标量密钥无论是否存在都会被访问，
// kv 密钥按现有键访问，object_list 按现有元素下标递归。
func walkSecrets(fields []Field, cfg map[string]any, prefix string, visit func(secretLeaf)) {
	walkLevel(fields, cfg, prefix, "", -1, visit)
}

func walkLevel(fields []Field, cfg map[string]any, prefix, listPath string, idx int, visit func(secretLeaf)) {
	for i := range fields {
		f := &fields[i]
		path := joinPath(prefix, f.Key)
		switch {
		case f.Type == TypeSecret || f.Type == TypeSecretURL:
			visit(secretLeaf{path, secretScalar, f, cfg, f.Key, listPath, idx})
		case f.Type == TypeKV && f.SecretValues:
			m, _ := cfg[f.Key].(map[string]any)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				visit(secretLeaf{path + "." + k, secretKVValue, f, m, k, listPath, idx})
			}
		case f.Type == TypeObjectList:
			items, _ := cfg[f.Key].([]any)
			for n, it := range items {
				if m, ok := it.(map[string]any); ok {
					walkLevel(f.Fields, m, fmt.Sprintf("%s[%d]", path, n), path, n, visit)
				}
			}
		}
	}
}

// isKeepMarker 判断密钥值是否表示「保留原值」：缺省、nil、空串，
// 或回显的 {"set": true}（object_list 内还可带 "ref"）。
func isKeepMarker(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case map[string]any:
		s, ok := x["set"].(bool)
		if !ok || !s {
			return false
		}
		_, hasRef := x["ref"]
		return len(x) == 1 || (len(x) == 2 && hasRef)
	}
	return false
}

// keepRef 取回显标记里的原下标。
func keepRef(v any) (int, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	f, ok := toFloat(m["ref"])
	if !ok || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

// SecretPaths 返回 cfg 中已有值的密钥的具体路径（api_key、headers.X-Token、accounts[1].token）。
func SecretPaths(fields []Field, cfg map[string]any) []string {
	var out []string
	walkSecrets(fields, cfg, "", func(l secretLeaf) {
		v, present := l.container[l.key]
		if (l.kind == secretKVValue && present) || (l.kind == secretScalar && !isKeepMarker(v)) {
			out = append(out, l.path)
		}
	})
	return out
}

// Split 把完整配置拆成普通配置与密钥（键为密钥的具体路径）。
// 普通配置里：标量密钥被移除，kv 密钥值以 nil 占位以保留键名。入参不会被修改。
func Split(fields []Field, cfg map[string]any) (plain map[string]any, secrets map[string]any) {
	plain = deepCopy(cfg)
	secrets = map[string]any{}
	walkSecrets(fields, plain, "", func(l secretLeaf) {
		v, present := l.container[l.key]
		switch l.kind {
		case secretScalar:
			if present && !isKeepMarker(v) {
				secrets[l.path] = v
			}
			delete(l.container, l.key)
		case secretKVValue:
			if present && v != nil {
				secrets[l.path] = v
			}
			l.container[l.key] = nil
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
	walkSecrets(fields, out, "", func(l secretLeaf) {
		if v, ok := secrets[l.path]; ok {
			l.container[l.key] = v
		}
	})
	return out
}

// Redact 返回可回显给前端的配置，永不含明文：已设置的密钥一律替换为 {"set": true}。
// 契约：object_list 元素内的密钥回显为 {"set": true, "ref": <该元素在本配置中的下标>}，
// 前端增删或重排元素后原样带回 ref，KeepSecrets 据此找回原值；顶层与 kv 的密钥只回显 {"set": true}。
func Redact(fields []Field, cfg map[string]any) map[string]any {
	out := deepCopy(cfg)
	walkSecrets(fields, out, "", func(l secretLeaf) {
		v, present := l.container[l.key]
		if !present {
			return
		}
		if (l.kind != secretScalar || isKeepMarker(v)) && (l.kind != secretKVValue || v == nil) {
			return
		}
		marker := map[string]any{"set": true}
		if l.idx >= 0 && l.kind == secretScalar {
			marker["ref"] = l.idx
		}
		l.container[l.key] = marker
	})
	return out
}

// KeepSecrets 实现密钥的「保留原值」语义：incoming 中的密钥若缺省、为空串、nil 或回显的
// {"set": true}，就取 existing 里的原值（existing 里没有则视为未设置）。匹配方式：
//   - 顶层密钥按路径；kv 密钥按键。
//   - object_list 元素内的密钥按回显标记里的 ref（原下标）匹配，与元素当前位置无关，
//     所以增删、重排元素不会错配。留空且没有 ref 时视为未设置（必填密钥报 required）；
//     ref 越界、或 ref 指向的旧元素没有该密钥时，报该路径 required，绝不静默丢弃或错配。
//
// 返回合并后的配置与按路径给出的 model.FieldErrors（无问题为 nil）。入参不会被修改。
func KeepSecrets(fields []Field, incoming, existing map[string]any) (map[string]any, model.FieldErrors) {
	_, old := Split(fields, existing)
	out := deepCopy(incoming)
	if out == nil {
		out = map[string]any{}
	}
	errs := model.FieldErrors{}
	walkSecrets(fields, out, "", func(l secretLeaf) {
		cur := l.container[l.key]
		if !isKeepMarker(cur) {
			return
		}
		if l.idx >= 0 && l.kind == secretScalar {
			ref, hasRef := keepRef(cur)
			if !hasRef {
				delete(l.container, l.key)
				if l.field.Required {
					errs[l.path] = model.FieldRequired
				}
				return
			}
			v, ok := old[l.refPath(ref)]
			if !ok {
				delete(l.container, l.key)
				errs[l.path] = model.FieldRequired
				return
			}
			l.container[l.key] = v
			return
		}
		if v, ok := old[l.path]; ok {
			l.container[l.key] = v
		} else {
			delete(l.container, l.key)
		}
	})
	if len(errs) == 0 {
		return out, nil
	}
	return out, errs
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
