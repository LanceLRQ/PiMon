package kiosk

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireLock_第二个持有者拿不到锁(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kiosk", "kiosk.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("第二次应返回 ErrLocked，实际 %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("释放后应能再次取得: %v", err)
	}
	_ = again.Release()
}

func TestCleanSingleton_必须持锁且只删Singleton前缀(t *testing.T) {
	profile := t.TempDir()
	for _, n := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie", "Preferences"} {
		if err := os.WriteFile(filepath.Join(profile, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// SingletonLock 在真实环境是悬空符号链接
	if err := os.Remove(filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("host-12345", filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}

	if err := CleanSingleton(nil, profile); err == nil {
		t.Fatal("未持锁时必须拒绝清理")
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatalf("未持锁时不得删除: %v", err)
	}

	lk, err := AcquireLock(filepath.Join(t.TempDir(), "k.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release() }()
	if err := CleanSingleton(lk, profile); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(profile)
	if len(entries) != 1 || entries[0].Name() != "Preferences" {
		t.Fatalf("应只剩 Preferences，实际 %v", entries)
	}
}

func TestCleanSingleton_profile不存在时无操作(t *testing.T) {
	lk, err := AcquireLock(filepath.Join(t.TempDir(), "k.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release() }()
	if err := CleanSingleton(lk, filepath.Join(t.TempDir(), "none")); err != nil {
		t.Fatal(err)
	}
}
