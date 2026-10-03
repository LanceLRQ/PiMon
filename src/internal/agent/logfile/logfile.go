// Package logfile 是 agent 在 macOS LaunchAgent 下使用的日志文件：超过上限时把当前文件改名为 .1 再重开，
// 只保留一个旧文件（launchd 的 StandardErrorPath 不会轮转）。
package logfile

import (
	"os"
	"sync"
)

// DefaultMaxBytes 是默认的单文件上限。
const DefaultMaxBytes = 5 << 20

// Writer 是带大小上限的日志文件，可并发写。
type Writer struct {
	path string
	max  int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

// Open 以 0600 追加方式打开 path；max 不大于 0 时用 DefaultMaxBytes。
func Open(path string, max int64) (*Writer, error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	w := &Writer{path: path, max: max}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f, w.size = f, fi.Size()
	return nil
}

// Write 写入一条日志；写入后会超过上限且当前文件非空时先轮转。轮转失败时继续写当前文件。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *Writer) rotate() {
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return
	}
	old := w.f
	if err := w.open(); err != nil {
		// 新文件打不开时继续写已改名的旧句柄，避免丢日志。
		w.f = old
		return
	}
	_ = old.Close()
}

// Close 关闭文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
