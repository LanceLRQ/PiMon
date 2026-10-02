package kiosk

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestCommand_环境变量缺失时启动即报错(t *testing.T) {
	var out bytes.Buffer
	err := Command(context.Background(), nil, &out, env(map[string]string{"HOME": t.TempDir()}))
	if err == nil {
		t.Fatal("缺少 XDG_RUNTIME_DIR/WAYLAND_DISPLAY 应报错")
	}
	var ue UsageError
	if errors.As(err, &ue) {
		t.Fatalf("环境问题不是用法错误: %v", err)
	}
}

func TestCommand_未知参数是用法错误(t *testing.T) {
	var out bytes.Buffer
	err := Command(context.Background(), []string{"--nope"}, &out, env(nil))
	var ue UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("应为 UsageError，实际 %v", err)
	}
	err = Command(context.Background(), []string{"--log-level", "loud"}, &out, env(nil))
	if !errors.As(err, &ue) {
		t.Fatalf("非法日志级别应为 UsageError，实际 %v", err)
	}
	err = Command(context.Background(), []string{"extra"}, &out, env(nil))
	if !errors.As(err, &ue) {
		t.Fatalf("多余位置参数应为 UsageError，实际 %v", err)
	}
}

func TestDefaultPaths(t *testing.T) {
	p, err := DefaultPaths(env(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	if p.LockPath != "/home/u/.local/state/pimon/kiosk/kiosk.lock" || p.ProfileDir != "/home/u/.local/state/pimon/kiosk/chrome-profile" {
		t.Fatalf("%+v", p)
	}
	p, err = DefaultPaths(env(map[string]string{"HOME": "/home/u", "XDG_STATE_HOME": "/x/state"}))
	if err != nil || p.LockPath != "/x/state/pimon/kiosk/kiosk.lock" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := DefaultPaths(env(nil)); err == nil {
		t.Fatal("HOME 与 XDG_STATE_HOME 都没有应报错")
	}
}
