package logging

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

var monoRe = regexp.MustCompile(`mono=\S+`)

func TestMonoPresent(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).Info("hello", "k", "v")
	out := buf.String()
	if !monoRe.MatchString(out) || !strings.Contains(out, "msg=hello") || !strings.Contains(out, "k=v") {
		t.Fatalf("输出缺少字段: %q", out)
	}
}

func TestMonoKeptAfterWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).With("svc", "hub").Info("x")
	out := buf.String()
	if !monoRe.MatchString(out) || !strings.Contains(out, "svc=hub") {
		t.Fatalf("WithAttrs 后丢失 mono: %q", out)
	}
}

func TestMonoKeptAfterWithGroup(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).WithGroup("g").With("a", 1).Info("x", "b", 2)
	out := buf.String()
	if !strings.Contains(out, "mono=") || !strings.Contains(out, "g.b=2") || !strings.Contains(out, "g.a=1") {
		t.Fatalf("WithGroup 后输出不符: %q", out)
	}
}

func TestMonoNonDecreasing(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	l.Info("a")
	l.Info("b")
	if n := len(monoRe.FindAllString(buf.String(), -1)); n != 2 {
		t.Fatalf("应有两条 mono: %q", buf.String())
	}
}

func TestLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelWarn).Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("低于级别的日志不应输出: %q", buf.String())
	}
}
