package install

import (
	"fmt"
	"io"
)

// Status 是单个步骤的结果。
type Status string

const (
	StatusDone    Status = "完成"
	StatusSkipped Status = "跳过"
	StatusExisted Status = "已存在"
	StatusWarning Status = "警告"
)

type entry struct {
	Name   string
	Status Status
	Detail string
}

// report 逐步输出一行结果，并记录下来供失败时汇总已完成的步骤。
type report struct {
	out     io.Writer
	entries []entry
}

func (r *report) add(st Status, name, detail string) {
	r.entries = append(r.entries, entry{name, st, detail})
	if detail != "" {
		_, _ = fmt.Fprintf(r.out, "[%s] %s（%s）\n", st, name, detail)
		return
	}
	_, _ = fmt.Fprintf(r.out, "[%s] %s\n", st, name)
}

func (r *report) done(name, detail string)    { r.add(StatusDone, name, detail) }
func (r *report) skipped(name, detail string) { r.add(StatusSkipped, name, detail) }
func (r *report) existed(name, detail string) { r.add(StatusExisted, name, detail) }
func (r *report) warn(name, detail string)    { r.add(StatusWarning, name, detail) }

// failure 输出失败原因与已完成的步骤。
func (r *report) failure(step string, err error) {
	_, _ = fmt.Fprintf(r.out, "[失败] %s：%v\n", step, err)
	if len(r.entries) == 0 {
		_, _ = fmt.Fprintln(r.out, "尚未完成任何步骤，系统未做改动。")
		return
	}
	_, _ = fmt.Fprintln(r.out, "已完成的步骤：")
	for _, e := range r.entries {
		_, _ = fmt.Fprintf(r.out, "  - %s：%s\n", e.Status, e.Name)
	}
	_, _ = fmt.Fprintln(r.out, "排除问题后重新运行 install 即可，已完成的部分会被跳过。")
}
