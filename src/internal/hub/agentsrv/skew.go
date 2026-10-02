package agentsrv

import (
	"slices"
	"time"
)

// skewSampleCount 是时钟偏移保留的最近样本数。
const skewSampleCount = 5

// skewSamples 是一个会话最近的时钟偏移样本（环形，最多 skewSampleCount 个）。调用方负责加锁。
type skewSamples struct {
	buf  [skewSampleCount]time.Duration
	n    int // 已有样本数（≤ skewSampleCount）
	next int // 下一个写入位置
}

func (s *skewSamples) add(d time.Duration) {
	s.buf[s.next] = d
	s.next = (s.next + 1) % skewSampleCount
	if s.n < skewSampleCount {
		s.n++
	}
}

// median 返回现有样本的中位数；没有样本时 ok 为假。
func (s *skewSamples) median() (time.Duration, bool) {
	if s.n == 0 {
		return 0, false
	}
	return median(s.buf[:s.n]), true
}

// median 返回 ds 的中位数（偶数个取两个中间值的平均），不修改 ds。ds 不能为空。
func median(ds []time.Duration) time.Duration {
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
