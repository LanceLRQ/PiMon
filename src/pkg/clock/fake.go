package clock

import (
	"sync"
	"time"
)

type waiter struct {
	deadline time.Time
	ch       chan time.Time
}

// Fake 是测试用假时钟，时间只随 Advance 前进，并发安全。
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

// NewFake 创建起点为 start 的假时钟。
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

// Now 返回假时钟的当前时间。
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After 返回带 1 个缓冲的 channel；d <= 0 时立即触发，否则等 Advance 越过到期时刻。
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{deadline: f.now.Add(d), ch: ch})
	return ch
}

// Advance 把时间推进 d，并触发所有到期（deadline <= 新的当前时间）的等待者。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	remaining := f.waiters[:0]
	for _, w := range f.waiters {
		if w.deadline.After(f.now) {
			remaining = append(remaining, w)
			continue
		}
		w.ch <- w.deadline
	}
	f.waiters = remaining
}

// Waiters 返回尚未触发的等待者数量。
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}
