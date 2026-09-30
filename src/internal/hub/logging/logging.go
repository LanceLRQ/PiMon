// Package logging 提供中枢的 slog 文本日志，每条日志附带单调时长 mono。
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
	base := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(&monoHandler{inner: base, start: time.Now()})
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
