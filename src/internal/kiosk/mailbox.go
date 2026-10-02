package kiosk

import (
	"context"
	"sync"
)

// mailbox 是容量 1、只保留最新值的信箱：生产者 put 永不阻塞，消费者慢时中间值被覆盖。
type mailbox[T any] struct {
	mu    sync.Mutex
	val   T
	has   bool
	ready chan struct{}
}

func newMailbox[T any]() *mailbox[T] { return &mailbox[T]{ready: make(chan struct{}, 1)} }

func (m *mailbox[T]) put(v T) {
	m.mu.Lock()
	m.val, m.has = v, true
	m.mu.Unlock()
	select {
	case m.ready <- struct{}{}:
	default:
	}
}

func (m *mailbox[T]) take() (T, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.val, m.has
	var zero T
	m.val, m.has = zero, false
	return v, ok
}

// run 在当前 goroutine 里把信箱里的最新值依次交给 fn，直到 ctx 结束。
// fn 可以阻塞，这期间新到的值只保留最新一个。
func (m *mailbox[T]) run(ctx context.Context, fn func(T)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.ready:
			if v, ok := m.take(); ok {
				fn(v)
			}
		}
	}
}
