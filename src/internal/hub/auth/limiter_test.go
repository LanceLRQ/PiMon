package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLimiter_九次失败剩一次_第十次锁定(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk, 10, 15*time.Minute)
	key := KeyLogin + "1.2.3.4"
	if l.Locked(key) != 0 {
		t.Fatal("初始不应锁定")
	}
	for i := 1; i <= 9; i++ {
		if got := l.Fail(key); got != 10-i {
			t.Fatalf("第 %d 次失败剩余 = %d, 期望 %d", i, got, 10-i)
		}
	}
	if l.Locked(key) != 0 {
		t.Fatal("9 次失败不应锁定")
	}
	if got := l.Fail(key); got != 0 {
		t.Fatalf("第 10 次应返回 0, 得 %d", got)
	}
	if got := l.Locked(key); got != 15*time.Minute {
		t.Fatalf("剩余锁定 = %v", got)
	}
	clk.Advance(5 * time.Minute)
	if got := l.Locked(key); got != 10*time.Minute {
		t.Fatalf("推进 5 分钟后剩余 = %v", got)
	}
}

func TestLimiter_锁定期满后计数重新开始(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk, 10, 15*time.Minute)
	for i := 0; i < 10; i++ {
		l.Fail("k")
	}
	clk.Advance(15 * time.Minute)
	if l.Locked("k") != 0 {
		t.Fatal("期满应解锁")
	}
	if got := l.Fail("k"); got != 9 {
		t.Fatalf("解锁后首次失败剩余 = %d, 期望 9", got)
	}
}

func TestLimiter_成功清零(t *testing.T) {
	l := NewLimiter(newClock(), 10, 15*time.Minute)
	for i := 0; i < 5; i++ {
		l.Fail("k")
	}
	l.Success("k")
	if got := l.Fail("k"); got != 9 {
		t.Fatalf("成功后首次失败剩余 = %d, 期望 9", got)
	}
}

func TestLimiter_key相互独立(t *testing.T) {
	l := NewLimiter(newClock(), 2, time.Minute)
	l.Fail(KeyLogin + "a")
	l.Fail(KeyLogin + "a")
	if l.Locked(KeyLogin+"a") == 0 {
		t.Fatal("a 应锁定")
	}
	if l.Locked(KeyLogin+"b") != 0 || l.Locked(KeySetup+"a") != 0 {
		t.Fatal("其他 key 不应受影响")
	}
}

func TestLimiter_并发安全(t *testing.T) {
	l := NewLimiter(newClock(), 1000, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Fail("k")
			l.Locked("k")
			l.Success("x")
		}()
	}
	wg.Wait()
}

func TestLimiter_Fail时清理过期条目(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk, 2, time.Minute)
	for i := 0; i < 50; i++ {
		l.Fail(fmt.Sprintf("old-%d", i))
		l.Fail(fmt.Sprintf("old-%d", i)) // 触发锁定
	}
	clk.Advance(2 * time.Minute)
	l.Fail("fresh")
	l.mu.Lock()
	n := len(l.entries)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("过期条目应被清理, 剩余 %d", n)
	}
}

func TestLimiter_条目数硬上限淘汰最旧(t *testing.T) {
	clk := newClock()
	l := NewLimiter(clk, 10, time.Minute)
	for i := 0; i < maxLimiterEntries+10; i++ {
		l.Fail(fmt.Sprintf("k-%d", i))
		clk.Advance(time.Millisecond)
	}
	l.mu.Lock()
	n := len(l.entries)
	_, oldest := l.entries["k-0"]
	_, newest := l.entries[fmt.Sprintf("k-%d", maxLimiterEntries+9)]
	l.mu.Unlock()
	if n != maxLimiterEntries {
		t.Fatalf("条目数 = %d, 期望 %d", n, maxLimiterEntries)
	}
	if oldest || !newest {
		t.Fatalf("应淘汰最旧保留最新: oldest=%v newest=%v", oldest, newest)
	}
}
