package schema

import (
	"fmt"
	"net/url"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// Validate 校验实例配置，全部合法返回 nil；否则返回按配置路径给出的 model.FieldErrors。
func Validate(fields []Field, cfg map[string]any) model.FieldErrors {
	_, errs := Prepare(fields, cfg)
	return errs
}

// Prepare 校验并整理实例配置：补上默认值、丢弃 visible_when 不成立的字段，
// 返回整理后的配置（数字统一为 float64）。校验失败时第二个返回值非 nil，此时配置仅供参考。
// 配置里出现 schema 未声明的键视为 invalid。
func Prepare(fields []Field, cfg map[string]any) (map[string]any, model.FieldErrors) {
	errs := model.FieldErrors{}
	clean := prepareLevel(fields, cfg, "", errs)
	if len(errs) == 0 {
		return clean, nil
	}
	return clean, errs
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func prepareLevel(fields []Field, in map[string]any, prefix string, errs model.FieldErrors) map[string]any {
	out := make(map[string]any, len(fields))
	known := make(map[string]bool, len(fields))
	for i := range fields {
		f := &fields[i]
		known[f.Key] = true
		if !visible(f, out) {
			continue
		}
		path := joinPath(prefix, f.Key)
		v := in[f.Key]
		if isEmpty(v) {
			v = f.Default
		}
		if isEmpty(v) {
			if f.Required {
				errs[path] = model.FieldRequired
			}
			continue
		}
		if c, ok := checkValue(f, v, path, errs); ok {
			out[f.Key] = c
		}
	}
	for k := range in {
		if !known[k] {
			errs[joinPath(prefix, k)] = model.FieldInvalid
		}
	}
	return out
}

// visible 按已整理好的同级有效值求 visible_when。
func visible(f *Field, effective map[string]any) bool {
	for _, c := range f.VisibleWhen {
		cur, ok := effective[c.Key]
		if !ok {
			return false
		}
		match := false
		for _, want := range c.Values {
			if looseEqual(cur, want) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	return true
}

func looseEqual(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		fb, ok := toFloat(b)
		return ok && fa == fb
	}
	return a == b
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case map[string]string:
		return len(x) == 0
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case interface{ Float64() (float64, error) }: // json.Number
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func rangeErr(f *Field, n float64) bool {
	return (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max)
}

func (f *Field) pattern() *regexp.Regexp {
	if f.re == nil && f.Pattern != "" {
		f.re, _ = regexp.Compile(f.Pattern)
	}
	return f.re
}

// checkValue 校验一个非空值，返回规范化后的值；出错时写入 errs 并返回 ok=false。
func checkValue(f *Field, v any, path string, errs model.FieldErrors) (any, bool) {
	fail := func(code string) (any, bool) {
		errs[path] = code
		return nil, false
	}
	switch f.Type {
	case TypeString, TypeText, TypeSecret:
		s, ok := v.(string)
		if !ok {
			return fail(model.FieldInvalid)
		}
		if rangeErr(f, float64(utf8.RuneCountInString(s))) {
			return fail(model.FieldOutOfRange)
		}
		if re := f.pattern(); re != nil && !re.MatchString(s) {
			return fail(model.FieldPattern)
		}
		return s, true
	case TypeNumber:
		n, ok := toFloat(v)
		if !ok {
			return fail(model.FieldInvalid)
		}
		if rangeErr(f, n) {
			return fail(model.FieldOutOfRange)
		}
		return n, true
	case TypeBoolean:
		b, ok := v.(bool)
		if !ok {
			return fail(model.FieldInvalid)
		}
		return b, true
	case TypeEnum:
		s, ok := v.(string)
		if !ok {
			return fail(model.FieldInvalid)
		}
		for _, o := range f.Options {
			if o.Value == s {
				return s, true
			}
		}
		return fail(model.FieldInvalid)
	case TypeSecretURL:
		s, ok := v.(string)
		if !ok {
			return fail(model.FieldInvalid)
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fail(model.FieldInvalid)
		}
		return s, true
	case TypeURL:
		s, ok := v.(string)
		if !ok || CheckURL(s, f.AllowQuery, f.AllowPublicHTTP) != nil {
			return fail(model.FieldInvalid)
		}
		return s, true
	case TypeProxy:
		s, ok := v.(string)
		if !ok {
			return fail(model.FieldInvalid)
		}
		return s, true
	case TypeDuration:
		s, ok := v.(string)
		if !ok {
			return fail(model.FieldInvalid)
		}
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return fail(model.FieldInvalid)
		}
		if rangeErr(f, d.Seconds()) {
			return fail(model.FieldOutOfRange)
		}
		return s, true
	case TypeList:
		return checkList(f, v, path, errs)
	case TypeKV:
		return checkKV(f, v, path, errs)
	case TypeObjectList:
		return checkObjectList(f, v, path, errs)
	case TypeLookup:
		switch x := v.(type) {
		case string:
			return x, true
		case map[string]any:
			return x, true
		}
		return fail(model.FieldInvalid)
	}
	return fail(model.FieldInvalid)
}

func checkList(f *Field, v any, path string, errs model.FieldErrors) (any, bool) {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case []string:
		for _, s := range x {
			items = append(items, s)
		}
	default:
		errs[path] = model.FieldInvalid
		return nil, false
	}
	if rangeErr(f, float64(len(items))) {
		errs[path] = model.FieldOutOfRange
		return nil, false
	}
	re := f.pattern()
	out := make([]any, 0, len(items))
	ok := true
	for i, it := range items {
		ipath := fmt.Sprintf("%s[%d]", path, i)
		s, isStr := it.(string)
		switch {
		case !isStr:
			errs[ipath] = model.FieldInvalid
			ok = false
		case re != nil && !re.MatchString(s):
			errs[ipath] = model.FieldPattern
			ok = false
		default:
			out = append(out, s)
		}
	}
	return out, ok
}

func checkKV(f *Field, v any, path string, errs model.FieldErrors) (any, bool) {
	var m map[string]any
	switch x := v.(type) {
	case map[string]any:
		m = x
	case map[string]string:
		m = make(map[string]any, len(x))
		for k, s := range x {
			m[k] = s
		}
	default:
		errs[path] = model.FieldInvalid
		return nil, false
	}
	if rangeErr(f, float64(len(m))) {
		errs[path] = model.FieldOutOfRange
		return nil, false
	}
	out := make(map[string]any, len(m))
	ok := true
	for k, val := range m {
		if k == "" {
			errs[path] = model.FieldInvalid
			ok = false
			continue
		}
		s, isStr := val.(string)
		if !isStr {
			errs[path+"."+k] = model.FieldInvalid
			ok = false
			continue
		}
		out[k] = s
	}
	return out, ok
}

func checkObjectList(f *Field, v any, path string, errs model.FieldErrors) (any, bool) {
	items, isList := v.([]any)
	if !isList {
		errs[path] = model.FieldInvalid
		return nil, false
	}
	if rangeErr(f, float64(len(items))) {
		errs[path] = model.FieldOutOfRange
		return nil, false
	}
	out := make([]any, 0, len(items))
	before := len(errs)
	for i, it := range items {
		ipath := fmt.Sprintf("%s[%d]", path, i)
		m, isMap := it.(map[string]any)
		if !isMap {
			errs[ipath] = model.FieldInvalid
			continue
		}
		out = append(out, prepareLevel(f.Fields, m, ipath, errs))
	}
	return out, len(errs) == before
}
