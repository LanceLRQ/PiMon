package report

import "encoding/json"

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

// Event 是报告中的事件。本期只校验通用字段 id、type、at，不解释 type；
// 其余字段（seq 等）原样保存在 Extra 中。
type Event struct {
	ID    string
	Type  string
	At    int64
	Extra map[string]json.RawMessage
}

// UnmarshalJSON 取出通用字段，其余进入 Extra。
func (e *Event) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*e = Event{}
	take := func(k string, dst any) error {
		raw, ok := m[k]
		if !ok {
			return nil
		}
		delete(m, k)
		if string(raw) == "null" {
			return nil
		}
		return json.Unmarshal(raw, dst)
	}
	if err := take("id", &e.ID); err != nil {
		return err
	}
	if err := take("type", &e.Type); err != nil {
		return err
	}
	if err := take("at", &e.At); err != nil {
		return err
	}
	if len(m) > 0 {
		e.Extra = m
	}
	return nil
}

// MarshalJSON 输出通用字段并合并 Extra（Extra 中与通用字段同名的键被忽略）。
func (e Event) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(e.Extra)+3)
	for k, v := range e.Extra {
		m[k] = v
	}
	id, _ := json.Marshal(e.ID)
	typ, _ := json.Marshal(e.Type)
	at, _ := json.Marshal(e.At)
	m["id"], m["type"], m["at"] = id, typ, at
	return json.Marshal(m)
}
