// Package logfile 是 agent 在 macOS LaunchAgent 下使用的日志文件：超过上限时把当前文件改名为 .1 再重开，
// 只保留一个旧文件（launchd 的 StandardErrorPath 不会轮转）。
package logfile

import (
	"os"
	"sync"
)

// DefaultMaxBytes 是默认的单文件上限。
const DefaultMaxBytes = 5 << 20

// 测试替换点。
var (
	rename   = os.Rename
	openFile = os.OpenFile
)

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

// open 打开（必要时创建）日志文件，并把已存在文件的宽松权限收紧为 0600。
func (w *Writer) open() error {
	f, err := openFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
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
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		w.rotate()
		if w.f == nil {
			return 0, os.ErrClosed
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate 把当前文件改名为 .1 再重开。失败时把计数清零，再写满一个上限才重试，避免每条日志都重试：
// 改名失败继续写当前文件；改名成功但重开失败则改回原名继续写原路径，改回也失败就停止写入（不写已改名的文件）。
func (w *Writer) rotate() {
	if err := rename(w.path, w.path+".1"); err != nil {
		w.size = 0
		return
	}
	old := w.f
	if err := w.open(); err == nil {
		_ = old.Close()
		return
	}
	w.size = 0
	if err := rename(w.path+".1", w.path); err != nil {
		_ = old.Close()
		w.f = nil
		return
	}
	w.f = old
}

// Close 关闭文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
