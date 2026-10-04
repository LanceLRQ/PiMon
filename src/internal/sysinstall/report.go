package sysinstall

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

// Report 逐步输出一行结果，并记录下来供失败时汇总已完成的步骤。
type Report struct {
	out     io.Writer
	entries []entry
}

// NewReport 创建向 out 输出的步骤报告。
func NewReport(out io.Writer) *Report { return &Report{out: out} }

// Add 记录并输出一个步骤结果。
func (r *Report) Add(st Status, name, detail string) {
	r.entries = append(r.entries, entry{name, st, detail})
	if detail != "" {
		_, _ = fmt.Fprintf(r.out, "[%s] %s（%s）\n", st, name, detail)
		return
	}
	_, _ = fmt.Fprintf(r.out, "[%s] %s\n", st, name)
}

func (r *Report) Done(name, detail string)    { r.Add(StatusDone, name, detail) }
func (r *Report) Skipped(name, detail string) { r.Add(StatusSkipped, name, detail) }
func (r *Report) Existed(name, detail string) { r.Add(StatusExisted, name, detail) }
func (r *Report) Warn(name, detail string)    { r.Add(StatusWarning, name, detail) }

// Failure 输出失败原因与已完成的步骤。
func (r *Report) Failure(step string, err error) {
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
