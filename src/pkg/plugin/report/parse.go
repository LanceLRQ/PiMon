package report

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ValidationError 汇总报告的全部校验问题，每条以字段路径开头（如 items[0].key）。
type ValidationError struct {
	Problems []string
}

// Error 实现 error 接口。
func (e *ValidationError) Error() string {
	return "报告不合法: " + strings.Join(e.Problems, "; ")
}

// Parse 解码 JSON 报告（exec 插件的 stdout）并校验。顶层未知字段忽略以便向前兼容。
// 语义见 Validate。log 为 nil 时不记日志。
func Parse(data []byte, log *slog.Logger) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, &ValidationError{Problems: []string{"不是合法的 JSON 报告: " + err.Error()}}
	}
	if err := r.Validate(log); err != nil {
		return nil, err
	}
	return &r, nil
}

// Validate 校验并规整报告（就地修改），问题一次性收集：
//   - status 必填且为 ok/warning/critical/unknown；
//   - 未知 type 的数据项丢弃并记日志（不算错误）；自带 error 的数据项保留；
//   - 数据项 key 必须是合法的具体键名（不能是通配）且不重复；state 类数据项的 state 取值须合法；
//   - events 只校验通用字段 id、type、at 必填，不解释 type；
//   - Stale 标记由运行时维护，插件自报的值被清除。
func (r *Report) Validate(log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var probs []string
	add := func(format string, a ...any) { probs = append(probs, fmt.Sprintf(format, a...)) }

	if !r.Status.Valid() {
		add("status: %q 不合法，应为 ok、warning、critical、unknown", string(r.Status))
	}
	r.Stale = false

	kept := make([]Item, 0, len(r.Items))
	seen := map[string]bool{}
	for i, it := range r.Items {
		if !IsType(it.Type) {
			log.Warn("丢弃未知类型的数据项", "key", it.Key, "type", it.Type)
			continue
		}
		path := fmt.Sprintf("items[%d]", i)
		ok := true
		if k, err := ParseKey(it.Key); err != nil {
			add("%s.key: %q 不合法: %v", path, it.Key, err)
			ok = false
		} else if k.Wildcard {
			add("%s.key: %q 是通配，报告里必须写具体成员", path, it.Key)
			ok = false
		} else if seen[it.Key] {
			add("%s.key: %q 重复", path, it.Key)
			ok = false
		}
		if it.Type == TypeState && it.State != "" && !it.State.Valid() {
			add("%s.state: %q 不合法，应为 ok、warning、critical、unknown", path, string(it.State))
			ok = false
		}
		if !ok {
			continue
		}
		seen[it.Key] = true
		it.Stale = false
		kept = append(kept, it)
	}
	r.Items = kept

	for i, e := range r.Events {
		path := fmt.Sprintf("events[%d]", i)
		if e.ID == "" {
			add("%s.id: 必填", path)
		}
		if e.Type == "" {
			add("%s.type: 必填", path)
		}
		if e.At == 0 {
			add("%s.at: 必填（Unix 毫秒）", path)
		}
	}

	if len(probs) > 0 {
		return &ValidationError{Problems: probs}
	}
	return nil
}
