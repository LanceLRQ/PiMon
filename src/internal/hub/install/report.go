package install

import (
	"io"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// Status 是单个步骤的结果。
type Status = sysinstall.Status

const (
	StatusDone    = sysinstall.StatusDone
	StatusSkipped = sysinstall.StatusSkipped
	StatusExisted = sysinstall.StatusExisted
	StatusWarning = sysinstall.StatusWarning
)

// report 是 sysinstall.Report 的薄封装，保留 install 内部的小写方法名。
type report struct{ r *sysinstall.Report }

func newReport(out io.Writer) *report { return &report{r: sysinstall.NewReport(out)} }

func (p *report) done(name, detail string)    { p.r.Done(name, detail) }
func (p *report) skipped(name, detail string) { p.r.Skipped(name, detail) }
func (p *report) existed(name, detail string) { p.r.Existed(name, detail) }
func (p *report) warn(name, detail string)    { p.r.Warn(name, detail) }

// failure 输出失败原因与已完成的步骤。
func (p *report) failure(step string, err error) { p.r.Failure(step, err) }
