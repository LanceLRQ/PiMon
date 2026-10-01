package report

import "encoding/json"

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
