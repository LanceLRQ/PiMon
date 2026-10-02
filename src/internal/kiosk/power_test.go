package kiosk

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
)

type runCall struct {
	name string
	args []string
}

type fakeRunner struct {
	mu     sync.Mutex
	calls  []runCall
	err    error
	notify chan runCall // 非 nil 时每次调用都投递一份
}

func (r *fakeRunner) run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := runCall{name, append([]string(nil), args...)}
	r.calls = append(r.calls, c)
	if r.notify != nil {
		r.notify <- c
	}
	return r.err
}

func (r *fakeRunner) snapshot() []runCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runCall(nil), r.calls...)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestPower_off与on调用对应的wlopm命令(t *testing.T) {
	r := &fakeRunner{}
	p := NewPower("/usr/bin/wlopm", r.run, quietLogger())
	p.Apply("off")
	p.Apply("on")
	want := []runCall{
		{"/usr/bin/wlopm", []string{"--off", "*"}},
		{"/usr/bin/wlopm", []string{"--on", "*"}},
	}
	if got := r.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestPower_非off的模式一律按亮屏处理(t *testing.T) {
	r := &fakeRunner{}
	p := NewPower("wlopm", r.run, quietLogger())
	p.Apply("")
	if got := r.snapshot(); len(got) != 1 || got[0].args[0] != "--on" {
		t.Fatalf("got %v", got)
	}
}

func TestPower_状态未变不重复执行(t *testing.T) {
	r := &fakeRunner{}
	p := NewPower("wlopm", r.run, quietLogger())
	p.Apply("off")
	p.Apply("off")
	p.Apply("off")
	if n := len(r.snapshot()); n != 1 {
		t.Fatalf("应只执行 1 次，实际 %d", n)
	}
}

func TestPower_Invalidate后同状态再执行一次(t *testing.T) {
	r := &fakeRunner{}
	p := NewPower("wlopm", r.run, quietLogger())
	p.Apply("on")
	p.Invalidate()
	p.Apply("on")
	p.Apply("on")
	if n := len(r.snapshot()); n != 2 {
		t.Fatalf("应执行 2 次，实际 %d", n)
	}
}

func TestPower_命令失败不记为已应用从而下次重试(t *testing.T) {
	r := &fakeRunner{err: errors.New("wlopm 失败")}
	p := NewPower("wlopm", r.run, quietLogger())
	p.Apply("off")
	r.mu.Lock()
	r.err = nil
	r.mu.Unlock()
	p.Apply("off")
	p.Apply("off")
	if n := len(r.snapshot()); n != 2 {
		t.Fatalf("失败后应重试 1 次，共 2 次，实际 %d", n)
	}
}

func TestExecRunner_星号参数不经shell原样传递(t *testing.T) {
	// 用 /bin/sh -c 'test "$1" = "*"' 验证参数就是字面量 *；若经过 shell 展开会变成文件名列表。
	if err := ExecRunner(context.Background(), "/bin/sh", "-c", `test "$1" = "*"`, "sh", "*"); err != nil {
		t.Fatal(err)
	}
	if err := ExecRunner(context.Background(), "/nonexistent/wlopm", "--off", "*"); err == nil {
		t.Fatal("可执行文件不存在应报错")
	}
}
