package manifest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/i18n"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// I18nText 是 manifest 中的多语言文本。
type I18nText = i18n.Text

// Kind 是插件用途。
type Kind string

// 插件用途。
const (
	KindSource   Kind = "source"
	KindNotifier Kind = "notifier"
)

// Runtime 是插件形态。
type Runtime string

// 插件形态（HTTP 形态由内置 http-json 承载，不需要描述文件）。
const (
	RuntimeBuiltin Runtime = "builtin"
	RuntimeExec    Runtime = "exec"
)

// 运行位置。
const (
	RunsOnHub   = "hub"
	RunsOnAgent = "agent"
)

// 默认值与约束。
const (
	SupportedAPIVersion = 1
	DefaultInterval     = 300 * time.Second
	DefaultTimeout      = 30 * time.Second
	// MaxCols、MaxRows 是小组件尺寸上限（最小网格 800×480 为 6×4）。
	MaxCols = 6
	MaxRows = 4
)

// Templates 是小组件模板名允许集合：M1d 实现的 10 个，加上设计示例里的 quota、quota-multi（M3 实现）。
var Templates = []string{
	"value", "gauge", "state", "status-grid", "list", "table", "chart", "clock", "weather", "text",
	"quota", "quota-multi",
}

// OutputTypes 是数据项类型（设计 2.3）。
var OutputTypes = []string{"gauge", "number", "quota", "money", "state", "text", "table"}

// Manifest 是解析后的 plugin.yaml。
type Manifest struct {
	ID           string
	Version      string
	APIVersion   int
	Name         I18nText
	Kind         Kind
	Runtime      Runtime
	RunsOn       []string
	Interval     time.Duration
	Timeout      time.Duration
	ConfigSchema []schema.Field
	Outputs      []Output
	Widgets      []Widget
	Alerts       []Alert
}

// Output 声明插件产出的一个数据项；Key 以 [*] 结尾表示动态集合。
type Output struct {
	Key   string
	Type  string
	Title I18nText
	Line  int
}

// ItemRef 是结构化的字段引用；Field 为空表示取该类型的默认字段。
type ItemRef struct {
	Item  string
	Field string
}

// Binding 是小组件的一个绑定槽。List 为 true 时 Refs 来自列表写法。
type Binding struct {
	Slot string
	Refs []ItemRef
	List bool
}

// WidgetSize 是小组件的一种尺寸及其模板与绑定。
type WidgetSize struct {
	Size     string // 原文，如 2x1
	Cols     int
	Rows     int
	Template string
	Bind     []Binding
	Line     int
}

// Widget 是插件声明的小组件；Sizes 保持声明顺序。
type Widget struct {
	ID    string
	Name  I18nText
	Sizes []WidgetSize
	Line  int
}

// Alert 是默认告警规则，本期只解析并存储。
type Alert struct {
	Name     I18nText
	Item     string
	Field    string
	Op       string
	Value    any
	Severity string
	For      time.Duration
	Line     int
}

// Problem 是一条 manifest 问题，Line 为 yaml 行号（0 表示未知）。
type Problem = schema.Issue

// Error 汇总 manifest 的全部问题，按行号排序。
type Error struct {
	Problems []Problem
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		loc := ""
		if p.Line > 0 {
			loc = fmt.Sprintf("第 %d 行: ", p.Line)
		}
		path := ""
		if p.Path != "" {
			path = p.Path + ": "
		}
		parts[i] = loc + path + p.Message
	}
	return "plugin.yaml 不合法: " + strings.Join(parts, "; ")
}

func (e *Error) sort() {
	sort.SliceStable(e.Problems, func(i, j int) bool { return e.Problems[i].Line < e.Problems[j].Line })
}
