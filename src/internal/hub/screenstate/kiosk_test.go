package screenstate

import (
	"context"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func kioskFixture(t *testing.T, dr *model.DailyRestartSettings) *fixture {
	t.Helper()
	f := &fixture{t: t, db: openTestDB(t), clk: clock.NewFake(shanghai(t, 12, 0, 0)), tz: &tzVar{v: "Asia/Shanghai"}}
	f.svc = New(Config{DB: f.db, Clock: f.clk, Timezone: f.tz.get, DailyRestart: func() model.DailyRestartSettings { return *dr }})
	if err := f.svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestKiosk_从未连过状态为空(t *testing.T) {
	f := kioskFixture(t, &model.DailyRestartSettings{})
	if st := f.svc.Status(); st.Kiosk != nil {
		t.Fatalf("从未连过应为 nil: %+v", st.Kiosk)
	}
}

func TestKiosk_上报保存断开后保留内容(t *testing.T) {
	f := kioskFixture(t, &model.DailyRestartSettings{})
	f.svc.SetKioskOnline(true)
	if k := f.svc.Status().Kiosk; k == nil || !k.Online || k.LastReportAt != nil {
		t.Fatalf("连上未上报: %+v", k)
	}
	touch := true
	rss := int64(123456)
	started := f.clk.Now().Add(-time.Hour).UTC()
	f.svc.ReportKiosk(model.KioskReport{
		Version: "1.2.3", ChromiumStartedAt: &started, Restarts: 2, Touchscreen: &touch, ChromiumRSSBytes: &rss,
		IdleCheck: &model.KioskIdleCheck{User: true, CheckedAt: f.clk.Now().UTC()},
	})
	k := f.svc.Status().Kiosk
	if k == nil || !k.Online || k.Version != "1.2.3" || k.Restarts != 2 || k.Touchscreen == nil || !*k.Touchscreen ||
		k.ChromiumRSSBytes == nil || *k.ChromiumRSSBytes != rss || k.IdleCheck == nil || !k.IdleCheck.User ||
		k.LastReportAt == nil || !k.LastReportAt.Equal(f.clk.Now()) {
		t.Fatalf("上报内容 = %+v", k)
	}
	f.svc.SetKioskOnline(false)
	k = f.svc.Status().Kiosk
	if k == nil || k.Online || k.Version != "1.2.3" || k.IdleCheck == nil {
		t.Fatalf("断开后应保留最后上报: %+v", k)
	}
}

func TestKiosk_上报在返回值之后修改不影响已保存内容(t *testing.T) {
	f := kioskFixture(t, &model.DailyRestartSettings{})
	touch := true
	f.svc.ReportKiosk(model.KioskReport{Version: "v", Touchscreen: &touch})
	touch = false
	if k := f.svc.Status().Kiosk; k.Touchscreen == nil || !*k.Touchscreen {
		t.Fatalf("应深拷贝: %+v", k.Touchscreen)
	}
	k := f.svc.Status().Kiosk
	*k.Touchscreen = false
	if k2 := f.svc.Status().Kiosk; !*k2.Touchscreen {
		t.Fatal("Status 返回值应与内部状态隔离")
	}
}

func TestKiosk_NextRestart随设置与时区重算(t *testing.T) {
	dr := &model.DailyRestartSettings{Enabled: false, At: "04:00"}
	f := kioskFixture(t, dr)
	f.svc.SetKioskOnline(true)
	if k := f.svc.Status().Kiosk; k.NextRestart != nil {
		t.Fatalf("未开启应为 nil: %v", k.NextRestart)
	}
	dr.Enabled = true
	k := f.svc.Status().Kiosk
	if k.NextRestart == nil || !k.NextRestart.Equal(time.Date(2026, 10, 2, 4, 0, 0, 0, mustLoc(t, "Asia/Shanghai"))) {
		t.Fatalf("12:00 开启 04:00 → 次日 04:00，得到 %v", k.NextRestart)
	}
	dr.At = "13:30"
	if k := f.svc.Status().Kiosk; !k.NextRestart.Equal(shanghai(t, 13, 30, 0)) {
		t.Fatalf("改时刻后重算: %v", k.NextRestart)
	}
	f.tz.set("UTC")
	if k := f.svc.Status().Kiosk; !k.NextRestart.Equal(time.Date(2026, 10, 1, 13, 30, 0, 0, time.UTC)) {
		// 12:00 上海 = 04:00 UTC，下一个 13:30 UTC 在当天
		t.Fatalf("改时区后重算: %v", k.NextRestart)
	}
	dr.Enabled = false
	if k := f.svc.Status().Kiosk; k.NextRestart != nil {
		t.Fatalf("关闭后应为 nil: %v", k.NextRestart)
	}
}

func TestKiosk_Wake唤醒屏幕并记录来源(t *testing.T) {
	ctx := context.Background()
	f := kioskFixture(t, &model.DailyRestartSettings{})
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	// 12:00 是亮屏；推进到 23:30 进入夜间关屏。
	f.clk.Advance(11*time.Hour + 30*time.Minute)
	if st := f.svc.State(); st.Mode != model.ScreenModeOff {
		t.Fatalf("应关屏: %+v", st)
	}
	if err := f.svc.Wake(ctx, 0); err != nil {
		t.Fatal(err)
	}
	st := f.svc.State()
	if st.Mode != model.ScreenModeOn || st.Reason != model.ScreenReasonWake || st.Until == nil ||
		!st.Until.Equal(f.clk.Now().Add(30*time.Minute)) {
		t.Fatalf("默认 30 分钟唤醒: %+v", st)
	}
	ops, err := f.svc.Ops(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Action != model.ScreenActionWake || ops[0].ClientIP != "kiosk" {
		t.Fatalf("操作记录 = %+v", ops)
	}
	if err := f.svc.Wake(ctx, 99999); err == nil {
		t.Fatal("分钟数超限应报错")
	}
}
