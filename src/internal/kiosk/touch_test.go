package kiosk

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func inputEvent(typ, code uint16, value int32) []byte {
	buf := make([]byte, inputEventSize)
	off := inputEventSize - 8
	binary.NativeEndian.PutUint16(buf[off:], typ)
	binary.NativeEndian.PutUint16(buf[off+2:], code)
	binary.NativeEndian.PutUint32(buf[off+4:], uint32(value))
	return buf
}

var (
	evTouchDown = inputEvent(evKey, btnTouch, 1)
	evTouchUp   = inputEvent(evKey, btnTouch, 0)
	evAbs       = inputEvent(evAbsolute, 0x35, 100)
	evSyn       = inputEvent(0, 0, 0)
)

func TestIsTouchEvent(t *testing.T) {
	cases := []struct {
		name string
		ev   []byte
		want bool
	}{
		{"BTN_TOUCH 按下", evTouchDown, true},
		{"BTN_TOUCH 抬起", evTouchUp, false},
		{"其他按键按下", inputEvent(evKey, 0x110, 1), false},
		{"EV_ABS 坐标", evAbs, true},
		{"EV_SYN", evSyn, false},
	}
	for _, c := range cases {
		typ, code, val := parseInputEvent(c.ev)
		if got := isTouchEvent(typ, code, val); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

type touchFixture struct {
	clk     *armClock
	w       *TouchWatcher
	mode    atomic.Value
	wakes   chan struct{}
	// undelivered 为 true 时 Wake 返回未投递（链路未连接）。
	undelivered atomic.Bool
	changes atomic.Int32

	mu      sync.Mutex
	result  TouchResult
	pipes   map[string]*io.PipeWriter
	opened  chan string
	openErr map[string]error
}

func newTouchFixture(t *testing.T, devices ...string) *touchFixture {
	t.Helper()
	f := &touchFixture{
		clk:     newArmClock(testStart),
		wakes:   make(chan struct{}, 16),
		pipes:   map[string]*io.PipeWriter{},
		opened:  make(chan string, 16),
		openErr: map[string]error{},
		result:  TouchResult{Known: true, Touch: len(devices) > 0, Devices: devices},
	}
	f.mode.Store("off")
	f.w = NewTouchWatcher(TouchConfig{
		Clock: f.clk,
		Log:   quietLogger(),
		Detect: func() TouchResult {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.result
		},
		Open: func(path string) (io.ReadCloser, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if err := f.openErr[path]; err != nil {
				f.opened <- path
				return nil, err
			}
			r, w := io.Pipe()
			f.pipes[path] = w
			f.opened <- path
			return r, nil
		},
		Mode:     func() string { return f.mode.Load().(string) },
		Wake:     func() bool { f.wakes <- struct{}{}; return !f.undelivered.Load() },
		OnChange: func() { f.changes.Add(1) },
	})
	return f
}

func (f *touchFixture) setResult(r TouchResult) {
	f.mu.Lock()
	f.result = r
	f.mu.Unlock()
}

func (f *touchFixture) waitOpened(t *testing.T) string {
	t.Helper()
	select {
	case p := <-f.opened:
		return p
	case <-time.After(waitLimit):
		t.Fatal("等待打开设备超时")
		return ""
	}
}

func (f *touchFixture) send(t *testing.T, path string, ev []byte) {
	t.Helper()
	f.mu.Lock()
	w := f.pipes[path]
	f.mu.Unlock()
	if _, err := w.Write(ev); err != nil {
		t.Fatalf("写入事件失败: %v", err)
	}
}

// sync 写入一个无关事件：pipe 写入要等读端回到下一次 Read，因此返回时之前的事件都已处理完。
func (f *touchFixture) sync(t *testing.T, path string) { t.Helper(); f.send(t, path, evSyn) }

func (f *touchFixture) wantWake(t *testing.T) {
	t.Helper()
	select {
	case <-f.wakes:
	case <-time.After(waitLimit):
		t.Fatal("应发出 kiosk_wake")
	}
}

func (f *touchFixture) wantNoWake(t *testing.T) {
	t.Helper()
	if n := len(f.wakes); n != 0 {
		t.Fatalf("不应发 kiosk_wake，实际 %d 次", n)
	}
}

func TestTouchWatcher_仅关屏时触摸才唤醒(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	runUntilCancel(t, f.w.Run)
	p := f.waitOpened(t)

	f.mode.Store("on")
	f.send(t, p, evTouchDown)
	f.sync(t, p)
	f.wantNoWake(t)

	f.mode.Store("off")
	f.send(t, p, evTouchDown)
	f.wantWake(t)
}

func TestTouchWatcher_EV_ABS也算触摸_抬起与SYN不算(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	runUntilCancel(t, f.w.Run)
	p := f.waitOpened(t)
	f.send(t, p, evTouchUp)
	f.send(t, p, evSyn)
	f.sync(t, p)
	f.wantNoWake(t)
	f.send(t, p, evAbs)
	f.wantWake(t)
}

func TestTouchWatcher_五秒去抖(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	runUntilCancel(t, f.w.Run)
	p := f.waitOpened(t)

	f.send(t, p, evTouchDown)
	f.wantWake(t)
	f.clk.Advance(4 * time.Second)
	f.send(t, p, evTouchDown)
	f.send(t, p, evAbs)
	f.sync(t, p)
	f.wantNoWake(t)

	f.clk.Advance(time.Second) // 距首次满 5 秒
	f.send(t, p, evTouchDown)
	f.wantWake(t)
}

func TestTouchWatcher_唤醒未投递时不记去抖(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	runUntilCancel(t, f.w.Run)
	p := f.waitOpened(t)

	f.undelivered.Store(true)
	f.send(t, p, evTouchDown)
	f.wantWake(t) // 尝试过但链路未连接
	f.undelivered.Store(false)
	f.send(t, p, evTouchDown) // 时钟没动：未投递的那次不应占去抖窗口
	f.wantWake(t)
	f.send(t, p, evTouchDown)
	f.sync(t, p)
	f.wantNoWake(t) // 投递成功后才开始去抖
}

func TestTouchWatcher_设备打不开重测时重试(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	f.openErr["/dev/input/event3"] = errors.New("permission denied")
	runUntilCancel(t, f.w.Run)
	f.waitOpened(t) // 第一次失败
	f.clk.waitArmed(t, time.Minute)

	f.mu.Lock()
	delete(f.openErr, "/dev/input/event3")
	f.mu.Unlock()
	f.clk.Advance(time.Minute)
	p := f.waitOpened(t)
	f.send(t, p, evTouchDown)
	f.wantWake(t)
}

func TestTouchWatcher_检测结果变化才通知且已打开的设备不重复打开(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	runUntilCancel(t, f.w.Run)
	f.waitOpened(t)
	f.clk.waitArmed(t, time.Minute)
	if f.changes.Load() != 1 {
		t.Fatalf("首次检测应通知，实际 %d", f.changes.Load())
	}
	if v := f.w.Result().Touchscreen(); v == nil || !*v {
		t.Fatal("结果应为有触摸屏")
	}

	f.clk.Advance(time.Minute) // 结果不变
	f.clk.waitArmed(t, time.Minute)
	if f.changes.Load() != 1 || len(f.opened) != 0 {
		t.Fatalf("结果不变不应通知也不应重复打开: changes=%d opened=%d", f.changes.Load(), len(f.opened))
	}

	f.setResult(TouchResult{Known: true}) // 拔掉触摸屏
	f.clk.Advance(time.Minute)
	f.clk.waitArmed(t, time.Minute)
	if f.changes.Load() != 2 {
		t.Fatalf("结果变化应通知，实际 %d", f.changes.Load())
	}
	if v := f.w.Result().Touchscreen(); v == nil || *v {
		t.Fatal("结果应为无触摸屏")
	}
}

func TestTouchWatcher_设备读取结束后重测重新打开(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	runUntilCancel(t, f.w.Run)
	p := f.waitOpened(t)
	f.clk.waitArmed(t, time.Minute)

	f.mu.Lock()
	_ = f.pipes[p].CloseWithError(errors.New("ENODEV"))
	f.mu.Unlock()
	// 等读协程退出
	deadline := time.After(waitLimit)
	for f.w.readerCount() != 0 {
		select {
		case <-deadline:
			t.Fatal("读协程未退出")
		case <-time.After(time.Millisecond):
		}
	}
	f.clk.Advance(time.Minute)
	f.waitOpened(t)
}

func TestTouchWatcher_未知结果不开设备(t *testing.T) {
	f := newTouchFixture(t)
	f.setResult(TouchResult{})
	runUntilCancel(t, f.w.Run)
	f.clk.waitArmed(t, time.Minute)
	if len(f.opened) != 0 {
		t.Fatal("不应打开设备")
	}
	if f.w.Result().Touchscreen() != nil {
		t.Fatal("未知应为 nil")
	}
}

func TestTouchWatcher_取消后关闭设备并返回(t *testing.T) {
	f := newTouchFixture(t, "/dev/input/event3")
	stop := runUntilCancel(t, f.w.Run)
	f.waitOpened(t)
	stop() // 读协程阻塞在 pipe 上，必须靠关闭设备才能退出
	if f.w.readerCount() != 0 {
		t.Fatal("退出后不应残留读协程")
	}
}
