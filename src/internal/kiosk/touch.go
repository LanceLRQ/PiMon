package kiosk

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	evKey      = 0x01
	evAbsolute = 0x03
	btnTouch   = 0x14a

	touchRescanInterval = 60 * time.Second
	// wakeDebounce 是关屏期间两次 kiosk_wake 的最小间隔。
	wakeDebounce = 5 * time.Second
)

// inputEventSize 是 struct input_event 的大小：timeval（两个 long）+ type u16 + code u16 + value s32。
// 64 位系统（arm64）为 24 字节，32 位为 16 字节。
var inputEventSize = 8 + 2*(strconv.IntSize/8)

// parseInputEvent 取出一个原始 input_event 的 type、code、value（按本机字节序）。
func parseInputEvent(buf []byte) (typ, code uint16, value int32) {
	off := len(buf) - 8
	return binary.NativeEndian.Uint16(buf[off:]),
		binary.NativeEndian.Uint16(buf[off+2:]),
		int32(binary.NativeEndian.Uint32(buf[off+4:]))
}

// isTouchEvent 判断事件是否表示触摸：BTN_TOUCH 按下或任意 EV_ABS 坐标事件。
func isTouchEvent(typ, code uint16, value int32) bool {
	switch typ {
	case evKey:
		return code == btnTouch && value == 1
	case evAbsolute:
		return true
	}
	return false
}

// TouchConfig 是触摸监听的依赖，均可注入。
type TouchConfig struct {
	Clock clock.Clock
	Log   *slog.Logger
	// Detect 检测触摸设备（默认 DetectTouch 真实目录）。
	Detect func() TouchResult
	// Open 打开输入设备做只读旁听，不 grab（默认 os.Open）。
	Open func(path string) (io.ReadCloser, error)
	// Mode 返回当前屏幕状态的 mode（on/off）。
	Mode func() string
	// Wake 发出一次 kiosk_wake，返回是否已投递（链路未连接时为 false）。
	Wake func() bool
	// OnChange 在检测结果发生变化（含首次检测）时调用，用于立即上报。
	OnChange func()
	// Interval 是重新检测的周期，默认 60 秒。
	Interval time.Duration
}

// TouchWatcher 周期检测触摸屏，并旁听触摸设备：关屏期间收到触摸就请求唤醒。
type TouchWatcher struct {
	cfg TouchConfig

	mu       sync.Mutex
	result   TouchResult
	scanned  bool
	readers  map[string]io.Closer
	lastWake time.Time
	wg       sync.WaitGroup
}

// NewTouchWatcher 创建触摸监听并补默认值。
func NewTouchWatcher(cfg TouchConfig) *TouchWatcher {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Detect == nil {
		cfg.Detect = func() TouchResult { return DetectTouch(DefaultInputDir, DefaultUdevDataDir) }
	}
	if cfg.Open == nil {
		cfg.Open = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	}
	if cfg.Interval <= 0 {
		cfg.Interval = touchRescanInterval
	}
	return &TouchWatcher{cfg: cfg, readers: map[string]io.Closer{}}
}

// Result 返回最近一次检测结果（尚未检测时为未知）。
func (w *TouchWatcher) Result() TouchResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.result
	r.Devices = slices.Clone(r.Devices)
	return r
}

func (w *TouchWatcher) readerCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.readers)
}

// Run 启动时检测一次，之后每隔 Interval 重测（热插拔）；ctx 结束时关闭所有设备并返回。
func (w *TouchWatcher) Run(ctx context.Context) {
	defer w.shutdown()
	for {
		w.rescan()
		select {
		case <-ctx.Done():
			return
		case <-w.cfg.Clock.After(w.cfg.Interval):
		}
	}
}

func (w *TouchWatcher) rescan() {
	res := w.cfg.Detect()
	w.mu.Lock()
	changed := !w.scanned || res.Known != w.result.Known || res.Touch != w.result.Touch
	w.result, w.scanned = res, true
	// 设备已不在列表里：关闭，读协程随之结束。
	for path, c := range w.readers {
		if !slices.Contains(res.Devices, path) {
			_ = c.Close()
		}
	}
	var toOpen []string
	for _, path := range res.Devices {
		if _, ok := w.readers[path]; !ok {
			toOpen = append(toOpen, path)
		}
	}
	w.mu.Unlock()

	for _, path := range toOpen {
		rc, err := w.cfg.Open(path)
		if err != nil {
			w.cfg.Log.Warn("无法打开触摸设备，下次重测时重试", "dev", path, "err", err)
			continue
		}
		w.mu.Lock()
		w.readers[path] = rc
		w.mu.Unlock()
		w.wg.Add(1)
		go w.readLoop(path, rc)
	}
	if changed && w.cfg.OnChange != nil {
		w.cfg.OnChange()
	}
}

// readLoop 读取设备事件直到出错或被关闭，退出时从 readers 摘除自己以便重测时重开。
func (w *TouchWatcher) readLoop(path string, rc io.ReadCloser) {
	defer w.wg.Done()
	defer func() {
		_ = rc.Close()
		w.mu.Lock()
		if w.readers[path] == rc {
			delete(w.readers, path)
		}
		w.mu.Unlock()
	}()
	buf := make([]byte, inputEventSize)
	for {
		if _, err := io.ReadFull(rc, buf); err != nil {
			w.cfg.Log.Debug("触摸设备读取结束", "dev", path, "err", err)
			return
		}
		if isTouchEvent(parseInputEvent(buf)) {
			w.onTouch()
		}
	}
}

// onTouch 仅在屏幕处于关闭状态时请求唤醒，且最小间隔 wakeDebounce。
func (w *TouchWatcher) onTouch() {
	if w.cfg.Mode() != "off" {
		return
	}
	now := w.cfg.Clock.Now()
	w.mu.Lock()
	if !w.lastWake.IsZero() && now.Sub(w.lastWake) < wakeDebounce {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	if !w.cfg.Wake() {
		return // 未投递不占去抖窗口，下一次触摸可以重试
	}
	w.mu.Lock()
	w.lastWake = now
	w.mu.Unlock()
}

func (w *TouchWatcher) shutdown() {
	w.mu.Lock()
	for _, c := range w.readers {
		_ = c.Close()
	}
	w.mu.Unlock()
	w.wg.Wait()
}
