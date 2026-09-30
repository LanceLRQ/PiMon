package runtime

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

const panicSecret = "zzz-panic-secret-zzz"

func TestCollectWithTimeoutRecoversPanicAndRedacts(t *testing.T) {
	src := funcSource{
		m: &manifest.Manifest{ID: "b", Timeout: time.Second},
		collect: func(context.Context, Input) (*report.Report, error) {
			panic("连接 https://x/?key=" + panicSecret + " 失败")
		},
	}
	_, err := CollectWithTimeout(context.Background(), src, Input{Secrets: map[string]string{"key": panicSecret}})
	if !errors.Is(err, ErrFailed) || errors.Is(err, ErrTimeout) {
		t.Fatalf("插件 panic 应归为 ErrFailed: %v", err)
	}
	if strings.Contains(err.Error(), panicSecret) {
		t.Fatalf("panic 文字未脱敏: %v", err)
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("错误应说明是 panic: %v", err)
	}
}

func TestCollectWithTimeoutNilReportIsFailure(t *testing.T) {
	src := funcSource{
		m:       &manifest.Manifest{ID: "b"},
		collect: func(context.Context, Input) (*report.Report, error) { return nil, nil },
	}
	if _, err := CollectWithTimeout(context.Background(), src, Input{}); !errors.Is(err, ErrFailed) {
		t.Fatalf("空报告应归为 ErrFailed: %v", err)
	}
}

func TestSchedulerPanicTextOmitsValue(t *testing.T) {
	h := newSchedHarness(t, SchedulerOptions{})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: func(context.Context) (*report.Report, error) {
		panic(panicSecret)
	}})
	r := h.next(t)
	if !errors.Is(r.Err, ErrFailed) || strings.Contains(r.Err.Error(), panicSecret) {
		t.Fatalf("任务 panic 应为 ErrFailed 且不带 panic 值: %v", r.Err)
	}
}

type panicLookuper struct{}

func (panicLookuper) Lookup(context.Context, string, string, string) ([]Candidate, error) {
	panic("lookup " + panicSecret)
}

func TestSafeLookupRecoversPanic(t *testing.T) {
	_, err := SafeLookup(context.Background(), panicLookuper{}, "city", "q", "zh")
	if !errors.Is(err, ErrFailed) || strings.Contains(err.Error(), panicSecret) {
		t.Fatalf("lookup panic 应为 ErrFailed 且不带 panic 值: %v", err)
	}
}

func TestStreamExitErrorRedactedInLog(t *testing.T) {
	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	f := newFakeStreamer(func(context.Context, int32, func(*report.Report)) error {
		return errors.New("连接 wss://x/?token=" + panicSecret + " 断开")
	})
	clk := newRecClock()
	m := NewStreamManager(context.Background(), StreamOptions{Clock: clk, Logger: log}, func(string, *report.Report) {})
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f, Input: Input{Secrets: map[string]string{"token": panicSecret}}})
	waitStart(t, f)
	clk.waitDurs(t, 1) // 已记日志并进入退避
	out := buf.String()
	if !strings.Contains(out, "Streamer") || strings.Contains(out, panicSecret) {
		t.Fatalf("退出日志应存在且已脱敏:\n%s", out)
	}
}

func TestStreamPanicRecoveredAndRestarted(t *testing.T) {
	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	f := newFakeStreamer(func(ctx context.Context, n int32, _ func(*report.Report)) error {
		if n == 1 {
			panic(panicSecret)
		}
		<-ctx.Done()
		return ctx.Err()
	})
	clk := newRecClock()
	m := NewStreamManager(context.Background(), StreamOptions{Clock: clk, Logger: log}, func(string, *report.Report) {})
	defer m.Stop()
	m.Upsert(StreamTask{ID: "s", ConfigHash: "h", Streamer: f})
	waitStart(t, f)
	d := clk.waitDurs(t, 1)
	clk.Advance(d[0])
	waitStart(t, f) // panic 后照常退避重启
	if strings.Contains(buf.String(), panicSecret) {
		t.Fatalf("panic 值不应进日志:\n%s", buf.String())
	}
}
