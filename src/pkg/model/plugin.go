package model

// PluginInfo 是 GET /api/plugins 里一个插件的描述。面向用户的文本已按请求语言解析为单一字符串。
type PluginInfo struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
	// Kind 是 source | notifier。
	Kind string `json:"kind"`
	// Runtime 是 builtin | exec。
	Runtime string `json:"runtime"`
	// Origin 是插件来源：builtin（编译期注册）| exec（插件目录）。
	Origin string `json:"origin"`
	// RunsOn 是允许的运行位置：hub、agent。
	RunsOn []string `json:"runs_on"`
	// IntervalSeconds 是插件默认刷新间隔（未写 interval 时为默认值 300）。
	IntervalSeconds int `json:"interval_seconds"`
	// MinIntervalSeconds 是实例刷新间隔的下限，0 表示插件未声明（只受全局 5 秒下限约束）。
	MinIntervalSeconds int            `json:"min_interval_seconds"`
	TimeoutSeconds     int            `json:"timeout_seconds"`
	ConfigSchema       []PluginField  `json:"config_schema"`
	Outputs            []PluginOutput `json:"outputs"`
	Widgets            []PluginWidget `json:"widgets"`
}

// PluginField 是 config_schema 的一个字段，前端据此生成配置表单。
type PluginField struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Help  string `json:"help,omitempty"`
	// Required 表示必填。
	Required bool     `json:"required"`
	Default  any      `json:"default,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
	// VisibleWhen 中的条件全部成立才显示该字段。
	VisibleWhen []PluginCondition `json:"visible_when,omitempty"`
	// Options 仅 enum 字段有。
	Options         []PluginOption `json:"options,omitempty"`
	AllowQuery      bool           `json:"allow_query,omitempty"`
	AllowPublicHTTP bool           `json:"allow_public_http,omitempty"`
	FollowRedirects bool           `json:"follow_redirects,omitempty"`
	// SecretValues 仅 kv 字段：值是密钥。
	SecretValues bool `json:"secret_values,omitempty"`
	// Fields 仅 object_list 字段：每个元素的子字段。
	Fields []PluginField `json:"fields,omitempty"`
}

// PluginCondition 是 visible_when 的一项：字段 Key 的值等于 Values 中任意一个。
type PluginCondition struct {
	Key    string `json:"key"`
	Values []any  `json:"values"`
}

// PluginOption 是 enum 字段的一个选项。
type PluginOption struct {
	Value string `json:"value"`
	Title string `json:"title"`
}

// PluginOutput 是插件产出的一个数据项声明；Key 以 [*] 结尾表示动态集合。
type PluginOutput struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// PluginWidget 是插件声明的小组件。
type PluginWidget struct {
	ID    string             `json:"id"`
	Name  string             `json:"name"`
	Sizes []PluginWidgetSize `json:"sizes"`
}

// PluginWidgetSize 是小组件的一种尺寸及其模板与数据绑定。
type PluginWidgetSize struct {
	// Size 是原文，如 2x1。
	Size     string          `json:"size"`
	Cols     int             `json:"cols"`
	Rows     int             `json:"rows"`
	Template string          `json:"template"`
	Bind     []PluginBinding `json:"bind"`
}

// PluginBinding 是小组件的一个绑定槽。
type PluginBinding struct {
	Slot string `json:"slot"`
	// List 为 true 表示该槽按列表写法声明，可绑定多个数据项。
	List bool            `json:"list"`
	Refs []PluginItemRef `json:"refs"`
}

// PluginItemRef 是对数据项字段的引用；Field 为空表示取该类型的默认字段。
type PluginItemRef struct {
	Item  string `json:"item"`
	Field string `json:"field,omitempty"`
}

// PluginProblem 是 manifest 里的一个具体问题。
type PluginProblem struct {
	// Line 是 plugin.yaml 行号，0 表示未知。
	Line    int    `json:"line"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// PluginLoadIssue 是某个插件目录未能加载的原因（或与内置插件冲突）。
type PluginLoadIssue struct {
	// Dir 是插件目录名。
	Dir string `json:"dir"`
	ID  string `json:"id"`
	// Kind 是 invalid_manifest | id_mismatch | runtime_not_exec | missing_run |
	// not_executable | insecure | conflict。
	Kind     string          `json:"kind"`
	Message  string          `json:"message"`
	Problems []PluginProblem `json:"problems,omitempty"`
}

// PluginList 是 GET /api/plugins 与 POST /api/plugins/rescan 的响应。
type PluginList struct {
	Plugins []PluginInfo `json:"plugins"`
	// Errors 是加载失败的目录；Conflicts 是与内置插件 id 冲突的目录（供系统页提示）。
	Errors    []PluginLoadIssue `json:"errors"`
	Conflicts []PluginLoadIssue `json:"conflicts"`
}

// PluginLookupRequest 是 POST /api/plugins/{id}/lookup/{key} 的请求体。
type PluginLookupRequest struct {
	Query string `json:"query"`
}

// PluginCandidate 是 lookup 字段的一个候选项。
type PluginCandidate struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// PluginLookupResponse 是 lookup 的响应。
type PluginLookupResponse struct {
	Candidates []PluginCandidate `json:"candidates"`
}
