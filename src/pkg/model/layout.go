package model

import "time"

// 小组件来源。
const (
	// WidgetSourcePlugin 是插件在 manifest 里声明的小组件，由 plugin_id + widget_id 指定。
	WidgetSourcePlugin = "plugin"
	// WidgetSourceGeneric 是通用小组件：把任一实例的某个数据项套上通用模板。
	WidgetSourceGeneric = "generic"
	// WidgetSourceAggregate 是聚合小组件：把多个实例的数据项汇总到一个模板（如 status-grid）。
	WidgetSourceAggregate = "aggregate"
)

// IndexScreenID 是首页 screen 的固定 id，布局里必须存在且不可删除。
const IndexScreenID = "index"

// 布局版本来源。
const (
	LayoutSourceSeed     = "seed"
	LayoutSourceEdit     = "edit"
	LayoutSourceRollback = "rollback"
	// LayoutSourceAuto 表示系统自动生成（如按显示器自动选择网格）。
	LayoutSourceAuto = "auto"
)

// 布局问题码。problems 里的问题使保存被拒绝；broken 里的问题只做提示。
const (
	// LayoutProblemOutOfBounds 表示小组件超出网格（含位置为负、尺寸大于网格）。
	LayoutProblemOutOfBounds = "out_of_bounds"
	// LayoutProblemOverlap 表示两个小组件重叠；Widget 是后出现的那个，With 是先出现的那个。
	LayoutProblemOverlap = "overlap"
	// LayoutProblemSizeNotAllowed 表示尺寸不在插件声明（或通用、聚合目录允许）的尺寸之内。
	LayoutProblemSizeNotAllowed = "size_not_allowed"
	// LayoutProblemInvalidSize 表示尺寸的列数或行数小于 1。
	LayoutProblemInvalidSize = "invalid_size"
	// LayoutProblemInvalid 是其余结构性错误（缺少 index、id 为空或重复、来源与字段不匹配等）；
	// Field 指出出问题的字段路径。
	LayoutProblemInvalid = "invalid"

	// LayoutBrokenInstanceMissing 表示绑定的实例不存在。
	LayoutBrokenInstanceMissing = "instance_missing"
	// LayoutBrokenInstancePlugin 表示绑定的实例不属于小组件声明的插件。
	LayoutBrokenInstancePlugin = "instance_plugin_mismatch"
	// LayoutBrokenPluginMissing 表示小组件声明的插件已不可用。
	LayoutBrokenPluginMissing = "plugin_missing"
	// LayoutBrokenWidgetMissing 表示插件不再声明该小组件。
	LayoutBrokenWidgetMissing = "widget_missing"
	// LayoutBrokenSizeUnsupported 表示插件不再声明该小组件的这个尺寸（只在读取旧版本时出现）。
	LayoutBrokenSizeUnsupported = "size_unsupported"
)

// Grid 是 screen 的网格：列数与行数，所有 screen 共用。
type Grid struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// WidgetSize 是小组件占用的格数。
type WidgetSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// WidgetRef 指向某个实例的一个数据项（及字段）；Field 为空表示取该类型的默认字段。
type WidgetRef struct {
	InstanceID string `json:"instance_id"`
	Item       string `json:"item"`
	Field      string `json:"field,omitempty"`
}

// ResolvedRef 是解析后布局里的数据项引用：在 WidgetRef 之外带上按语言解析的数据项标题。
// Title 取 manifest outputs 的 title；动态键成员（如 target[home]）取方括号里的名字；解析不出为空。
type ResolvedRef struct {
	InstanceID string `json:"instance_id"`
	Item       string `json:"item"`
	Field      string `json:"field,omitempty"`
	Title      string `json:"title,omitempty"`
}

// WidgetBinding 是小组件的数据绑定。
//   - plugin 来源：InstanceID 指定实例，数据项由 manifest 声明的槽决定；为空表示占位，
//     解析布局时绑定到该插件最早创建的实例。
//   - generic 来源：Refs 至多一项；aggregate 来源：Refs 为参与汇总的数据项。
type WidgetBinding struct {
	InstanceID string      `json:"instance_id,omitempty"`
	Refs       []WidgetRef `json:"refs,omitempty"`
}

// LayoutWidget 是 screen 上的一个小组件放置记录。
type LayoutWidget struct {
	// ID 在整份布局内唯一。
	ID string `json:"id"`
	// Source 是 plugin | generic | aggregate。
	Source string `json:"source"`
	// PluginID、WidgetID 仅 plugin 来源有值。
	PluginID string `json:"plugin_id,omitempty"`
	WidgetID string `json:"widget_id,omitempty"`
	// Template 仅 generic、aggregate 来源有值（plugin 来源的模板由 manifest 的尺寸声明决定）。
	Template string     `json:"template,omitempty"`
	Size     WidgetSize `json:"size"`
	// Col、Row 是左上角所在格，从 0 开始。
	Col     int           `json:"col"`
	Row     int           `json:"row"`
	Binding WidgetBinding `json:"binding"`
	// Options 是显示选项：title（标题）、icon（lucide 图标名）、手动阈值、text 正文、clock 格式等。
	// 服务端只校验 title 与 icon 的类型，其余键原样保存。
	Options map[string]any `json:"options"`
}

// LayoutScreen 是一个 screen；在 Layout.Screens 中的顺序即轮播顺序。
type LayoutScreen struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// DwellSeconds 是轮播停留秒数，0 表示使用全局默认值。
	DwellSeconds int `json:"dwell_seconds"`
	// InRotation 表示是否参与轮播。
	InRotation bool           `json:"in_rotation"`
	Widgets    []LayoutWidget `json:"widgets"`
}

// Layout 是整份布局：网格加全部 screen。保存、回滚、版本都以整份为单位。
type Layout struct {
	Grid    Grid           `json:"grid"`
	Screens []LayoutScreen `json:"screens"`
}

// LayoutProblem 描述布局里的一个问题。
type LayoutProblem struct {
	Screen string `json:"screen,omitempty"`
	Widget string `json:"widget,omitempty"`
	Code   string `json:"code"`
	// With 仅 overlap：与之重叠的另一个小组件。
	With string `json:"with,omitempty"`
	// Field 仅 invalid：出问题的字段路径。
	Field string `json:"field,omitempty"`
}

// LayoutState 是某个版本的布局。
type LayoutState struct {
	Version   int       `json:"version"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	Layout    Layout    `json:"layout"`
	// Broken 是引用失效的小组件（实例已删除、插件消失等），不阻止保存。
	Broken []LayoutProblem `json:"broken"`
}

// LayoutSummary 是版本摘要。
type LayoutSummary struct {
	// ChangedScreens 是相对上一版有改动的 screen id（含新增与删除的）。
	ChangedScreens []string `json:"changed_screens"`
	WidgetsAdded   int      `json:"widgets_added"`
	WidgetsRemoved int      `json:"widgets_removed"`
	WidgetsChanged int      `json:"widgets_changed"`
	GridChanged    bool     `json:"grid_changed"`
	// RolledBackFrom 仅回滚生成的版本：内容取自哪个版本。
	RolledBackFrom int `json:"rolled_back_from,omitempty"`
	// Note 是调用方附加的说明（如「按显示器自动选择网格」）。
	Note string `json:"note,omitempty"`
}

// LayoutVersionInfo 是版本列表里的一项。
type LayoutVersionInfo struct {
	Version   int           `json:"version"`
	Source    string        `json:"source"`
	CreatedAt time.Time     `json:"created_at"`
	Summary   LayoutSummary `json:"summary"`
	// HasBroken 表示该版本的布局按当前实例与插件检查含有失效引用。
	HasBroken bool `json:"has_broken"`
}

// LayoutSaveRequest 是 PUT /api/screens 的请求体。
type LayoutSaveRequest struct {
	// BaseVersion 是编辑所基于的版本；与服务端当前版本不一致时返回 409 layout.conflict。
	// 尚无任何版本时为 0。
	BaseVersion int    `json:"base_version"`
	Layout      Layout `json:"layout"`
}

// LayoutRollbackRequest 是 POST /api/screens/rollback 的请求体。
type LayoutRollbackRequest struct {
	Version int `json:"version"`
}

// CatalogEntry 是通用或聚合目录里的一个模板及其允许的尺寸。
type CatalogEntry struct {
	Template string       `json:"template"`
	Sizes    []WidgetSize `json:"sizes"`
}

// WidgetCatalog 是通用与聚合小组件目录（插件小组件的尺寸来自各插件的 manifest）。
type WidgetCatalog struct {
	Generic   []CatalogEntry `json:"generic"`
	Aggregate []CatalogEntry `json:"aggregate"`
}

// ResolvedWidget 是解析后的小组件，供屏幕端渲染。
type ResolvedWidget struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	PluginID string `json:"plugin_id,omitempty"`
	WidgetID string `json:"widget_id,omitempty"`
	// Template 是渲染用的模板：plugin 来源取自 manifest 的尺寸声明，其余取自布局。
	Template string     `json:"template"`
	Size     WidgetSize `json:"size"`
	Col      int        `json:"col"`
	Row      int        `json:"row"`
	// InstanceID 仅 plugin 来源：实际绑定的实例（占位已解析为该插件最早创建的实例）。
	InstanceID string `json:"instance_id,omitempty"`
	// Placeholder 表示该小组件在布局里是占位（未指定实例），InstanceID 是解析出来的。
	Placeholder bool `json:"placeholder,omitempty"`
	// Title 是按语言解析的标题：Options.title，否则插件小组件名，否则（generic）数据项标题。
	Title string `json:"title"`
	// Slots 是各槽解析出的数据项引用。plugin 来源的键为 manifest 声明的槽名；
	// generic 来源的键固定为 value；aggregate 来源的键固定为 items。
	Slots map[string][]ResolvedRef `json:"slots"`
	// DisplayState 是统一展示状态：无绑定为 unconfigured，引用失效为 broken，
	// 否则为被绑定实例的展示状态（聚合取其中最严重的）。
	DisplayState string         `json:"display_state"`
	Options      map[string]any `json:"options"`
}

// ResolvedScreen 是解析后的 screen。
type ResolvedScreen struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	DwellSeconds int              `json:"dwell_seconds"`
	InRotation   bool             `json:"in_rotation"`
	Widgets      []ResolvedWidget `json:"widgets"`
}

// ResolvedLayout 是解析后的整份布局。
type ResolvedLayout struct {
	Version int              `json:"version"`
	Grid    Grid             `json:"grid"`
	Screens []ResolvedScreen `json:"screens"`
}
