package screenstate

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

type fixture struct {
	t   *testing.T
	db  *store.DB
	clk *clock.Fake
	tz  *tzVar
	svc *Service
}

type tzVar struct {
	mu sync.Mutex
	v  string
}

func (z *tzVar) get() string  { z.mu.Lock(); defer z.mu.Unlock(); return z.v }
func (z *tzVar) set(v string) { z.mu.Lock(); z.v = v; z.mu.Unlock() }

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

// newFixture 以上海时区的 start 时刻创建服务并 Load。
func newFixture(t *testing.T, start time.Time) *fixture {
	t.Helper()
	f := &fixture{t: t, db: openTestDB(t), clk: clock.NewFake(start), tz: &tzVar{v: "Asia/Shanghai"}}
	f.svc = New(Config{DB: f.db, Clock: f.clk, Timezone: f.tz.get})
	if err := f.svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f
}

func shanghai(t *testing.T, h, m, s int) time.Time {
	t.Helper()
	return time.Date(2026, 10, 1, h, m, s, 0, mustLoc(t, "Asia/Shanghai"))
}

func TestService_默认计划全天ambient(t *testing.T) {
	f := newFixture(t, shanghai(t, 12, 0, 0))
	st := f.svc.State()
	if st.Mode != model.ScreenModeOn || st.ThemeID != model.ThemeAmbient || st.NextChange != nil {
		t.Fatalf("状态 = %+v", st)
	}
	if got := f.svc.Schedule(); len(got.Periods) != 1 {
		t.Fatalf("计划 = %+v", got)
	}
}

func TestService_SetSchedule校验与持久化(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 22, 47, 0))
	bad := model.Schedule{Periods: []model.SchedulePeriod{per("07:00", "12:00", model.ThemeAmbient)}}
	var inv *InvalidError
	if err := f.svc.SetSchedule(ctx, bad); !errors.As(err, &inv) || len(inv.Problems) == 0 {
		t.Fatalf("err = %v", err)
	}
	if got := f.svc.Schedule(); len(got.Periods) != 1 || got.Periods[0].Theme != model.ThemeAmbient || got.Periods[0].Start != "00:00" {
		t.Fatalf("非法计划不应改变现有计划: %+v", got)
	}
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	st := f.svc.State()
	if st.NextChange == nil || !st.NextChange.Equal(shanghai(t, 23, 0, 0)) {
		t.Fatalf("保存后立即按新计划计算: %+v", st)
	}
	// 重新启动：从库中读回。
	again := New(Config{DB: f.db, Clock: f.clk, Timezone: f.tz.get})
	if err := again.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := again.Schedule(); len(got.Periods) != 2 {
		t.Fatalf("重启后计划 = %+v", got)
	}
}

func TestService_SetSchedule按开始时刻规范化顺序(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 12, 0, 0))
	s := model.Schedule{Periods: []model.SchedulePeriod{per("23:00", "07:00", model.ThemeOff), per("07:00", "23:00", model.ThemeAmbient)}}
	if err := f.svc.SetSchedule(ctx, s); err != nil {
		t.Fatal(err)
	}
	got := f.svc.Schedule()
	if got.Periods[0].Start != "07:00" || got.Periods[1].Start != "23:00" {
		t.Fatalf("应按开始时刻排序: %+v", got)
	}
}

func TestService_临时亮屏到期回到计划(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 2, 0, 0))
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	if st := f.svc.State(); st.Mode != model.ScreenModeOff {
		t.Fatalf("初始 = %+v", st)
	}
	resp, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionWake, Minutes: 30}, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if resp.State.Mode != model.ScreenModeOn || resp.State.Reason != model.ScreenReasonWake {
		t.Fatalf("响应状态 = %+v", resp.State)
	}
	f.clk.Advance(29 * time.Minute)
	if st := f.svc.State(); st.Mode != model.ScreenModeOn {
		t.Fatalf("29 分钟时仍应亮屏: %+v", st)
	}
	f.clk.Advance(time.Minute)
	if st := f.svc.State(); st.Mode != model.ScreenModeOff || st.Reason != model.ScreenReasonSchedule {
		t.Fatalf("30 分钟到期应回到计划: %+v", st)
	}
}

func TestService_wake缺省30分钟且范围校验(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 12, 0, 0))
	resp, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionWake}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Op.Params["minutes"]; got != 30 {
		t.Fatalf("记录的 minutes = %v", got)
	}
	for _, m := range []int{-1, 1441} {
		var fe model.FieldErrors
		_, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionWake, Minutes: m}, "")
		if !errors.As(err, &fe) || fe["minutes"] != model.FieldOutOfRange {
			t.Errorf("minutes=%d err = %v", m, err)
		}
	}
}

func TestService_Control校验动作与参数(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 12, 0, 0))
	var fe model.FieldErrors
	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: "explode"}, ""); !errors.As(err, &fe) || fe["action"] != model.FieldInvalid {
		t.Fatalf("未知动作 err = %v", err)
	}
	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionSwitch}, ""); !errors.As(err, &fe) || fe["screen_id"] != model.FieldRequired {
		t.Fatalf("switch 缺 screen_id err = %v", err)
	}
	if ops, _ := f.svc.Ops(ctx, 10); len(ops) != 0 {
		t.Fatalf("校验失败不应写记录: %+v", ops)
	}
}

func TestService_远程关屏与开屏(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 20, 0, 0))
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionOff}, ""); err != nil {
		t.Fatal(err)
	}
	st := f.svc.State()
	if st.Mode != model.ScreenModeOff || st.Reason != model.ScreenReasonRemoteOff || st.Until == nil || !st.Until.Equal(shanghai(t, 23, 0, 0)) {
		t.Fatalf("远程关屏 = %+v", st)
	}
	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionOn}, ""); err != nil {
		t.Fatal(err)
	}
	if st := f.svc.State(); st.Mode != model.ScreenModeOn || st.Reason != model.ScreenReasonRemoteOn {
		t.Fatalf("远程开屏 = %+v", st)
	}
}

func TestService_操作记录与一次性指令回调(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 12, 0, 0))
	var cmds []Command
	f.svc.OnCommand(func(c Command) { cmds = append(cmds, c) })

	r1, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionRefresh}, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionSwitch, ScreenID: "screen1"}, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionOff}, "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[0].Action != model.ScreenActionRefresh || cmds[0].OpID != r1.Op.ID ||
		cmds[1].Action != model.ScreenActionSwitch || cmds[1].ScreenID != "screen1" || cmds[1].OpID != r2.Op.ID {
		t.Fatalf("指令回调 = %+v", cmds)
	}
	ops, err := f.svc.Ops(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 || ops[0].Action != model.ScreenActionOff || ops[2].Action != model.ScreenActionRefresh {
		t.Fatalf("记录应新到旧: %+v", ops)
	}
	if ops[0].ClientIP != "10.0.0.3" || !ops[0].Delivered {
		t.Fatalf("状态类操作记为已送达: %+v", ops[0])
	}
	if ops[2].Delivered || ops[1].Params["screen_id"] != "screen1" {
		t.Fatalf("一次性指令初始未送达: %+v / %+v", ops[2], ops[1])
	}
	if err := f.svc.MarkDelivered(ctx, r1.Op.ID); err != nil {
		t.Fatal(err)
	}
	ops, _ = f.svc.Ops(ctx, 2)
	if len(ops) != 2 {
		t.Fatalf("limit 未生效: %d", len(ops))
	}
	all, _ := f.svc.Ops(ctx, 10)
	if !all[2].Delivered {
		t.Fatal("MarkDelivered 未生效")
	}
}

func TestService_RecordOp写token_reset(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 12, 0, 0))
	op, err := f.svc.RecordOp(ctx, model.ScreenActionTokenReset, nil, "10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if op.Action != model.ScreenActionTokenReset || !op.Delivered || op.ClientIP != "10.0.0.9" || op.Params == nil {
		t.Fatalf("op = %+v", op)
	}
}

func TestService_操作记录有上限(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 12, 0, 0))
	for i := 0; i < MaxOps+5; i++ {
		if _, err := f.svc.RecordOp(ctx, model.ScreenActionTokenReset, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM screen_ops`).Scan(&n); err != nil || n != MaxOps {
		t.Fatalf("记录数 = %d err=%v", n, err)
	}
}

func TestService_时区变化后重算并通知(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)) // 上海 23:00
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	var got []model.ScreenState
	f.svc.OnChange(func(s model.ScreenState) { got = append(got, s) })
	if st := f.svc.State(); st.Mode != model.ScreenModeOff {
		t.Fatalf("上海 23:00 应关屏: %+v", st)
	}
	f.svc.Refresh() // 基线：与上次状态一致，不通知
	f.tz.set("Europe/London")
	f.svc.Refresh()
	if len(got) != 1 || got[0].Mode != model.ScreenModeOn {
		t.Fatalf("时区变化后应通知亮屏: %+v", got)
	}
	f.svc.Refresh()
	if len(got) != 1 {
		t.Fatalf("状态未变不应重复通知: %d", len(got))
	}
}

func TestService_非法时区回退UTC(t *testing.T) {
	f := newFixture(t, time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC))
	f.tz.set("Mars/Base")
	if err := f.svc.SetSchedule(context.Background(), nightOff()); err != nil {
		t.Fatal(err)
	}
	if st := f.svc.State(); st.Mode != model.ScreenModeOn { // UTC 15:00
		t.Fatalf("状态 = %+v", st)
	}
}

func TestService_Run在变化点重算并通知(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t, shanghai(t, 22, 59, 30))
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	changed := make(chan model.ScreenState, 4)
	f.svc.OnChange(func(s model.ScreenState) { changed <- s })
	done := make(chan struct{})
	go func() { f.svc.Run(ctx); close(done) }()

	waitWaiters(t, f.clk, 1)
	f.clk.Advance(30 * time.Second) // 恰好到 23:00:00，睡眠时长取到变化点而不是 60 秒
	select {
	case s := <-changed:
		if s.Mode != model.ScreenModeOff {
			t.Fatalf("状态 = %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("到达变化点后应通知")
	}
	cancel()
	<-done
}

func TestService_Run每次最多睡60秒(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t, shanghai(t, 12, 0, 0)) // 默认计划，无变化点
	done := make(chan struct{})
	go func() { f.svc.Run(ctx); close(done) }()
	waitWaiters(t, f.clk, 1)
	f.clk.Advance(59 * time.Second)
	if f.clk.Waiters() != 1 {
		t.Fatal("59 秒时不应醒来")
	}
	f.clk.Advance(time.Second)
	waitWaiters(t, f.clk, 1) // 醒来重算后重新入睡
	cancel()
	<-done
}

func TestService_Run远程操作后按新到期点重睡(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t, shanghai(t, 2, 0, 0))
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	changed := make(chan model.ScreenState, 4)
	f.svc.OnChange(func(s model.ScreenState) { changed <- s })
	done := make(chan struct{})
	go func() { f.svc.Run(ctx); close(done) }()
	waitWaiters(t, f.clk, 1)

	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionWake, Minutes: 1}, ""); err != nil {
		t.Fatal(err)
	}
	if s := <-changed; s.Mode != model.ScreenModeOn {
		t.Fatalf("亮屏通知 = %+v", s)
	}
	waitWaiters(t, f.clk, 2) // 旧睡眠仍挂着，新的 1 分钟内到期点也挂上了
	f.clk.Advance(time.Minute)
	select {
	case s := <-changed:
		if s.Mode != model.ScreenModeOff {
			t.Fatalf("到期通知 = %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("临时亮屏到期后应通知")
	}
	cancel()
	<-done
}

func TestService_viewport关屏期间忽略并随状态切换(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, shanghai(t, 2, 0, 0))
	if err := f.svc.SetSchedule(ctx, nightOff()); err != nil {
		t.Fatal(err)
	}
	f.svc.Refresh()
	var adopted []model.Viewport
	var mu sync.Mutex
	f.svc.OnViewport(func(v model.Viewport) { mu.Lock(); adopted = append(adopted, v); mu.Unlock() })
	f.svc.ReportViewport(model.Viewport{W: 1920, H: 1080, DPR: 1})
	f.clk.Advance(3 * time.Second)
	if f.clk.Waiters() != 0 || f.svc.Status().Viewport != nil {
		t.Fatal("关屏期间的 viewport 应被忽略")
	}
	if _, err := f.svc.Control(ctx, model.ScreenControlRequest{Action: model.ScreenActionOn}, ""); err != nil {
		t.Fatal(err)
	}
	f.svc.ReportViewport(model.Viewport{W: 1024, H: 600, DPR: 1}) // 刚唤醒：忽略
	if f.clk.Waiters() != 0 {
		t.Fatal("唤醒后 5 秒内应忽略")
	}
	f.clk.Advance(5 * time.Second)
	f.svc.ReportViewport(model.Viewport{W: 1024, H: 600, DPR: 1})
	f.clk.Advance(2 * time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(adopted)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("稳定后应采信")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if f.svc.Status().Viewport == nil || len(adopted) != 1 || adopted[0].W != 1024 {
		t.Fatalf("回调 = %+v", adopted)
	}
}

func TestService_Status带触摸能力(t *testing.T) {
	f := newFixture(t, shanghai(t, 12, 0, 0))
	if f.svc.Status().CoarsePointer != nil {
		t.Fatal("未上报时应缺省")
	}
	f.svc.ReportCoarsePointer(true)
	if cp := f.svc.Status().CoarsePointer; cp == nil || !*cp {
		t.Fatalf("CoarsePointer = %v", cp)
	}
}

// waitWaiters 等待假时钟上至少挂了 n 个等待者。
func waitWaiters(t *testing.T, clk *clock.Fake, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for clk.Waiters() < n {
		if time.Now().After(deadline) {
			t.Fatalf("等待者 = %d, 期望至少 %d", clk.Waiters(), n)
		}
		time.Sleep(time.Millisecond)
	}
}
