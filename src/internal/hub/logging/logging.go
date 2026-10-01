// Package logging 提供中枢的 slog 文本日志（每条附带单调时长 mono）与供管理界面查看的内存环形缓冲。
package logging

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// New 创建输出到 w 的文本日志；mono 为自本函数调用起的单调时长，
// 不受系统墙钟调整影响，便于在对时前后对齐日志顺序。
// 若经 WithGroup 分组，mono 会落在分组内（前缀为组名）。
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	return NewWithRing(w, level, nil)
}

// NewWithRing 在 New 的基础上，把同样的日志（含 mono）再写入内存环形缓冲；ring 为 nil 时等同于 New。
func NewWithRing(w io.Writer, level slog.Leveler, ring *Ring) *slog.Logger {
	var inner slog.Handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	if ring != nil {
		inner = tee{inner, ring.Handler(level)}
	}
	return slog.New(&monoHandler{inner: inner, start: time.Now()})
}

// tee 把每条日志分发给多个 Handler。
type tee []slog.Handler

func (t tee) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range t {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (t tee) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range t {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (t tee) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(tee, len(t))
	for i, h := range t {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (t tee) WithGroup(name string) slog.Handler {
	out := make(tee, len(t))
	for i, h := range t {
		out[i] = h.WithGroup(name)
	}
	return out
}

type monoHandler struct {
	inner slog.Handler
	start time.Time
}

func (h *monoHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *monoHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.Duration("mono", time.Since(h.start)))
	return h.inner.Handle(ctx, r)
}

func (h *monoHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &monoHandler{inner: h.inner.WithAttrs(attrs), start: h.start}
}

func (h *monoHandler) WithGroup(name string) slog.Handler {
	return &monoHandler{inner: h.inner.WithGroup(name), start: h.start}
}
