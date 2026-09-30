package schema

import (
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/i18n"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/internal/yamlnode"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// 各属性适用的字段类型；未列出的属性对所有类型适用。
var (
	patternTypes = typeSet(TypeString, TypeText, TypeSecret, TypeList)
	rangeTypes   = typeSet(TypeString, TypeText, TypeSecret, TypeNumber, TypeDuration, TypeList, TypeKV, TypeObjectList)
)

func typeSet(ts ...Type) map[Type]bool {
	m := make(map[Type]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return m
}

// DecodeFields 解析 config_schema 序列节点并做定义自检（key 唯一、type 合法、
// visible_when 引用有效、默认值与类型相符等）。basePath 是错误路径前缀（如 config_schema）。
// 发现问题时仍尽量返回已解析的字段，由调用方决定是否采用。
func DecodeFields(seq *yaml.Node, basePath string) ([]Field, []Issue) {
	items, ok := yamlnode.Items(seq)
	if !ok {
		return nil, []Issue{{Line: line(seq), Path: basePath, Message: "必须是字段列表"}}
	}
	d := &decoder{}
	fields := d.decodeList(items, basePath)
	return fields, d.issues
}

type decoder struct {
	issues []Issue
}

func (d *decoder) add(n *yaml.Node, path, format string, args ...any) {
	d.issues = append(d.issues, Issue{Line: line(n), Path: path, Message: fmt.Sprintf(format, args...)})
}

func line(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}

func (d *decoder) decodeList(items []*yaml.Node, basePath string) []Field {
	fields := make([]Field, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		f, ok := d.decodeField(item, path)
		if !ok {
			continue
		}
		if f.Key != "" && seen[f.Key] {
			d.add(item, path+".key", "key %q 重复", f.Key)
			continue
		}
		seen[f.Key] = true
		fields = append(fields, f)
	}
	d.checkVisibleWhen(fields, basePath)
	return fields
}

func (d *decoder) decodeField(n *yaml.Node, path string) (Field, bool) {
	pairs, ok := yamlnode.Pairs(n)
	if !ok {
		d.add(n, path, "字段定义必须是映射")
		return Field{}, false
	}
	f := Field{Line: n.Line}
	var optionsNode, fieldsNode, whenNode, defaultNode *yaml.Node
	present := map[string]bool{}
	for _, p := range pairs {
		if p.Duplicate {
			d.add(p.KeyNode, path+"."+p.Key, "属性 %s 重复", p.Key)
			continue
		}
		present[p.Key] = true
		ppath := path + "." + p.Key
		switch p.Key {
		case "key":
			f.Key = d.scalar(p.Value, ppath)
		case "type":
			f.Type = Type(d.scalar(p.Value, ppath))
		case "title":
			f.Title = d.text(p.Value, ppath)
		case "help":
			f.Help = d.text(p.Value, ppath)
		case "required":
			f.Required = d.boolean(p.Value, ppath)
		case "default":
			defaultNode = p.Value
		case "min":
			f.Min = d.number(p.Value, ppath)
		case "max":
			f.Max = d.number(p.Value, ppath)
		case "pattern":
			f.Pattern = d.scalar(p.Value, ppath)
		case "visible_when":
			whenNode = p.Value
		case "options":
			optionsNode = p.Value
		case "allow_query":
			f.AllowQuery = d.boolean(p.Value, ppath)
		case "allow_public_http":
			f.AllowPublicHTTP = d.boolean(p.Value, ppath)
		case "follow_redirects":
			f.FollowRedirects = d.boolean(p.Value, ppath)
		case "secret_values":
			f.SecretValues = d.boolean(p.Value, ppath)
		case "fields":
			fieldsNode = p.Value
		default:
			d.add(p.KeyNode, ppath, "未知属性 %s", p.Key)
		}
	}
	d.checkCore(n, path, &f, present)
	if whenNode != nil {
		f.VisibleWhen = d.decodeWhen(whenNode, path+".visible_when")
	}
	if optionsNode != nil {
		f.Options = d.decodeOptions(optionsNode, path+".options")
	}
	if fieldsNode != nil {
		if items, ok := yamlnode.Items(fieldsNode); ok {
			f.Fields = d.decodeList(items, path+".fields")
		} else {
			d.add(fieldsNode, path+".fields", "fields 必须是字段列表")
		}
	}
	d.checkTypeSpecific(n, path, &f, present)
	if defaultNode != nil {
		var v any
		if err := defaultNode.Decode(&v); err != nil {
			d.add(defaultNode, path+".default", "default 无法解析")
		} else {
			f.Default = normalize(v)
		}
	}
	d.checkDefault(n, path, &f, defaultNode)
	return f, true
}

func (d *decoder) checkCore(n *yaml.Node, path string, f *Field, present map[string]bool) {
	switch {
	case !present["key"] || f.Key == "":
		d.add(n, path+".key", "缺少 key")
	case !keyPattern.MatchString(f.Key):
		d.add(n, path+".key", "key %q 不合法，只能用字母、数字、下划线且不以数字开头", f.Key)
	}
	switch {
	case !present["type"] || f.Type == "":
		d.add(n, path+".type", "缺少 type")
	case !f.Type.Valid():
		d.add(n, path+".type", "type %q 不在支持的 14 种字段类型内", f.Type)
	}
}

func (d *decoder) checkTypeSpecific(n *yaml.Node, path string, f *Field, present map[string]bool) {
	if !f.Type.Valid() {
		return
	}
	if present["pattern"] {
		if !patternTypes[f.Type] {
			d.add(n, path+".pattern", "type %s 不支持 pattern", f.Type)
		} else if re, err := regexp.Compile(f.Pattern); err != nil {
			d.add(n, path+".pattern", "pattern 不是合法的正则: %v", err)
		} else {
			f.re = re
		}
	}
	if (present["min"] || present["max"]) && !rangeTypes[f.Type] {
		d.add(n, path+".min", "type %s 不支持 min/max", f.Type)
	}
	if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
		d.add(n, path+".min", "min 不能大于 max")
	}
	if f.Type == TypeEnum && len(f.Options) == 0 {
		d.add(n, path+".options", "enum 必须声明非空的 options")
	}
	if f.Type != TypeEnum && present["options"] {
		d.add(n, path+".options", "只有 enum 可以声明 options")
	}
	if f.Type == TypeObjectList && len(f.Fields) == 0 {
		d.add(n, path+".fields", "object_list 必须声明非空的 fields")
	}
	if f.Type != TypeObjectList && present["fields"] {
		d.add(n, path+".fields", "只有 object_list 可以声明 fields")
	}
	if present["secret_values"] && f.Type != TypeKV {
		d.add(n, path+".secret_values", "secret_values 只用于 kv")
	}
	for _, k := range []string{"allow_query", "allow_public_http", "follow_redirects"} {
		if present[k] && f.Type != TypeURL {
			d.add(n, path+"."+k, "%s 只用于 url", k)
		}
	}
}

func (d *decoder) checkDefault(n *yaml.Node, path string, f *Field, defaultNode *yaml.Node) {
	if defaultNode == nil || f.Default == nil || !f.Type.Valid() {
		return
	}
	errs := map[string]string{}
	if _, ok := checkValue(f, f.Default, "default", errs); !ok || len(errs) != 0 {
		d.add(defaultNode, path+".default", "default 与字段类型 %s 不符", f.Type)
	}
}

func (d *decoder) checkVisibleWhen(fields []Field, basePath string) {
	for i, f := range fields {
		for _, c := range f.VisibleWhen {
			found := false
			for _, prev := range fields[:i] {
				if prev.Key == c.Key {
					found = true
					break
				}
			}
			if !found {
				d.issues = append(d.issues, Issue{
					Line: f.Line, Path: fmt.Sprintf("%s.%s.visible_when", basePath, f.Key),
					Message: fmt.Sprintf("visible_when 引用了未知或排在后面的字段 %q", c.Key),
				})
			}
		}
	}
}

func (d *decoder) decodeWhen(n *yaml.Node, path string) []Condition {
	pairs, ok := yamlnode.Pairs(n)
	if !ok {
		d.add(n, path, "visible_when 必须是 {字段: 值或值列表} 映射")
		return nil
	}
	var out []Condition
	for _, p := range pairs {
		c := Condition{Key: p.Key}
		var v any
		if err := p.Value.Decode(&v); err != nil {
			d.add(p.Value, path+"."+p.Key, "条件值无法解析")
			continue
		}
		if list, ok := v.([]any); ok {
			for _, x := range list {
				c.Values = append(c.Values, normalize(x))
			}
		} else {
			c.Values = []any{normalize(v)}
		}
		if len(c.Values) == 0 {
			d.add(p.Value, path+"."+p.Key, "条件值不能为空列表")
			continue
		}
		out = append(out, c)
	}
	return out
}

func (d *decoder) decodeOptions(n *yaml.Node, path string) []Option {
	items, ok := yamlnode.Items(n)
	if !ok {
		d.add(n, path, "options 必须是列表")
		return nil
	}
	var out []Option
	seen := map[string]bool{}
	for i, it := range items {
		ipath := fmt.Sprintf("%s[%d]", path, i)
		var o Option
		if it.Kind == yaml.ScalarNode {
			o = Option{Value: it.Value, Title: i18n.Text{ZH: it.Value, EN: it.Value}}
		} else if pairs, ok := yamlnode.Pairs(it); ok {
			for _, p := range pairs {
				switch p.Key {
				case "value":
					o.Value = d.scalar(p.Value, ipath+".value")
				case "title":
					o.Title = d.text(p.Value, ipath+".title")
				default:
					d.add(p.KeyNode, ipath+"."+p.Key, "未知属性 %s", p.Key)
				}
			}
			if o.Title.IsZero() {
				o.Title = i18n.Text{ZH: o.Value, EN: o.Value}
			}
		} else {
			d.add(it, ipath, "选项必须是标量或 {value, title} 映射")
			continue
		}
		if o.Value == "" {
			d.add(it, ipath, "选项缺少 value")
			continue
		}
		if seen[o.Value] {
			d.add(it, ipath, "选项 %q 重复", o.Value)
			continue
		}
		seen[o.Value] = true
		out = append(out, o)
	}
	return out
}

func (d *decoder) scalar(n *yaml.Node, path string) string {
	if n.Kind != yaml.ScalarNode {
		d.add(n, path, "必须是字符串")
		return ""
	}
	return n.Value
}

func (d *decoder) boolean(n *yaml.Node, path string) bool {
	var b bool
	if n.Kind != yaml.ScalarNode || n.Decode(&b) != nil {
		d.add(n, path, "必须是布尔值")
		return false
	}
	return b
}

func (d *decoder) number(n *yaml.Node, path string) *float64 {
	var v float64
	if n.Kind != yaml.ScalarNode || n.Decode(&v) != nil {
		d.add(n, path, "必须是数字")
		return nil
	}
	return &v
}

func (d *decoder) text(n *yaml.Node, path string) i18n.Text {
	t, msg := i18n.Decode(n)
	if msg != "" {
		d.add(n, path, "%s", msg)
	}
	return t
}

// normalize 把 YAML/JSON 解出的数字统一为 float64，递归处理列表与映射。
func normalize(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	}
	if f, ok := toFloat(v); ok {
		return f
	}
	return v
}
