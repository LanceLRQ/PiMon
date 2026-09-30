package plugindev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/invopop/jsonschema"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// Schema 产物文件名，位于仓库根 docs/plugin-schema/。
const (
	ManifestSchemaFile = "plugin.schema.json"
	ReportSchemaFile   = "report.schema.json"
)

// 以下 *Doc 结构只用于生成 plugin.yaml 的 JSON Schema：manifest 包为保留行号用 yaml.Node 解析，
// 没有可直接反射的 Go 结构。字段集合与 manifest 解析器实际接受的集合由 schemagen_test.go 双向比对，
// 取值范围（枚举、尺寸上限）直接取自 manifest、report、schema 包的常量。

type manifestDoc struct {
	ID           string      `json:"id" jsonschema:"description=插件 id，与 exec 插件的目录名一致"`
	Version      string      `json:"version" jsonschema:"description=插件版本 x.y.z"`
	APIVersion   int         `json:"api_version"`
	Name         i18nDoc     `json:"name"`
	Kind         string      `json:"kind"`
	Runtime      string      `json:"runtime" jsonschema:"description=目录里的 exec 插件必须写 exec"`
	RunsOn       []string    `json:"runs_on"`
	Interval     string      `json:"interval,omitempty" jsonschema:"description=默认刷新间隔，如 30s、5m；缺省 5m"`
	MinInterval  string      `json:"min_interval,omitempty" jsonschema:"description=实例刷新间隔下限，不得大于 interval"`
	Timeout      string      `json:"timeout,omitempty" jsonschema:"description=单次采集超时；缺省 30s"`
	ConfigSchema []fieldDoc  `json:"config_schema,omitempty"`
	Outputs      []outputDoc `json:"outputs,omitempty"`
	Widgets      []widgetDoc `json:"widgets,omitempty"`
	Alerts       []alertDoc  `json:"alerts,omitempty"`
}

type i18nDoc struct{}

// JSONSchema 描述多语言文本：单个字符串，或 {zh, en} 映射。
func (i18nDoc) JSONSchema() *jsonschema.Schema {
	obj := &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: jsonschema.FalseSchema,
		MinProperties:        ptr(uint64(1)),
		Properties:           jsonschema.NewProperties(),
	}
	obj.Properties.Set("zh", &jsonschema.Schema{Type: "string"})
	obj.Properties.Set("en", &jsonschema.Schema{Type: "string"})
	return &jsonschema.Schema{
		Description: "多语言文本：字符串，或 {zh, en} 映射",
		OneOf:       []*jsonschema.Schema{{Type: "string"}, obj},
	}
}

func ptr[T any](v T) *T { return &v }

func (manifestDoc) JSONSchemaExtend(s *jsonschema.Schema) {
	setEnum(s, "kind", []string{string(manifest.KindSource), string(manifest.KindNotifier)})
	setEnum(s, "runtime", []string{string(manifest.RuntimeBuiltin), string(manifest.RuntimeExec)})
	if p, ok := s.Properties.Get("api_version"); ok {
		p.Enum = []any{manifest.SupportedAPIVersion}
	}
	if p, ok := s.Properties.Get("runs_on"); ok {
		p.MinItems = ptr(uint64(1))
		p.UniqueItems = true
		if p.Items != nil {
			p.Items.Enum = []any{manifest.RunsOnHub, manifest.RunsOnAgent}
		}
	}
	if p, ok := s.Properties.Get("id"); ok {
		p.Pattern = `^[a-z0-9]+(-[a-z0-9]+)*$`
	}
	if p, ok := s.Properties.Get("version"); ok {
		p.Pattern = `^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`
	}
}

type fieldDoc struct {
	Key             string             `json:"key" jsonschema:"pattern=^[A-Za-z_][A-Za-z0-9_]*$"`
	Type            string             `json:"type"`
	Title           i18nDoc            `json:"title,omitempty"`
	Help            i18nDoc            `json:"help,omitempty"`
	Required        bool               `json:"required,omitempty"`
	Default         any                `json:"default,omitempty"`
	Min             *float64           `json:"min,omitempty" jsonschema:"description=字符串为字符数，number 为数值，duration 为秒，list/kv/object_list 为元素个数"`
	Max             *float64           `json:"max,omitempty"`
	Pattern         string             `json:"pattern,omitempty" jsonschema:"description=正则，仅 string/text/secret/list 可用"`
	VisibleWhen     map[string]condDoc `json:"visible_when,omitempty"`
	Options         []optionDoc        `json:"options,omitempty" jsonschema:"description=仅 enum 可用"`
	AllowQuery      bool               `json:"allow_query,omitempty" jsonschema:"description=仅 url 可用"`
	AllowPublicHTTP bool               `json:"allow_public_http,omitempty" jsonschema:"description=仅 url 可用"`
	FollowRedirects bool               `json:"follow_redirects,omitempty" jsonschema:"description=仅 url 可用"`
	SecretValues    bool               `json:"secret_values,omitempty" jsonschema:"description=仅 kv 可用：每个值都是密钥"`
	Fields          []fieldDoc         `json:"fields,omitempty" jsonschema:"description=仅 object_list 可用，不能嵌套 object_list"`
}

func (fieldDoc) JSONSchemaExtend(s *jsonschema.Schema) {
	types := make([]string, len(schema.Types))
	for i, t := range schema.Types {
		types[i] = string(t)
	}
	setEnum(s, "type", types)
}

// condDoc 是 visible_when 的条件值：标量，或标量列表。
type condDoc struct{}

func (condDoc) JSONSchema() *jsonschema.Schema {
	scalar := &jsonschema.Schema{OneOf: []*jsonschema.Schema{{Type: "string"}, {Type: "number"}, {Type: "boolean"}}}
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		scalar,
		{Type: "array", MinItems: ptr(uint64(1)), Items: scalar},
	}}
}

// optionDoc 是 enum 选项：字符串，或 {value, title}。
type optionDoc struct{}

func (optionDoc) JSONSchema() *jsonschema.Schema {
	obj := &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: jsonschema.FalseSchema,
		Properties:           jsonschema.NewProperties(),
		Required:             []string{"value"},
	}
	obj.Properties.Set("value", &jsonschema.Schema{Type: "string", MinLength: ptr(uint64(1))})
	obj.Properties.Set("title", &jsonschema.Schema{Ref: "#/$defs/i18nDoc"})
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{{Type: "string", MinLength: ptr(uint64(1))}, obj}}
}

type outputDoc struct {
	Key   string  `json:"key" jsonschema:"description=数据项键名；以 [*] 结尾表示动态集合，如 disk[*]"`
	Type  string  `json:"type"`
	Title i18nDoc `json:"title,omitempty"`
}

func (outputDoc) JSONSchemaExtend(s *jsonschema.Schema) { setEnum(s, "type", report.ItemTypes) }

type widgetDoc struct {
	ID    string             `json:"id"`
	Name  i18nDoc            `json:"name,omitempty"`
	Sizes map[string]sizeDoc `json:"sizes" jsonschema:"description=键为 NxM 尺寸"`
}

func (widgetDoc) JSONSchemaExtend(s *jsonschema.Schema) {
	p, ok := s.Properties.Get("sizes")
	if !ok {
		return
	}
	pat := fmt.Sprintf("^[1-%d]x[1-%d]$", manifest.MaxCols, manifest.MaxRows)
	p.PatternProperties = map[string]*jsonschema.Schema{pat: p.AdditionalProperties}
	p.AdditionalProperties = jsonschema.FalseSchema
	p.MinProperties = ptr(uint64(1))
}

type sizeDoc struct {
	Template string            `json:"template"`
	Bind     map[string]refDoc `json:"bind,omitempty" jsonschema:"description=槽名到数据项引用；值可以是单个引用，也可以是引用列表"`
}

func (sizeDoc) JSONSchemaExtend(s *jsonschema.Schema) {
	setEnum(s, "template", manifest.Templates)
	p, ok := s.Properties.Get("bind")
	if !ok || p.AdditionalProperties == nil {
		return
	}
	single := p.AdditionalProperties
	p.AdditionalProperties = &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		single,
		{Type: "array", Items: single},
	}}
}

type refDoc struct {
	Item  string `json:"item" jsonschema:"description=outputs 中声明的数据项，动态集合可写 disk[*] 或 disk[/vol1]"`
	Field string `json:"field,omitempty" jsonschema:"description=缺省取该数据项类型的默认字段"`
}

type alertDoc struct {
	Name     i18nDoc `json:"name"`
	Item     string  `json:"item"`
	Field    string  `json:"field,omitempty"`
	Op       string  `json:"op" jsonschema:"enum=<,enum=<=,enum=>,enum=>=,enum===,enum=!="`
	Value    any     `json:"value,omitempty"`
	Severity string  `json:"severity,omitempty" jsonschema:"enum=info,enum=warning,enum=critical"`
	For      string  `json:"for,omitempty" jsonschema:"description=持续时长，如 5m"`
}

func setEnum(s *jsonschema.Schema, prop string, values []string) {
	p, ok := s.Properties.Get(prop)
	if !ok {
		return
	}
	p.Enum = make([]any, len(values))
	for i, v := range values {
		p.Enum[i] = v
	}
}

// ManifestSchema 生成 plugin.yaml 的 JSON Schema。
func ManifestSchema() ([]byte, error) {
	r := &jsonschema.Reflector{Anonymous: true, ExpandedStruct: true}
	s := r.Reflect(&manifestDoc{})
	s.Title = "PiMon plugin.yaml"
	s.Description = "PiMon 插件描述文件（plugin.yaml）。YAML 文档可直接用本 Schema 校验（先转成 JSON）。"
	return marshal(s)
}

// ReportSchema 生成插件报告（exec 插件 stdout 的 JSON）的 JSON Schema。
// 直接反射 report.Report，字段集合与运行时解析使用同一份结构。
func ReportSchema() ([]byte, error) {
	r := &jsonschema.Reflector{
		Anonymous:                 true,
		ExpandedStruct:            true,
		AllowAdditionalProperties: true,
		Mapper:                    reportMapper,
	}
	s := r.Reflect(&report.Report{})
	s.Title = "PiMon 插件报告"
	s.Description = "exec 插件写到 stdout 的 JSON 报告；顶层未知字段会被忽略。"
	// stale 由运行时维护，插件自报的值会被清除，不出现在面向插件作者的 Schema 里。
	s.Properties.Delete("stale")
	item, ok := s.Definitions["Item"]
	if !ok {
		return nil, fmt.Errorf("报告 Schema 缺少 Item 定义")
	}
	item.Properties.Delete("stale")
	if p, ok := item.Properties.Get("type"); ok {
		p.Enum = make([]any, len(report.ItemTypes))
		for i, t := range report.ItemTypes {
			p.Enum[i] = t
		}
	}
	return marshal(s)
}

func reportMapper(t reflect.Type) *jsonschema.Schema {
	switch t {
	case reflect.TypeOf(report.Status("")):
		return &jsonschema.Schema{Type: "string", Enum: []any{
			string(report.StatusOK), string(report.StatusWarning), string(report.StatusCritical), string(report.StatusUnknown),
		}}
	case reflect.TypeOf(report.Event{}):
		props := jsonschema.NewProperties()
		props.Set("id", &jsonschema.Schema{Type: "string", MinLength: ptr(uint64(1))})
		props.Set("type", &jsonschema.Schema{Type: "string", MinLength: ptr(uint64(1))})
		props.Set("at", &jsonschema.Schema{Type: "integer", Description: "Unix 毫秒，必填且不为 0"})
		return &jsonschema.Schema{Type: "object", Properties: props, Required: []string{"id", "type", "at"}}
	}
	return nil
}

func marshal(s *jsonschema.Schema) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	// Schema 自带的 MarshalJSON 会转义 < > &，还原后产物才便于人读。
	out := buf.Bytes()
	for esc, ch := range map[string]string{`\u003c`: "<", `\u003e`: ">", `\u0026`: "&"} {
		out = bytes.ReplaceAll(out, []byte(esc), []byte(ch))
	}
	return out, nil
}
