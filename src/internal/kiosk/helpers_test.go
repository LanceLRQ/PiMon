package kiosk

import (
	"context"
	"testing"
	"time"
)

// runUntilCancel 在 goroutine 里跑 run，返回的函数取消 ctx 并等 run 返回。
func runUntilCancel(t *testing.T, run func(context.Context)) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { run(ctx); close(done) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(waitLimit):
			t.Fatal("等待 Run 返回超时")
		}
	}
	t.Cleanup(stop)
	return stop
}
