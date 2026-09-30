package clock

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func fired(ch <-chan time.Time) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestFakeNowAdvance(t *testing.T) {
	f := NewFake(t0)
	if !f.Now().Equal(t0) {
		t.Fatalf("初始时间不对: %v", f.Now())
	}
	f.Advance(time.Minute)
	if !f.Now().Equal(t0.Add(time.Minute)) {
		t.Fatalf("推进后时间不对: %v", f.Now())
	}
}

func TestFakeAfterNotFiredBeforeDeadline(t *testing.T) {
	f := NewFake(t0)
	ch := f.After(10 * time.Second)
	f.Advance(9 * time.Second)
	if fired(ch) {
		t.Fatal("未到期不应触发")
	}
	if f.Waiters() != 1 {
		t.Fatalf("Waiters = %d, 期望 1", f.Waiters())
	}
}

func TestFakeAfterFiresExactlyAtDeadline(t *testing.T) {
	f := NewFake(t0)
	ch := f.After(10 * time.Second)
	f.Advance(10 * time.Second)
	select {
	case got := <-ch:
		if !got.Equal(t0.Add(10 * time.Second)) {
			t.Fatalf("触发时间 = %v", got)
		}
	default:
		t.Fatal("恰好到期应触发")
	}
	if f.Waiters() != 0 {
		t.Fatalf("Waiters = %d, 期望 0", f.Waiters())
	}
}

func TestFakeAfterOnlyDueWaitersFire(t *testing.T) {
	f := NewFake(t0)
	a := f.After(5 * time.Second)
	b := f.After(10 * time.Second)
	c := f.After(20 * time.Second)
	if f.Waiters() != 3 {
		t.Fatalf("Waiters = %d, 期望 3", f.Waiters())
	}
	f.Advance(10 * time.Second)
	if !fired(a) || !fired(b) {
		t.Fatal("到期的等待者应触发")
	}
	if fired(c) {
		t.Fatal("未到期的等待者不应触发")
	}
	if f.Waiters() != 1 {
		t.Fatalf("Waiters = %d, 期望 1", f.Waiters())
	}
}

func TestFakeAfterNonPositiveFiresImmediately(t *testing.T) {
	f := NewFake(t0)
	for _, d := range []time.Duration{0, -time.Second} {
		if !fired(f.After(d)) {
			t.Fatalf("d=%v 应立即触发", d)
		}
	}
	if f.Waiters() != 0 {
		t.Fatalf("Waiters = %d, 期望 0", f.Waiters())
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := NewFake(t0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); f.After(time.Second); _ = f.Waiters(); _ = f.Now() }()
		go func() { defer wg.Done(); f.Advance(time.Second) }()
	}
	wg.Wait()
}

func TestRealClock(t *testing.T) {
	var c Clock = Real{}
	before := time.Now()
	if c.Now().Before(before) {
		t.Fatal("真实时钟 Now 不应早于调用前")
	}
	select {
	case <-c.After(time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("真实时钟 After 未触发")
	}
}
