package api

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// 同一 IP 并发猜密码：进入 argon2 校验的次数不得超过失败上限，其余必须看到锁定。
func TestLogin_ConcurrentSameIPBounded(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.hasher.delay = 20 * time.Millisecond
	e.hasher.verifies.Store(0)

	const n = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := e.do(e.newClient(), "POST", "/api/login", map[string]any{"password": "wrong-password"})
			mu.Lock()
			counts[resp.StatusCode]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if got := e.hasher.verifies.Load(); got > 10 {
		t.Fatalf("进入密码校验 %d 次，超过上限 10", got)
	}
	if counts[http.StatusUnauthorized] != 9 || counts[http.StatusTooManyRequests] != n-9 {
		t.Fatalf("状态码分布 = %v，期望 9 个 401、%d 个 429", counts, n-9)
	}
}

// 不同客户端同时校验时，argon2 并发数不得超过全局上限。
func TestLogin_ArgonConcurrencyCapped(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.trustLoopback()
	e.hasher.delay = 30 * time.Millisecond

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.do(e.newClient(), "POST", "/api/login", map[string]any{"password": "wrong-password"},
				withHeader("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1)))
		}()
	}
	wg.Wait()

	if got := e.hasher.peak.Load(); got > argonConcurrency || got < 1 {
		t.Fatalf("argon2 峰值并发 = %d，期望 1..%d", got, argonConcurrency)
	}
	if got := e.hasher.verifies.Load(); got != n {
		t.Fatalf("校验次数 = %d，期望 %d", got, n)
	}
}

func TestKeyedMutex_EntriesReleased(t *testing.T) {
	k := newKeyedMutex()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := k.lock(fmt.Sprintf("k%d", i%5))
			unlock()
		}()
	}
	wg.Wait()
	if len(k.m) != 0 {
		t.Fatalf("锁条目未回收: %d", len(k.m))
	}
}
