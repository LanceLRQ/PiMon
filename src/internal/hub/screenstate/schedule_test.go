package screenstate

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func per(start, end, theme string) model.SchedulePeriod {
	return model.SchedulePeriod{Start: start, End: end, Theme: theme}
}

// nightOff 是常用计划：白天 ambient，23:00 到次日 07:00 关屏。
func nightOff() model.Schedule {
	return model.Schedule{Periods: []model.SchedulePeriod{
		per("07:00", "23:00", model.ThemeAmbient),
		per("23:00", "07:00", model.ThemeOff),
	}}
}

func TestValidateSchedule_合法(t *testing.T) {
	cases := map[string]model.Schedule{
		"默认全天":     DefaultSchedule(),
		"跨日两段":     nightOff(),
		"三段含浅色":    {Periods: []model.SchedulePeriod{per("07:00", "19:00", model.ThemeIndustrial), per("19:00", "23:00", model.ThemeAmbient), per("23:00", "07:00", model.ThemeOff)}},
		"乱序给出":     {Periods: []model.SchedulePeriod{per("23:00", "07:00", model.ThemeOff), per("07:00", "23:00", model.ThemeMissionControl)}},
		"边界在午夜":    {Periods: []model.SchedulePeriod{per("00:00", "12:00", model.ThemeAmbient), per("12:00", "00:00", model.ThemeIndustrial)}},
		"全天关屏":     {Periods: []model.SchedulePeriod{per("06:30", "06:30", model.ThemeOff)}},
		"相邻同主题不合并": {Periods: []model.SchedulePeriod{per("00:00", "12:00", model.ThemeAmbient), per("12:00", "00:00", model.ThemeAmbient)}},
	}
	for name, s := range cases {
		if p := ValidateSchedule(s); len(p) != 0 {
			t.Errorf("%s: 不应有问题: %+v", name, p)
		}
	}
}

func TestDefaultSchedule_全天ambient(t *testing.T) {
	d := DefaultSchedule()
	if len(d.Periods) != 1 || d.Periods[0].Theme != model.ThemeAmbient {
		t.Fatalf("默认计划 = %+v", d)
	}
}

func TestValidateSchedule_缺口(t *testing.T) {
	s := model.Schedule{Periods: []model.SchedulePeriod{per("07:00", "12:00", model.ThemeAmbient), per("13:00", "07:00", model.ThemeOff)}}
	p := ValidateSchedule(s)
	if len(p) != 1 || p[0].Kind != model.ScheduleProblemGap || p[0].From != "12:00" || p[0].To != "13:00" {
		t.Fatalf("缺口 = %+v", p)
	}
}

func TestValidateSchedule_缺口跨午夜与首尾(t *testing.T) {
	// 只覆盖 08:00–20:00：缺口为 00:00–08:00 与 20:00–24:00，按线性区间拆开报告。
	s := model.Schedule{Periods: []model.SchedulePeriod{per("08:00", "20:00", model.ThemeAmbient)}}
	p := ValidateSchedule(s)
	if len(p) != 2 || p[0].From != "00:00" || p[0].To != "08:00" || p[1].From != "20:00" || p[1].To != "24:00" {
		t.Fatalf("缺口 = %+v", p)
	}
}

func TestValidateSchedule_重叠(t *testing.T) {
	s := model.Schedule{Periods: []model.SchedulePeriod{per("07:00", "12:00", model.ThemeAmbient), per("11:00", "07:00", model.ThemeOff)}}
	p := ValidateSchedule(s)
	if len(p) != 1 || p[0].Kind != model.ScheduleProblemOverlap || p[0].From != "11:00" || p[0].To != "12:00" {
		t.Fatalf("重叠 = %+v", p)
	}
}

func TestValidateSchedule_全天时段与其他时段重叠(t *testing.T) {
	s := model.Schedule{Periods: []model.SchedulePeriod{per("00:00", "00:00", model.ThemeAmbient), per("08:00", "09:00", model.ThemeOff)}}
	p := ValidateSchedule(s)
	if len(p) != 1 || p[0].Kind != model.ScheduleProblemOverlap || p[0].From != "08:00" || p[0].To != "09:00" {
		t.Fatalf("重叠 = %+v", p)
	}
}

func TestValidateSchedule_格式主题与空(t *testing.T) {
	if p := ValidateSchedule(model.Schedule{}); len(p) != 1 || p[0].Kind != model.ScheduleProblemEmpty {
		t.Fatalf("空计划 = %+v", p)
	}
	bad := []model.SchedulePeriod{
		per("7:00", "12:00", model.ThemeAmbient),
		per("12:00", "25:00", model.ThemeAmbient),
		per("12:00", "12:60", model.ThemeAmbient),
		per("12:00", "13:00", "neon"),
		per("12:00", "13:00", ""),
	}
	for i, b := range bad {
		p := ValidateSchedule(model.Schedule{Periods: []model.SchedulePeriod{b}})
		if len(p) == 0 || p[0].Period == nil || *p[0].Period != 0 {
			t.Errorf("第 %d 个应报格式或主题问题: %+v", i, p)
			continue
		}
		if k := p[0].Kind; k != model.ScheduleProblemFormat && k != model.ScheduleProblemTheme {
			t.Errorf("第 %d 个类别 = %s", i, k)
		}
	}
}

func TestValidateSchedule_时段过多(t *testing.T) {
	var ps []model.SchedulePeriod
	for i := 0; i < MaxPeriods+1; i++ {
		ps = append(ps, per("00:00", "00:00", model.ThemeAmbient))
	}
	if p := ValidateSchedule(model.Schedule{Periods: ps}); len(p) == 0 {
		t.Fatal("超过上限应报错")
	}
}

func TestValidateSchedule_空长度时段在多段时非法(t *testing.T) {
	s := model.Schedule{Periods: []model.SchedulePeriod{per("08:00", "08:00", model.ThemeAmbient), per("09:00", "10:00", model.ThemeOff)}}
	if p := ValidateSchedule(s); len(p) == 0 {
		t.Fatal("多段时 Start==End 视为全天，必须与其他时段重叠")
	}
}
