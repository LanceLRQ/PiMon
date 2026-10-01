// Package screenstate 计算屏幕当前状态：时段计划、远程临时操作与关屏合成为当前主题或关屏，
// 在变化点重算并通知订阅者；同时负责 viewport 采信规则与远程操作记录。
package screenstate

import (
	"fmt"
	"sort"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	// MaxPeriods 是时段计划的时段数上限。
	MaxPeriods    = 48
	minutesPerDay = 24 * 60
)

// DefaultSchedule 是默认计划：全天 ambient，不关屏。
func DefaultSchedule() model.Schedule {
	return model.Schedule{Periods: []model.SchedulePeriod{{Start: "00:00", End: "00:00", Theme: model.ThemeAmbient}}}
}

// validTheme 判断主题取值是否在内置主题或关屏之内。
func validTheme(id string) bool {
	switch id {
	case model.ThemeOff, model.ThemeAmbient, model.ThemeMissionControl, model.ThemeIndustrial:
		return true
	}
	return false
}

// parseHM 严格解析 HH:MM（00:00–23:59），返回当天的分钟数。
func parseHM(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// formatHM 把分钟数格式化为 HH:MM；一天结束（1440）写作 24:00。
func formatHM(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

// period 是校验后的时段：从 start 起持续 length 分钟（1–1440）。
type period struct {
	start, length int
	theme         string
}

// parsePeriod 把时段转换为内部形式；Start 等于 End 视为全天。
func parsePeriod(p model.SchedulePeriod) (period, bool) {
	st, ok1 := parseHM(p.Start)
	en, ok2 := parseHM(p.End)
	if !ok1 || !ok2 {
		return period{}, false
	}
	length := (en - st + minutesPerDay) % minutesPerDay
	if length == 0 {
		length = minutesPerDay
	}
	return period{start: st, length: length, theme: p.Theme}, true
}

type segment struct{ from, to int }

// segments 把时段展开为不跨午夜的线性区间。
func (p period) segments() []segment {
	if end := p.start + p.length; end > minutesPerDay {
		return []segment{{p.start, minutesPerDay}, {0, end - minutesPerDay}}
	}
	return []segment{{p.start, p.start + p.length}}
}

// ValidateSchedule 校验时段计划：格式、主题、互不重叠、覆盖完整 24 小时。
// 返回空表示合法；重叠与缺口按线性区间（不跨午夜）逐段列出。
func ValidateSchedule(s model.Schedule) []model.ScheduleProblem {
	if len(s.Periods) == 0 {
		return []model.ScheduleProblem{{Kind: model.ScheduleProblemEmpty}}
	}
	if len(s.Periods) > MaxPeriods {
		return []model.ScheduleProblem{{Kind: model.ScheduleProblemFormat}}
	}
	var problems []model.ScheduleProblem
	var segs []segment
	for i, p := range s.Periods {
		idx := i
		parsed, ok := parsePeriod(p)
		if !ok {
			problems = append(problems, model.ScheduleProblem{Kind: model.ScheduleProblemFormat, Period: &idx})
			continue
		}
		if !validTheme(p.Theme) {
			problems = append(problems, model.ScheduleProblem{Kind: model.ScheduleProblemTheme, Period: &idx})
			continue
		}
		segs = append(segs, parsed.segments()...)
	}
	if len(problems) > 0 {
		return problems
	}
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].from != segs[j].from {
			return segs[i].from < segs[j].from
		}
		return segs[i].to < segs[j].to
	})
	cursor := 0
	for _, sg := range segs {
		switch {
		case sg.from > cursor:
			problems = append(problems, model.ScheduleProblem{Kind: model.ScheduleProblemGap, From: formatHM(cursor), To: formatHM(sg.from)})
		case sg.from < cursor:
			problems = append(problems, model.ScheduleProblem{Kind: model.ScheduleProblemOverlap, From: formatHM(sg.from), To: formatHM(min(cursor, sg.to))})
		}
		cursor = max(cursor, sg.to)
	}
	if cursor < minutesPerDay {
		problems = append(problems, model.ScheduleProblem{Kind: model.ScheduleProblemGap, From: formatHM(cursor), To: formatHM(minutesPerDay)})
	}
	return problems
}

// sortedPeriods 把已通过校验的计划转换为按开始时刻排序的内部时段；
// 相邻时段首尾相接，因此顺序即时间顺序。
func sortedPeriods(s model.Schedule) []period {
	ps := make([]period, 0, len(s.Periods))
	for _, p := range s.Periods {
		if q, ok := parsePeriod(p); ok {
			ps = append(ps, q)
		}
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].start < ps[j].start })
	return ps
}

// normalizeSchedule 返回按开始时刻排序、字段规范化的计划副本。
func normalizeSchedule(s model.Schedule) model.Schedule {
	out := model.Schedule{Periods: make([]model.SchedulePeriod, 0, len(s.Periods))}
	for _, p := range s.Periods {
		out.Periods = append(out.Periods, model.SchedulePeriod{Start: p.Start, End: p.End, Theme: p.Theme})
	}
	sort.SliceStable(out.Periods, func(i, j int) bool { return out.Periods[i].Start < out.Periods[j].Start })
	return out
}

// InvalidError 表示时段计划校验失败。
type InvalidError struct {
	Problems []model.ScheduleProblem
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("时段计划不合法（%d 项问题）", len(e.Problems))
}
