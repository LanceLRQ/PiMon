package pluginscan

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatch防抖后回调一次(t *testing.T) {
	root := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done, err := Watch(ctx, WatchConfig{Dir: root, Clock: clk, Debounce: time.Second}, func() { calls.Add(1) })
	must(t, err)

	put(t, root, "late", manifestYAML("late", "exec"))
	waitFor(t, "防抖计时器注册", func() bool { return clk.Waiters() > 0 })
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("防抖期内不应回调")
	}
	clk.Advance(time.Second)
	waitFor(t, "回调", func() bool { return calls.Load() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("回调次数 %d，期望 1", n)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 结束后监视未停止")
	}
}

func TestWatch目录不存在返回错误(t *testing.T) {
	_, err := Watch(context.Background(), WatchConfig{Dir: t.TempDir() + "/nope", Clock: clock.Real{}}, func() {})
	if err == nil {
		t.Fatal("期望错误")
	}
}
