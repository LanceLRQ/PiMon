package kiosk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CleanSingleton 删除 profile 目录下 Chromium 残留的 Singleton* 文件（异常退出后会阻止新实例启动）。
// 参数 lk 要求调用方已持有单实例锁：没有锁就无法确认没有别的 kiosk 正在使用该 profile。
func CleanSingleton(lk *Lock, profileDir string) error {
	if lk == nil || lk.f == nil {
		return errors.New("清理 Singleton 前必须先持有 kiosk 锁")
	}
	entries, err := os.ReadDir(profileDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 profile 目录: %w", err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "Singleton") {
			continue
		}
		// SingletonLock 通常是悬空符号链接，Remove 只删链接本身。
		if err := os.Remove(filepath.Join(profileDir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("删除 %s: %w", e.Name(), err)
		}
	}
	return nil
}
