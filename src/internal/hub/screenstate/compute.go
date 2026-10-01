package screenstate

import (
	"sort"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 状态机最多睡眠的时长：即使下一个变化点更远也每分钟重算一次，防冷启动时 NTP 校时造成的时钟跳变。
const maxSleep = 60 * time.Second

// 向前查找变化点的范围：覆盖两天的时段边界，足以确认「不再变化」。
const changeHorizon = 49 * time.Hour

type overlay int

const (
	overlayOn overlay = iota
	overlayOff
	overlayWake
)

// override 是远程临时操作：开屏、关屏持续到下一个时段边界，临时亮屏持续固定时长。
type override struct {
	kind  overlay
	until time.Time
}

func (o *override) activeAt(t time.Time) bool { return o != nil && t.Before(o.until) }

func (o *override) reason() string {
	switch o.kind {
	case overlayOff:
		return model.ScreenReasonRemoteOff
	case overlayWake:
		return model.ScreenReasonWake
	default:
		return model.ScreenReasonRemoteOn
	}
}

// newOverride 创建远程临时操作；minutes 只对 overlayWake 有意义。
func newOverride(s model.Schedule, loc *time.Location, now time.Time, kind overlay, minutes int) *override {
	if kind == overlayWake {
		return &override{kind: kind, until: now.Add(time.Duration(minutes) * time.Minute)}
	}
	ps := sortedPeriods(s)
	bs := boundariesAfter(ps, loc, now, 25*time.Hour)
	until := now.Add(24 * time.Hour)
	if len(bs) > 0 {
		until = bs[0]
	}
	return &override{kind: kind, until: until}
}

// localInstant 返回 loc 时区某日本地第 minutes 分钟对应的时刻；
// 该本地时刻因夏令时跳变不存在时，取跳变后的第一个有效时刻。
func localInstant(loc *time.Location, y int, m time.Month, d, minutes int) time.Time {
	y, m, d = time.Date(y, m, d, 12, 0, 0, 0, loc).Date() // 规范化溢出的日期
	t := time.Date(y, m, d, minutes/60, minutes%60, 0, 0, loc)
	if t.Hour()*60+t.Minute() == minutes {
		return t
	}
	// Date 对不存在的本地时刻的取值没有保证：从前后各 12 小时里找第一个本地时钟不早于目标的分钟。
	key := func(x time.Time) int {
		l := x.In(loc)
		return ((l.Year()*12+int(l.Month()))*32+l.Day())*minutesPerDay + l.Hour()*60 + l.Minute()
	}
	want := ((y*12+int(m))*32+d)*minutesPerDay + minutes
	c := t.Add(-12 * time.Hour).Truncate(time.Minute)
	for i := 0; i < 24*60; i++ {
		if key(c) >= want {
			return c
		}
		c = c.Add(time.Minute)
	}
	return t
}

// boundariesAfter 返回 (now, now+within] 内全部时段开始时刻，升序。
func boundariesAfter(ps []period, loc *time.Location, now time.Time, within time.Duration) []time.Time {
	ln := now.In(loc)
	y, m, d := ln.Date()
	limit := now.Add(within)
	var out []time.Time
	for off := 0; off <= 2; off++ {
		for _, p := range ps {
			c := localInstant(loc, y, m, d+off, p.start)
			if c.After(now) && !c.After(limit) {
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// planAt 返回 t 时刻所在时段的下标；ps 必须覆盖完整 24 小时。
func planAt(ps []period, loc *time.Location, t time.Time) int {
	ln := t.In(loc)
	m := ln.Hour()*60 + ln.Minute()
	for i, p := range ps {
		if (m-p.start+minutesPerDay)%minutesPerDay < p.length {
			return i
		}
	}
	return 0
}

// lastOnTheme 从下标 idx 起向前找最近一个非关屏时段的主题；没有就用 ambient。
func lastOnTheme(ps []period, idx int) string {
	n := len(ps)
	for k := 0; k < n; k++ {
		if p := ps[(idx-k+n)%n]; p.theme != model.ThemeOff {
			return p.theme
		}
	}
	return model.ThemeAmbient
}

// stateAt 计算 t 时刻的状态（不含 next_change）。
func stateAt(ps []period, loc *time.Location, t time.Time, ov *override) model.ScreenState {
	idx := planAt(ps, loc, t)
	st := model.ScreenState{ThemeID: lastOnTheme(ps, idx), Mode: model.ScreenModeOn, Reason: model.ScreenReasonSchedule}
	if ov.activeAt(t) {
		st.Reason = ov.reason()
		if ov.kind == overlayOff {
			st.Mode = model.ScreenModeOff
		}
		return st
	}
	if ps[idx].theme == model.ThemeOff {
		st.Mode = model.ScreenModeOff
	}
	return st
}

// visiblyDiffers 判断两个状态是否构成可见变化：模式不同，或亮屏时主题不同。
func visiblyDiffers(a, b model.ScreenState) bool {
	return a.Mode != b.Mode || (a.Mode == model.ScreenModeOn && a.ThemeID != b.ThemeID)
}

// Compute 计算 now 时刻的屏幕状态与下一次可见变化的时刻。
// 优先级：远程临时操作 > 时段计划；计划不合法时按默认计划计算。
func Compute(s model.Schedule, loc *time.Location, now time.Time, ov *override) model.ScreenState {
	ps := sortedPeriods(s)
	if len(ps) == 0 || len(ValidateSchedule(s)) > 0 {
		ps = sortedPeriods(DefaultSchedule())
	}
	st := stateAt(ps, loc, now, ov)
	if ov.activeAt(now) {
		u := ov.until
		st.Until = &u
	}
	cands := boundariesAfter(ps, loc, now, changeHorizon)
	if ov.activeAt(now) {
		cands = append(cands, ov.until)
		sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
	}
	for _, c := range cands {
		if visiblyDiffers(st, stateAt(ps, loc, c, ov)) {
			next := c
			st.NextChange = &next
			break
		}
	}
	return st
}

// sleepFor 返回下一次重算前应睡眠的时长：到最近的时段边界或临时操作到期点，最多 maxSleep。
func sleepFor(s model.Schedule, loc *time.Location, now time.Time, ov *override) time.Duration {
	ps := sortedPeriods(s)
	if len(ps) == 0 || len(ValidateSchedule(s)) > 0 {
		ps = sortedPeriods(DefaultSchedule())
	}
	d := maxSleep
	if bs := boundariesAfter(ps, loc, now, maxSleep); len(bs) > 0 {
		d = min(d, bs[0].Sub(now))
	}
	if ov.activeAt(now) {
		d = min(d, ov.until.Sub(now))
	}
	return d
}
