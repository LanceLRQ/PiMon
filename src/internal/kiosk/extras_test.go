package kiosk

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestDaemon_ReportNow立即上报(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	h.link.waitReport(t, func(r model.KioskReport) bool { return r.ChromiumStartedAt != nil })
	h.d.ReportNow()
	h.link.waitReport(t, func(r model.KioskReport) bool { return r.ChromiumStartedAt != nil && r.Version == "v1" })
}

func TestDaemon_周期上报(t *testing.T) {
	var fills atomic.Int64
	h := newHarness(t, func(c *Config) {
		c.ReportInterval = time.Minute
		c.FillReport = func(r *model.KioskReport) {
			v := fills.Add(1)
			r.ChromiumRSSBytes = &v
		}
	})
	h.start()
	h.clk.waitArmed(t, time.Minute)
	base := fills.Load()
	h.clk.Advance(time.Minute)
	h.link.waitReport(t, func(r model.KioskReport) bool { return r.ChromiumRSSBytes != nil && *r.ChromiumRSSBytes > base })
	h.clk.waitArmed(t, time.Minute) // 下一周期已挂好
}

func TestDaemon_Services随守护进程启停(t *testing.T) {
	started := make(chan struct{})
	var stopped atomic.Bool
	h := newHarness(t, func(c *Config) {
		c.Services = []func(context.Context){func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			stopped.Store(true)
		}}
	})
	h.start()
	select {
	case <-started:
	case <-time.After(waitLimit):
		t.Fatal("服务未启动")
	}
	h.cancel()
	if err := h.waitExit(); err != nil {
		t.Fatal(err)
	}
	if !stopped.Load() {
		t.Fatal("Run 返回前服务应已结束")
	}
}

func TestDaemon_ChromiumPID随进程状态变化(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	if h.d.ChromiumPID() == 0 {
		h.link.waitReport(t, func(r model.KioskReport) bool { return r.ChromiumStartedAt != nil })
	}
	p := h.launcher.next(t)
	if got := h.d.ChromiumPID(); got != fakePID {
		t.Fatalf("pid=%d", got)
	}
	p.exit() // 崩溃，进入退避
	h.link.waitReport(t, func(r model.KioskReport) bool { return r.BackoffUntil != nil })
	if got := h.d.ChromiumPID(); got != 0 {
		t.Fatalf("退避期间 pid 应为 0，实际 %d", got)
	}
}

func TestReportSources_合并各项(t *testing.T) {
	yes := true
	idle := &model.KioskIdleCheck{User: true}
	src := reportSources{
		Touch: func() TouchResult { return TouchResult{Known: true, Touch: yes} },
		Idle:  func() *model.KioskIdleCheck { return idle },
		PID:   func() int { return 321 },
		RSS: func(pgid int) (int64, bool) {
			if pgid != 321 {
				t.Errorf("pgid=%d", pgid)
			}
			return 4096, true
		},
	}
	var r model.KioskReport
	src.Fill(&r)
	if r.Touchscreen == nil || !*r.Touchscreen {
		t.Fatalf("touchscreen=%v", r.Touchscreen)
	}
	if r.ChromiumRSSBytes == nil || *r.ChromiumRSSBytes != 4096 {
		t.Fatalf("rss=%v", r.ChromiumRSSBytes)
	}
	if r.IdleCheck == nil || !r.IdleCheck.User {
		t.Fatalf("idle=%v", r.IdleCheck)
	}
}

func TestReportSources_未知值为null(t *testing.T) {
	src := reportSources{
		Touch: func() TouchResult { return TouchResult{} },
		Idle:  func() *model.KioskIdleCheck { return nil },
		PID:   func() int { return 0 },
		RSS:   func(int) (int64, bool) { t.Error("无 Chromium 不应读 RSS"); return 0, true },
	}
	var r model.KioskReport
	src.Fill(&r)
	if r.Touchscreen != nil || r.ChromiumRSSBytes != nil || r.IdleCheck != nil {
		t.Fatalf("应全为 nil: %+v", r)
	}
	// RSS 读不到也是 null，而不是 0
	src.PID = func() int { return 9 }
	src.RSS = func(int) (int64, bool) { return 0, false }
	r = model.KioskReport{}
	src.Fill(&r)
	if r.ChromiumRSSBytes != nil {
		t.Fatal("读不到应为 nil")
	}
}

func TestReportSources_并发安全(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	idle := newIdleFixture(t)
	runUntilCancel(t, f.w.Run)
	runUntilCancel(t, idle.checker.Run)
	src := reportSources{Touch: f.w.Result, Idle: idle.checker.Last, PID: func() int { return 1 }, RSS: func(int) (int64, bool) { return 1, true }}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				var r model.KioskReport
				src.Fill(&r)
			}
		}()
	}
	wg.Wait()
}
