package report

// Status 是插件对被测对象的判断；采集本身失败不用它表达。
type Status string

// 报告状态。
const (
	StatusOK       Status = "ok"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	StatusUnknown  Status = "unknown"
)

// Valid 报告 s 是否为四个合法取值之一。
func (s Status) Valid() bool {
	switch s {
	case StatusOK, StatusWarning, StatusCritical, StatusUnknown:
		return true
	}
	return false
}

// 数据项类型。
const (
	TypeGauge  = "gauge"
	TypeNumber = "number"
	TypeQuota  = "quota"
	TypeMoney  = "money"
	TypeState  = "state"
	TypeText   = "text"
	TypeTable  = "table"
)

// ItemTypes 是全部数据项类型（设计 2.3），按设计表格顺序。
var ItemTypes = []string{TypeGauge, TypeNumber, TypeQuota, TypeMoney, TypeState, TypeText, TypeTable}

// typeFields 是各类型的字段集，首个之外的顺序与设计表格一致。
var typeFields = map[string][]string{
	TypeGauge:  {"value", "unit", "min", "max"},
	TypeNumber: {"value", "unit"},
	TypeQuota:  {"used", "total", "remaining", "remaining_pct", "resets_at", "expires_at", "unit", "label"},
	TypeMoney:  {"amount", "currency", "used", "total"},
	TypeState:  {"state", "text"},
	TypeText:   {"text"},
	TypeTable:  {"columns", "rows"},
}

// typeDefault 是各类型的默认字段；table 没有默认字段。
var typeDefault = map[string]string{
	TypeGauge:  "value",
	TypeNumber: "value",
	TypeQuota:  "remaining_pct",
	TypeMoney:  "amount",
	TypeState:  "state",
	TypeText:   "text",
}

// Item 是报告中的一个数据项。各类型只使用自己的字段（见 FieldsOf），
// 数值字段用指针区分“缺失”与 0。
type Item struct {
	Key  string `json:"key"`
	Type string `json:"type"`

	Value        *float64 `json:"value,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	Used         *float64 `json:"used,omitempty"`
	Total        *float64 `json:"total,omitempty"`
	Remaining    *float64 `json:"remaining,omitempty"`
	RemainingPct *float64 `json:"remaining_pct,omitempty"`
	ResetsAt     *int64   `json:"resets_at,omitempty"`
	ExpiresAt    *int64   `json:"expires_at,omitempty"`
	Label        string   `json:"label,omitempty"`
	Amount       *float64 `json:"amount,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	State        Status   `json:"state,omitempty"`
	Text         string   `json:"text,omitempty"`
	Columns      []string `json:"columns,omitempty"`
	Rows         [][]any  `json:"rows,omitempty"`

	// Error 是条目级错误（如“mdadm 调用失败”），由条目级状态表达，数据项仍保留。
	Error string `json:"error,omitempty"`
	// Stale 由运行时在采集失败保留旧值时置位，插件自报的值会被解析忽略。
	Stale bool `json:"stale,omitempty"`
}

// Report 是插件一次采集的全量快照，同时是 exec 插件 stdout 的 JSON 结构。
type Report struct {
	Status      Status  `json:"status"`
	Summary     string  `json:"summary,omitempty"`
	CollectedAt int64   `json:"collected_at,omitempty"`
	DurationMs  int64   `json:"duration_ms,omitempty"`
	Items       []Item  `json:"items,omitempty"`
	Events      []Event `json:"events,omitempty"`
	// State 是插件私有状态，对运行时不透明，下次运行时传回。
	State string `json:"state,omitempty"`
	// Stale 表示本报告是采集失败后保留的旧值。
	Stale bool `json:"stale,omitempty"`
}

// Event 的 JSON 形状由 event.go 里的自定义编解码决定（扁平对象），
// tygo 无法从结构体推出，因此排除 event.go 并在此直接给出 TS 声明。
//
//tygo:emit
var _ = "export interface Event { id: string; type: string; at: number; [key: string]: unknown }"
