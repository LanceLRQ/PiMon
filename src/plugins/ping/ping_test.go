package ping

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

type fakePinger struct {
	res   Result
	err   error
	calls []string
	count int
}

func (f *fakePinger) Ping(_ context.Context, host string, count int, _ time.Duration) (Result, error) {
	f.calls = append(f.calls, host)
	f.count = count
	return f.res, f.err
}

func collect(t *testing.T, pg Pinger, cfg map[string]any) (*report.Report, error) {
	t.Helper()
	p := &plugin{m: mustManifest(), pinger: pg}
	return p.Collect(context.Background(), runtime.Input{Config: cfg, Clock: clock.NewFake(time.Unix(1_800_000_000, 0))})
}

func TestRegistered(t *testing.T) {
	if _, ok := runtime.Builtin("ping"); !ok {
		t.Fatal("ping 应在 init 中注册")
	}
}

func TestLossAndAverage(t *testing.T) {
	f := &fakePinger{res: Result{Sent: 4, RTTs: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}}}
	rep, err := collect(t, f, map[string]any{"host": "example.test", "count": 4.0})
	if err != nil {
		t.Fatal(err)
	}
	if f.count != 4 || f.calls[0] != "example.test" {
		t.Fatalf("参数未传入: %+v", f)
	}
	if rep.Status != report.StatusWarning {
		t.Fatalf("有丢包应为 warning: %s", rep.Status)
	}
	if v := *rep.Find("loss").Value; v != 25 {
		t.Fatalf("丢包率应为 25: %v", v)
	}
	if v := *rep.Find("latency").Value; v != 20 {
		t.Fatalf("平均延迟应为 20ms: %v", v)
	}
	assertValid(t, rep)
}

func TestAllReceivedIsOK(t *testing.T) {
	f := &fakePinger{res: Result{Sent: 2, RTTs: []time.Duration{time.Millisecond, time.Millisecond}}}
	rep, err := collect(t, f, map[string]any{"host": "h"})
	if err != nil || rep.Status != report.StatusOK || *rep.Find("loss").Value != 0 {
		t.Fatalf("全部收到应 ok 且丢包 0: %+v %v", rep, err)
	}
	assertValid(t, rep)
}

func TestAllLostIsCriticalWithoutLatency(t *testing.T) {
	f := &fakePinger{res: Result{Sent: 3}}
	rep, err := collect(t, f, map[string]any{"host": "h", "count": 3.0})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusCritical || rep.Find("latency") != nil {
		t.Fatalf("全丢应 critical 且无延迟: %+v", rep)
	}
	if v := *rep.Find("loss").Value; v != 100 {
		t.Fatalf("丢包率应为 100: %v", v)
	}
	assertValid(t, rep)
}

func TestPermissionErrorSuggestsTCPCheck(t *testing.T) {
	_, err := collect(t, &fakePinger{err: ErrPermission}, map[string]any{"host": "h"})
	if err == nil || !strings.Contains(err.Error(), "tcp-check") {
		t.Fatalf("权限不足应提示 tcp-check: %v", err)
	}
}

func TestResolveFailureIsCritical(t *testing.T) {
	rep, err := collect(t, &fakePinger{err: ErrResolve}, map[string]any{"host": "nope.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusCritical || rep.Find("loss") != nil || rep.Find("latency") != nil {
		t.Fatalf("解析失败应 critical: %+v", rep)
	}
	assertValid(t, rep)
}

func TestUnknownErrorAndCtx(t *testing.T) {
	boom := errors.New("boom")
	if _, err := collect(t, &fakePinger{err: boom}, map[string]any{"host": "h"}); !errors.Is(err, boom) {
		t.Fatalf("其它错误应原样返回: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &plugin{m: mustManifest(), pinger: &fakePinger{err: ErrResolve}}
	if _, err := p.Collect(ctx, runtime.Input{Config: map[string]any{"host": "h"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("上层 ctx 结束应返回 ctx 错误: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	f := &fakePinger{}
	for name, c := range map[string]map[string]any{
		"缺 host":   {},
		"次数过多":     {"host": "h", "count": 11.0},
		"次数为 0":    {"host": "h", "count": 0.0},
		"host 全空白": {"host": "  "},
	} {
		if _, err := collect(t, f, c); err == nil {
			t.Errorf("%s 应返回错误", name)
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("配置错误时不应发包")
	}
}

func TestConfigBoundsBelowRunTimeout(t *testing.T) {
	m := mustManifest()
	var count, timeout float64
	for _, f := range m.ConfigSchema {
		switch f.Key {
		case "count":
			count = *f.Max
		case "timeout":
			timeout = *f.Max
		}
	}
	if count*timeout >= m.Timeout.Seconds() {
		t.Fatalf("最坏耗时 %v 秒必须小于运行超时 %v", count*timeout, m.Timeout)
	}
}

func assertValid(t *testing.T, rep *report.Report) {
	t.Helper()
	if err := rep.Validate(nil); err != nil {
		t.Fatalf("报告应通过 Validate: %v", err)
	}
	for _, it := range rep.Items {
		ok := false
		for _, o := range mustManifest().Outputs {
			if o.Key == it.Key && o.Type == it.Type {
				ok = true
			}
		}
		if !ok {
			t.Errorf("键 %s（%s）不在 manifest outputs 内", it.Key, it.Type)
		}
	}
}

// 真实 ICMP 回环：无权限（CI、未放开 ping_group_range）时跳过。
func TestRealLoopback(t *testing.T) {
	res, err := NewICMPPinger().Ping(context.Background(), "127.0.0.1", 2, time.Second)
	if errors.Is(err, ErrPermission) {
		t.Skipf("无非特权 ICMP 权限: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 2 || len(res.RTTs) == 0 {
		t.Fatalf("回环应收到应答: %+v", res)
	}
}
