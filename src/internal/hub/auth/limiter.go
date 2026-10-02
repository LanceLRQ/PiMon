package auth

import (
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// 限流 key 前缀，后面拼客户端 IP。
const (
	KeyLogin  = "login:"
	KeySetup  = "setup:"
	KeyScreen = "screen:"
)

// maxLimiterEntries 是内存中限流条目的硬上限，满员时淘汰最旧条目。
const maxLimiterEntries = 4096

type limitEntry struct {
	created     time.Time
	fails       int
	lockedUntil time.Time
}

// Limiter 按 key 统计连续失败次数，达到上限后锁定一段时间。状态只在内存中，重启后清零。
type Limiter struct {
	clk  clock.Clock
	max  int
	lock time.Duration

	mu      sync.Mutex
	entries map[string]*limitEntry
}

// NewLimiter 创建限流器：连续失败 max 次后锁定 lock。
func NewLimiter(clk clock.Clock, max int, lock time.Duration) *Limiter {
	return &Limiter{clk: clk, max: max, lock: lock, entries: make(map[string]*limitEntry)}
}

// Now 返回限流器使用的当前时间，用于把剩余锁定时长换算成到期时刻。
func (l *Limiter) Now() time.Time { return l.clk.Now() }

// Locked 返回 key 剩余的锁定时长，未锁定返回 0。锁定期满时清除该 key 的计数。
func (l *Limiter) Locked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lockedLocked(key)
}

func (l *Limiter) lockedLocked(key string) time.Duration {
	e, ok := l.entries[key]
	if !ok || e.lockedUntil.IsZero() {
		return 0
	}
	if remain := e.lockedUntil.Sub(l.clk.Now()); remain > 0 {
		return remain
	}
	delete(l.entries, key)
	return 0
}

// Fail 记一次失败，返回剩余可尝试次数；本次失败触发锁定或已处于锁定中时返回 0。
func (l *Limiter) Fail(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lockedLocked(key) > 0 {
		return 0
	}
	e, ok := l.entries[key]
	if !ok {
		l.sweepLocked()
		e = &limitEntry{created: l.clk.Now()}
		l.entries[key] = e
	}
	e.fails++
	if e.fails >= l.max {
		e.lockedUntil = l.clk.Now().Add(l.lock)
		return 0
	}
	return l.max - e.fails
}

// sweepLocked 清除锁定已到期的条目；清理后仍满员则淘汰最旧的一条。新增条目前调用。
func (l *Limiter) sweepLocked() {
	now := l.clk.Now()
	for k, e := range l.entries {
		if !e.lockedUntil.IsZero() && !e.lockedUntil.After(now) {
			delete(l.entries, k)
		}
	}
	for len(l.entries) >= maxLimiterEntries {
		var oldestKey string
		var oldest time.Time
		first := true
		for k, e := range l.entries {
			if first || e.created.Before(oldest) {
				oldestKey, oldest, first = k, e.created, false
			}
		}
		delete(l.entries, oldestKey)
	}
}

// Success 清零 key 的失败计数。
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
