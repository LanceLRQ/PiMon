package screenstate

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	// ViewportStableFor 是尺寸连续稳定多久才被采信。
	ViewportStableFor = 2 * time.Second
	// ViewportWakeIgnore 是唤醒后多久之内的上报被忽略（M0 结论：唤醒瞬间尺寸不可靠）。
	ViewportWakeIgnore = 5 * time.Second
)

// ViewportTracker 实现 viewport 采信规则：尺寸连续稳定 2 秒才采信，
// 关屏期间与唤醒后 5 秒内的上报被忽略。只处理屏幕会话的上报，来源校验由调用方负责。
type ViewportTracker struct {
	clk     clock.Clock
	onAdopt func(model.Viewport)

	mu       sync.Mutex
	screenOn bool
	wakeAt   time.Time
	gen      uint64
	pending  *model.Viewport
	current  *model.Viewport

	// settled 统计已处理完的待采信检查次数（无论是否采信），供测试同步。
	settled atomic.Int64
}

// NewViewportTracker 创建采信器；onAdopt 在新尺寸被采信后调用（不持锁），可为 nil。
func NewViewportTracker(clk clock.Clock, onAdopt func(model.Viewport)) *ViewportTracker {
	return &ViewportTracker{clk: clk, onAdopt: onAdopt, screenOn: true}
}

func (t *ViewportTracker) suppressedLocked() bool {
	if !t.screenOn {
		return true
	}
	return !t.wakeAt.IsZero() && t.clk.Now().Sub(t.wakeAt) < ViewportWakeIgnore
}

// SetScreenOn 通知屏幕开关状态：关屏会放弃待采信的尺寸；由关到开记为一次唤醒。
func (t *ViewportTracker) SetScreenOn(on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case !on:
		t.screenOn = false
		t.gen++
		t.pending = nil
	case !t.screenOn:
		t.screenOn = true
		t.wakeAt = t.clk.Now()
	}
}

// Report 处理一次上报。被忽略的上报不留痕迹；与已采信尺寸相同的上报不重复回调。
func (t *ViewportTracker) Report(v model.Viewport) {
	if v.W <= 0 || v.H <= 0 || v.DPR < 0 {
		return
	}
	if v.DPR == 0 {
		v.DPR = 1
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.suppressedLocked() {
		return
	}
	if t.current != nil && *t.current == v {
		t.gen++
		t.pending = nil
		return
	}
	if t.pending != nil && *t.pending == v {
		return // 同一尺寸重复上报不重置计时
	}
	t.gen++
	t.pending = &v
	ch := t.clk.After(ViewportStableFor)
	go func(g uint64) {
		<-ch
		t.settle(g)
	}(t.gen)
}

func (t *ViewportTracker) settle(g uint64) {
	defer t.settled.Add(1)
	t.mu.Lock()
	if g != t.gen || t.pending == nil || t.suppressedLocked() {
		t.mu.Unlock()
		return
	}
	v := *t.pending
	t.pending = nil
	t.current = &v
	t.mu.Unlock()
	if t.onAdopt != nil {
		t.onAdopt(v)
	}
}

// Current 返回已采信的尺寸；尚未采信时为 nil。
func (t *ViewportTracker) Current() *model.Viewport {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return nil
	}
	v := *t.current
	return &v
}
