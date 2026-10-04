package cmdexec

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Call 是 Fake 记录的一次调用。
type Call struct {
	Name string
	Args []string
}

type fakeEntry struct {
	res Result
	err error
}

// Fake 是测试替身：按命令名与参数返回预置结果，并记录全部调用。
type Fake struct {
	mu      sync.Mutex
	entries map[string]fakeEntry
	calls   []Call
}

// NewFake 创建空替身；未预置的命令返回 error。
func NewFake() *Fake { return &Fake{entries: map[string]fakeEntry{}} }

func fakeKey(name string, args []string) string {
	return name + "\x00" + strings.Join(args, "\x00")
}

// Set 预置某条命令（名称加完整参数）的结果或错误。
func (f *Fake) Set(res Result, err error, name string, args ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[fakeKey(name, args)] = fakeEntry{res: res, err: err}
}

// Calls 返回按顺序记录的调用副本。
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Run 实现 Runner。
func (f *Fake) Run(_ context.Context, name string, args ...string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Name: name, Args: append([]string(nil), args...)})
	e, ok := f.entries[fakeKey(name, args)]
	if !ok {
		return Result{}, fmt.Errorf("未预置的命令: %s %s", name, strings.Join(args, " "))
	}
	return e.res, e.err
}
