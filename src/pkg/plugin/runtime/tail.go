package runtime

import "sync"

// tailBuffer 只保留最近写入的 max 字节，用于截取 stderr 尾部。并发安全。
type tailBuffer struct {
	mu        sync.Mutex
	max       int
	buf       []byte
	truncated bool
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if n >= b.max {
		if n > b.max || len(b.buf) > 0 {
			b.truncated = true
		}
		b.buf = append(b.buf[:0], p[n-b.max:]...)
		return n, nil
	}
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.truncated = true
		copy(b.buf, b.buf[over:])
		b.buf = b.buf[:b.max]
	}
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Truncated 表示是否丢弃过更早的内容。
func (b *tailBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
