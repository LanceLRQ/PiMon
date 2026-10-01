package logging

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestRingKeepsNewestWithinCapacity(t *testing.T) {
	r := NewRing(3)
	lg := slog.New(r.Handler(slog.LevelDebug))
	for i := 1; i <= 5; i++ {
		lg.Info(fmt.Sprintf("m%d", i))
	}
	got := r.Entries(slog.LevelDebug, 100)
	if len(got) != 3 || got[0].Message != "m3" || got[2].Message != "m5" {
		t.Fatalf("应只保留最近 3 条且按时间正序: %+v", got)
	}
	if r.Capacity() != 3 {
		t.Fatalf("容量 = %d", r.Capacity())
	}
}

func TestRingLevelFilterAndLimit(t *testing.T) {
	r := NewRing(10)
	lg := slog.New(r.Handler(slog.LevelDebug))
	lg.Debug("d")
	lg.Info("i1")
	lg.Warn("w1")
	lg.Error("e1")
	lg.Info("i2")
	if got := r.Entries(slog.LevelWarn, 100); len(got) != 2 || got[0].Message != "w1" || got[1].Message != "e1" {
		t.Fatalf("warn 及以上应有 2 条: %+v", got)
	}
	got := r.Entries(slog.LevelInfo, 2)
	if len(got) != 2 || got[0].Message != "e1" || got[1].Message != "i2" {
		t.Fatalf("limit 取符合条件的最近 2 条: %+v", got)
	}
	if got := r.Entries(slog.LevelInfo, 0); len(got) != 0 {
		t.Fatalf("limit=0 应为空: %+v", got)
	}
}

func TestRingHonorsHandlerLevel(t *testing.T) {
	r := NewRing(10)
	lg := slog.New(r.Handler(slog.LevelWarn))
	lg.Info("skip")
	lg.Warn("keep")
	if got := r.Entries(slog.LevelDebug, 10); len(got) != 1 || got[0].Message != "keep" {
		t.Fatalf("handler 级别以下不应入缓冲: %+v", got)
	}
}

func TestRingAttrsGroupsAndRedaction(t *testing.T) {
	r := NewRing(10)
	lg := slog.New(r.Handler(slog.LevelDebug)).With("svc", "hub").WithGroup("g")
	lg.Info("x", "a", 1, "note", "has space", "password", "p@ss", "setup_code", "1234", "Token", "t0k")
	got := r.Entries(slog.LevelDebug, 1)[0].Attrs
	for _, want := range []string{"svc=hub", "g.a=1", `g.note="has space"`, "g.password=***", "g.setup_code=***", "g.Token=***"} {
		if !strings.Contains(got, want) {
			t.Errorf("attrs 缺 %q: %s", want, got)
		}
	}
	for _, leak := range []string{"p@ss", "1234", "t0k"} {
		if strings.Contains(got, leak) {
			t.Errorf("attrs 泄露 %q: %s", leak, got)
		}
	}
}

func TestNewWithRingWritesBothAndKeepsMono(t *testing.T) {
	var buf bytes.Buffer
	r := NewRing(10)
	NewWithRing(&buf, slog.LevelInfo, r).Info("hello", "k", "v")
	if !strings.Contains(buf.String(), "msg=hello") || !strings.Contains(buf.String(), "mono=") {
		t.Fatalf("文本输出不对: %q", buf.String())
	}
	e := r.Entries(slog.LevelDebug, 10)
	if len(e) != 1 || e[0].Message != "hello" || !strings.Contains(e[0].Attrs, "k=v") || !strings.Contains(e[0].Attrs, "mono=") {
		t.Fatalf("缓冲内容不对: %+v", e)
	}
}

func TestRingConcurrent(t *testing.T) {
	r := NewRing(50)
	lg := slog.New(r.Handler(slog.LevelDebug))
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func() {
			for i := 0; i < 200; i++ {
				lg.Info("x")
				_ = r.Entries(slog.LevelDebug, 10)
			}
			done <- struct{}{}
		}()
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	if n := len(r.Entries(slog.LevelDebug, 1000)); n != 50 {
		t.Fatalf("n = %d", n)
	}
}
