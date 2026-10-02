package datalock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquire同目录互斥(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("第二次获取应返回 ErrLocked, 得 %v", err)
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	b, err := Acquire(dir)
	if err != nil {
		t.Fatalf("释放后应可再次获取: %v", err)
	}
	_ = b.Release()
}

func TestAcquire不同目录互不影响(t *testing.T) {
	a, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Release() }()
	b, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Release()
}

func TestAcquire不新建锁文件(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()
	des, _ := os.ReadDir(dir)
	if len(des) != 0 {
		t.Fatalf("锁取在目录 fd 上，不应产生文件: %v", des)
	}
}

func TestAcquire目录不存在报错(t *testing.T) {
	if _, err := Acquire(filepath.Join(t.TempDir(), "nope")); err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestRelease可重复调用(t *testing.T) {
	l, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("重复释放应无错: %v", err)
	}
}
