package pluginscan

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/fsnotify/fsnotify"
)

// WatchConfig 是 Watch 的参数。Debounce 缺省 1 秒，Clock 缺省真实时钟，Log 缺省 slog.Default()。
type WatchConfig struct {
	Dir      string
	Clock    clock.Clock
	Debounce time.Duration
	Log      *slog.Logger
}

// Watch 监视插件目录（含其下一层子目录），变化后防抖 Debounce 再调用 onChange。
// 防抖计时用注入的 Clock，事件来自真实文件系统。监视建立成功后立即返回；
// 返回的 channel 在监视停止（ctx 结束）后关闭。目录必须已存在，创建由调用方负责。
func Watch(ctx context.Context, cfg WatchConfig, onChange func()) (<-chan struct{}, error) {
	if cfg.Debounce <= 0 {
		cfg.Debounce = time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(cfg.Dir); err != nil {
		_ = w.Close()
		return nil, err
	}
	addSubdirs(cfg, w)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = w.Close() }()
		watchLoop(ctx, cfg, w, onChange)
	}()
	return done, nil
}

// addSubdirs 给根目录下每个子目录加监视（重复添加无副作用）。
func addSubdirs(cfg WatchConfig, w *fsnotify.Watcher) {
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := w.Add(filepath.Join(cfg.Dir, e.Name())); err != nil {
				cfg.Log.Warn("监视插件子目录失败", "dir", e.Name(), "err", err)
			}
		}
	}
}

func watchLoop(ctx context.Context, cfg WatchConfig, w *fsnotify.Watcher, onChange func()) {
	var (
		timer <-chan time.Time
		last  time.Time
	)
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-w.Events:
			if !ok {
				return
			}
			addSubdirs(cfg, w)
			// 防抖：记录最近一次事件时间，计时到点后若期间又有事件则顺延。
			last = cfg.Clock.Now()
			if timer == nil {
				timer = cfg.Clock.After(cfg.Debounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			cfg.Log.Warn("插件目录监视出错", "err", err)
		case <-timer:
			timer = nil
			if rest := last.Add(cfg.Debounce).Sub(cfg.Clock.Now()); rest > 0 {
				timer = cfg.Clock.After(rest)
				continue
			}
			onChange()
		}
	}
}
