package instances

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// currentIssue 返回实例当前无法运行的原因：先看插件与配置（实时计算，插件消失立即可见），
// 再看排程时发现的代理问题。
func (s *Service) currentIssue(r row) *issue {
	if _, iss := s.resolve(r); iss != nil {
		return iss
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncIssues[r.ID]
}

// toInstance 组装实例的 API 视图。展示状态由 report.ComputeDisplayState 计算；
// 暂停的实例仍按最后报告计算，但不做过期判定（Ruling 27）。
func (s *Service) toInstance(r row) model.Instance {
	iss := s.currentIssue(r)
	var interval time.Duration
	if p, ok := s.reg.Get(r.PluginID); ok {
		interval = effectiveInterval(r, p.Manifest)
	} else {
		interval = effectiveInterval(r, nil)
	}

	s.mu.Lock()
	var (
		rep     *report.Report
		lastOK  int64
		lastErr string
		fails   int
	)
	if st := s.states[r.ID]; st != nil {
		rep, lastOK, lastErr, fails = st.report, st.lastSuccessAt, st.lastErr, st.failures
	}
	s.mu.Unlock()

	in := report.DisplayInput{
		Broken:        iss != nil && iss.kind == issueBroken,
		Unconfigured:  iss != nil && iss.kind == issueUnconfigured,
		LastFailed:    fails > 0,
		LastSuccessAt: lastOK,
		Interval:      interval,
	}
	if r.Paused {
		in.LastSuccessAt = 0
	}
	if rep != nil {
		in.ReportStatus = rep.Status
	}
	inst := model.Instance{
		ID: r.ID, PluginID: r.PluginID, Name: r.Name, RunsOn: machineHub,
		IntervalSeconds: r.IntervalSeconds, EffectiveIntervalSeconds: int(interval.Seconds()),
		Paused:       r.Paused,
		DisplayState: string(report.ComputeDisplayState(in, s.clk.Now())),
		LastError:    lastErr, Failures: fails,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if iss != nil {
		inst.Issue = iss.msg
	}
	if rep != nil {
		inst.Summary, inst.ReportStatus, inst.ReportStale = rep.Summary, string(rep.Status), rep.Stale
	}
	if lastOK > 0 {
		t := time.UnixMilli(lastOK).UTC()
		inst.LastSuccessAt = &t
	}
	return inst
}

// toDetail 在实例视图上加上脱敏配置、配置问题与当前报告。
func (s *Service) toDetail(r row) model.InstanceDetail {
	d := model.InstanceDetail{Instance: s.toInstance(r), Config: map[string]any{}}
	s.mu.Lock()
	if st := s.states[r.ID]; st != nil {
		d.Report = publicReport(st.report)
	}
	s.mu.Unlock()
	if p, full, iss := s.loadFull(r); iss == nil {
		d.Config = schema.Redact(p.Manifest.ConfigSchema, full)
	} else {
		d.Config = r.Config
	}
	if _, iss := s.resolve(r); iss != nil {
		d.Problems = iss.problems
		if iss.refill {
			d.Problems = model.FieldErrors{RefillKey: model.FieldInvalid}
		}
	}
	return d
}

// publicReport 返回可经 API 输出的报告副本：去掉插件私有 state（可能缓存登录态），
// 不修改内存中共享的报告（其 state 仍要在下次运行时传回插件）。
func publicReport(rep *report.Report) *report.Report {
	if rep == nil {
		return nil
	}
	c := *rep
	c.State = ""
	return &c
}
