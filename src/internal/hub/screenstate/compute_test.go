package screenstate

import (
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func at(loc *time.Location, y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, loc)
}

func TestCompute_22点47在ambient下一变化23点关屏(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := at(loc, 2026, 10, 1, 22, 47)
	st := compute(nightOff(), loc, now, nil)
	if st.Mode != model.ScreenModeOn || st.ThemeID != model.ThemeAmbient || st.Reason != model.ScreenReasonSchedule {
		t.Fatalf("状态 = %+v", st)
	}
	if st.Until != nil {
		t.Fatalf("无临时操作不应有 until: %v", st.Until)
	}
	if st.NextChange == nil || !st.NextChange.Equal(at(loc, 2026, 10, 1, 23, 0)) {
		t.Fatalf("next_change = %v", st.NextChange)
	}
}

func TestCompute_跨日时段(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	cases := []struct {
		now      time.Time
		mode     string
		next     time.Time
		nextMode string
	}{
		{at(loc, 2026, 10, 1, 23, 30), model.ScreenModeOff, at(loc, 2026, 10, 2, 7, 0), model.ScreenModeOn},
		{at(loc, 2026, 10, 2, 0, 0), model.ScreenModeOff, at(loc, 2026, 10, 2, 7, 0), model.ScreenModeOn},
		{at(loc, 2026, 10, 2, 6, 59), model.ScreenModeOff, at(loc, 2026, 10, 2, 7, 0), model.ScreenModeOn},
		{at(loc, 2026, 10, 2, 7, 0), model.ScreenModeOn, at(loc, 2026, 10, 2, 23, 0), model.ScreenModeOff},
	}
	for _, c := range cases {
		st := compute(nightOff(), loc, c.now, nil)
		if st.Mode != c.mode || st.NextChange == nil || !st.NextChange.Equal(c.next) {
			t.Errorf("%v: 状态 = %+v, 期望 %s 下一变化 %v", c.now, st, c.mode, c.next)
		}
	}
}

func TestCompute_关屏状态带回退主题(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	s := model.Schedule{Periods: []model.SchedulePeriod{
		per("07:00", "19:00", model.ThemeIndustrial), per("19:00", "23:00", model.ThemeMissionControl), per("23:00", "07:00", model.ThemeOff),
	}}
	st := compute(s, loc, at(loc, 2026, 10, 2, 3, 0), nil)
	if st.Mode != model.ScreenModeOff || st.ThemeID != model.ThemeMissionControl {
		t.Fatalf("关屏时回退主题应为最近的非关屏时段: %+v", st)
	}
}

func TestCompute_全天关屏回退ambient(t *testing.T) {
	loc := mustLoc(t, "UTC")
	s := model.Schedule{Periods: []model.SchedulePeriod{per("00:00", "00:00", model.ThemeOff)}}
	st := compute(s, loc, at(loc, 2026, 10, 2, 3, 0), nil)
	if st.Mode != model.ScreenModeOff || st.ThemeID != model.ThemeAmbient || st.NextChange != nil {
		t.Fatalf("状态 = %+v", st)
	}
}

func TestCompute_默认计划不再变化(t *testing.T) {
	loc := mustLoc(t, "UTC")
	st := compute(DefaultSchedule(), loc, at(loc, 2026, 10, 2, 3, 0), nil)
	if st.Mode != model.ScreenModeOn || st.ThemeID != model.ThemeAmbient || st.NextChange != nil {
		t.Fatalf("状态 = %+v", st)
	}
}

func TestCompute_相邻同主题边界不算变化(t *testing.T) {
	loc := mustLoc(t, "UTC")
	s := model.Schedule{Periods: []model.SchedulePeriod{per("00:00", "12:00", model.ThemeAmbient), per("12:00", "00:00", model.ThemeAmbient)}}
	if st := compute(s, loc, at(loc, 2026, 10, 2, 3, 0), nil); st.NextChange != nil {
		t.Fatalf("主题不变不应报下一变化: %v", st.NextChange)
	}
}

func TestCompute_远程开屏持续到下一时段边界(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := at(loc, 2026, 10, 2, 1, 0)
	ov := newOverride(nightOff(), loc, now, overlayOn, 0)
	if !ov.until.Equal(at(loc, 2026, 10, 2, 7, 0)) {
		t.Fatalf("until = %v", ov.until)
	}
	st := compute(nightOff(), loc, now, ov)
	if st.Mode != model.ScreenModeOn || st.ThemeID != model.ThemeAmbient || st.Reason != model.ScreenReasonRemoteOn {
		t.Fatalf("状态 = %+v", st)
	}
	if st.Until == nil || !st.Until.Equal(ov.until) {
		t.Fatalf("until = %v", st.Until)
	}
	// 到 07:00 临时操作过期，计划也是亮屏：不是可见变化，next_change 指向 23:00。
	if st.NextChange == nil || !st.NextChange.Equal(at(loc, 2026, 10, 2, 23, 0)) {
		t.Fatalf("next_change = %v", st.NextChange)
	}
}

func TestCompute_远程开屏取最近非关屏时段主题(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	s := model.Schedule{Periods: []model.SchedulePeriod{
		per("07:00", "19:00", model.ThemeIndustrial), per("19:00", "23:00", model.ThemeMissionControl), per("23:00", "07:00", model.ThemeOff),
	}}
	now := at(loc, 2026, 10, 2, 1, 0)
	st := compute(s, loc, now, newOverride(s, loc, now, overlayOn, 0))
	if st.ThemeID != model.ThemeMissionControl {
		t.Fatalf("主题 = %s", st.ThemeID)
	}
}

func TestCompute_远程关屏持续到下一边界后回到计划(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := at(loc, 2026, 10, 1, 20, 0)
	ov := newOverride(nightOff(), loc, now, overlayOff, 0)
	st := compute(nightOff(), loc, now, ov)
	if st.Mode != model.ScreenModeOff || st.Reason != model.ScreenReasonRemoteOff || !st.Until.Equal(at(loc, 2026, 10, 1, 23, 0)) {
		t.Fatalf("状态 = %+v", st)
	}
	// 23:00 过期后计划也是关屏：没有可见变化；下一次变化是 07:00 亮屏。
	if st.NextChange == nil || !st.NextChange.Equal(at(loc, 2026, 10, 2, 7, 0)) {
		t.Fatalf("next_change = %v", st.NextChange)
	}
	after := compute(nightOff(), loc, at(loc, 2026, 10, 1, 23, 0), ov)
	if after.Reason != model.ScreenReasonSchedule || after.Until != nil {
		t.Fatalf("到期后应回到计划: %+v", after)
	}
}

func TestCompute_临时亮屏30分钟到期回到计划(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := at(loc, 2026, 10, 2, 2, 0)
	ov := newOverride(nightOff(), loc, now, overlayWake, 30)
	st := compute(nightOff(), loc, now, ov)
	if st.Mode != model.ScreenModeOn || st.Reason != model.ScreenReasonWake || !st.Until.Equal(at(loc, 2026, 10, 2, 2, 30)) {
		t.Fatalf("状态 = %+v", st)
	}
	if st.NextChange == nil || !st.NextChange.Equal(at(loc, 2026, 10, 2, 2, 30)) {
		t.Fatalf("next_change = %v", st.NextChange)
	}
	after := compute(nightOff(), loc, at(loc, 2026, 10, 2, 2, 30), ov)
	if after.Mode != model.ScreenModeOff || after.Reason != model.ScreenReasonSchedule {
		t.Fatalf("到期后 = %+v", after)
	}
}

func TestCompute_时区变化后结果不同(t *testing.T) {
	now := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC) // 上海 23:00，伦敦 16:00（BST）
	sh := compute(nightOff(), mustLoc(t, "Asia/Shanghai"), now, nil)
	ldn := compute(nightOff(), mustLoc(t, "Europe/London"), now, nil)
	if sh.Mode != model.ScreenModeOff || ldn.Mode != model.ScreenModeOn {
		t.Fatalf("上海 %+v 伦敦 %+v", sh, ldn)
	}
}

func TestLocalInstant_DST不存在的时刻取下一个有效时刻(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-03-08 02:00–03:00 本地时间不存在；02:30 应落在 03:00 EDT（跳变瞬间）。
	got := localInstant(ny, 2026, 3, 8, 2*60+30)
	want := time.Date(2026, 3, 8, 3, 0, 0, 0, ny)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 存在的时刻不受影响。
	if g := localInstant(ny, 2026, 3, 8, 7*60); !g.Equal(time.Date(2026, 3, 8, 7, 0, 0, 0, ny)) {
		t.Fatalf("普通时刻 = %v", g)
	}
}

func TestCompute_DST边界下一变化(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	s := model.Schedule{Periods: []model.SchedulePeriod{per("02:30", "08:00", model.ThemeOff), per("08:00", "02:30", model.ThemeAmbient)}}
	now := at(ny, 2026, 3, 8, 1, 0)
	st := compute(s, ny, now, nil)
	want := time.Date(2026, 3, 8, 3, 0, 0, 0, ny)
	if st.Mode != model.ScreenModeOn || st.NextChange == nil || !st.NextChange.Equal(want) {
		t.Fatalf("状态 = %+v want next %v", st, want)
	}
}
