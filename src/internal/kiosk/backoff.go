package kiosk

import "time"

const (
	backoffInitial = time.Second
	backoffMax     = 60 * time.Second
	// stableRun 是 Chromium 连续运行多久后视为稳定、退避清零。
	stableRun = 5 * time.Minute
)

// backoff 是翻倍退避：1s 起，上限 60s。
type backoff struct {
	cur time.Duration
}

// next 返回本次应等待的时长并推进到下一档。
func (b *backoff) next() time.Duration {
	switch {
	case b.cur == 0:
		b.cur = backoffInitial
	case b.cur*2 > backoffMax:
		b.cur = backoffMax
	default:
		b.cur *= 2
	}
	return b.cur
}

func (b *backoff) reset() { b.cur = 0 }
