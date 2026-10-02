// Package kiosk 实现 `pimon-hub kiosk` 守护进程：以桌面用户身份在 labwc 图形会话里
// 持有单实例锁、拉起并看护 Chromium、按设置重启、会话结束时退出、hub 升级后换新二进制。
package kiosk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked 表示 kiosk 锁已被另一个进程持有。
var ErrLocked = errors.New("另一个 kiosk 守护进程正在运行")

// Lock 是持有中的 kiosk 单实例锁。
type Lock struct {
	f *os.File
}

// AcquireLock 对 path 取独占非阻塞 flock，必要时创建父目录；已被占用返回 ErrLocked。
// os.OpenFile 默认带 O_CLOEXEC，Chromium 子进程不会继承该 fd。
func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建锁目录: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("锁定 %s: %w", path, err)
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
	return f.Close()
}
