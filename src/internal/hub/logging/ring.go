package logging

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Entry 是环形缓冲里的一条日志。
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	// Attrs 是 key=value 空格分隔的属性文本（含分组前缀），敏感键的值已替换为 ***。
	Attrs string
}

// Ring 在内存里保留最近 N 条日志，供管理界面查看；容量满后覆盖最旧的一条。
type Ring struct {
	mu   sync.Mutex
	buf  []Entry
	next int
	size int
}

// NewRing 创建容量为 capacity 的环形缓冲；capacity 小于 1 时按 1 处理。
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Entry, capacity)}
}

// Capacity 返回缓冲容量。
func (r *Ring) Capacity() int { return len(r.buf) }

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.size < len(r.buf) {
		r.size++
	}
}

// Entries 返回级别不低于 min 的最近 limit 条日志，按时间正序。
func (r *Ring) Entries(min slog.Level, limit int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Entry{}
	// 从最新往回找，取够 limit 条后反转为正序。
	for i := 0; i < r.size && len(out) < limit; i++ {
		idx := (r.next - 1 - i + len(r.buf)) % len(r.buf)
		if e := r.buf[idx]; e.Level >= min {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Handler 返回把日志写入缓冲的 slog.Handler；低于 level 的日志不入缓冲。
func (r *Ring) Handler(level slog.Leveler) slog.Handler {
	return &ringHandler{ring: r, level: level}
}

type ringHandler struct {
	ring  *Ring
	level slog.Leveler
	// group 是当前分组前缀（形如 "a.b."），pre 是 WithAttrs 预先格式化的属性。
	group string
	pre   string
}

func (h *ringHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *ringHandler) Handle(_ context.Context, rec slog.Record) error {
	var sb strings.Builder
	sb.WriteString(h.pre)
	rec.Attrs(func(a slog.Attr) bool {
		appendAttr(&sb, h.group, a)
		return true
	})
	h.ring.add(Entry{Time: rec.Time, Level: rec.Level, Message: rec.Message, Attrs: strings.TrimPrefix(sb.String(), " ")})
	return nil
}

func (h *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var sb strings.Builder
	sb.WriteString(h.pre)
	for _, a := range attrs {
		appendAttr(&sb, h.group, a)
	}
	n := *h
	n.pre = sb.String()
	return &n
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.group = h.group + name + "."
	return &n
}

// sensitiveKeys 命中（不区分大小写、子串匹配）的属性键，其值不进入缓冲。
var sensitiveKeys = []string{"password", "passwd", "secret", "token", "setup_code", "authorization", "cookie", "credential", "api_key", "apikey"}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func appendAttr(sb *strings.Builder, group string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		prefix := group
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, ga := range a.Value.Group() {
			appendAttr(sb, prefix, ga)
		}
		return
	}
	sb.WriteByte(' ')
	sb.WriteString(group + a.Key)
	sb.WriteByte('=')
	if isSensitive(a.Key) {
		sb.WriteString("***")
		return
	}
	v := a.Value.String()
	if v == "" || strings.ContainsAny(v, " \t\r\n\"=") {
		v = strconv.Quote(v)
	}
	sb.WriteString(v)
}
