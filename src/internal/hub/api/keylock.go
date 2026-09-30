package api

import (
	"context"
	"sync"
)

// keyedMutex 按 key 提供互斥锁。条目在无人持有或等待时即删除，map 不会无限增长。
// 用它把同一限流 key 的"查锁定→校验→记失败"串行化，堵住并发绕过失败次数上限。
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyEntry
}

type keyEntry struct {
	mu   sync.Mutex
	refs int
}

func newKeyedMutex() *keyedMutex { return &keyedMutex{m: make(map[string]*keyEntry)} }

// lock 获取 key 的锁，返回释放函数。
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	e, ok := k.m[key]
	if !ok {
		e = &keyEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

// gate 是并发信号量，限制同时进行的重计算数量。
type gate struct{ ch chan struct{} }

func newGate(n int) *gate { return &gate{ch: make(chan struct{}, n)} }

func (g *gate) acquire(ctx context.Context) error {
	select {
	case g.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *gate) release() { <-g.ch }

// hash 在信号量保护下计算密码哈希。
func (s *server) hash(ctx context.Context, password string) (string, error) {
	if err := s.gate.acquire(ctx); err != nil {
		return "", err
	}
	defer s.gate.release()
	return s.Hasher.Hash(password)
}

// verify 在信号量保护下校验密码。
func (s *server) verify(ctx context.Context, encoded, password string) (bool, error) {
	if err := s.gate.acquire(ctx); err != nil {
		return false, err
	}
	defer s.gate.release()
	return s.Hasher.Verify(encoded, password)
}
