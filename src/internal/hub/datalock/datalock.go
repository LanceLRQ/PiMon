// Package datalock 对数据目录取进程间互斥锁：serve 与 restore 不能同时操作同一数据目录。
// 锁取在目录自身的文件描述符上（flock），不新建锁文件；进程退出时由内核自动释放。
package datalock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrLocked 表示数据目录已被另一个进程持有。
var ErrLocked = errors.New("数据目录已被占用")

// Lock 是持有中的数据目录锁。
type Lock struct {
	f *os.File
}

// Acquire 对 dir 取独占的非阻塞 flock；已被占用返回 ErrLocked。
func Acquire(dir string) (*Lock, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("打开数据目录: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("锁定数据目录: %w", err)
	}
	return &Lock{f: f}, nil
}

// Release 释放锁；可重复调用。
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	return f.Close() // 关闭 fd 即释放 flock
}
