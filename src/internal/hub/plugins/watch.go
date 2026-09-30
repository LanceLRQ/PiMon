package plugins

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch 监视插件目录，变化后防抖 Debounce（默认 1 秒）再重新扫描。
// 防抖计时用注入的 Clock，事件来自真实文件系统。监视建立成功后立即返回；
// 返回的 channel 在监视停止（ctx 结束）后关闭。插件目录不存在时会先创建。
func (r *Registry) Watch(ctx context.Context) (<-chan struct{}, error) {
	if err := os.MkdirAll(r.cfg.Dir, 0o750); err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(r.cfg.Dir); err != nil {
		_ = w.Close()
		return nil, err
	}
	r.addSubdirs(w)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = w.Close() }()
		r.watchLoop(ctx, w)
	}()
	return done, nil
}

// addSubdirs 给根目录下每个子目录加监视（重复添加无副作用）。
func (r *Registry) addSubdirs(w *fsnotify.Watcher) {
	entries, err := os.ReadDir(r.cfg.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := w.Add(filepath.Join(r.cfg.Dir, e.Name())); err != nil {
				slog.Warn("监视插件子目录失败", "dir", e.Name(), "err", err)
			}
		}
	}
}

func (r *Registry) watchLoop(ctx context.Context, w *fsnotify.Watcher) {
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
			r.addSubdirs(w)
			// 防抖：记录最近一次事件时间，计时到点后若期间又有事件则顺延。
			last = r.cfg.Clock.Now()
			if timer == nil {
				timer = r.cfg.Clock.After(r.cfg.Debounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Warn("插件目录监视出错", "err", err)
		case <-timer:
			timer = nil
			if rest := last.Add(r.cfg.Debounce).Sub(r.cfg.Clock.Now()); rest > 0 {
				timer = r.cfg.Clock.After(rest)
				continue
			}
			if _, err := r.Scan(ctx); err != nil && ctx.Err() == nil {
				slog.Error("重新扫描插件目录失败", "err", err)
			}
		}
	}
}
