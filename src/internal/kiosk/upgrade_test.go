package kiosk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutable_去掉deleted后缀并解析符号链接(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "pimon-hub")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)

	got, err := resolveExecutable(func() (string, error) { return real + " (deleted)", nil })
	if err != nil || got != realResolved {
		t.Fatalf("got %q err %v，期望 %q", got, err, realResolved)
	}
	got, err = resolveExecutable(func() (string, error) { return link, nil })
	if err != nil || got != realResolved {
		t.Fatalf("符号链接未解析: got %q err %v", got, err)
	}
	// 去掉后缀后文件仍不存在：保持原样返回，交给 exec 报错
	ghost := filepath.Join(dir, "ghost") + " (deleted)"
	got, err = resolveExecutable(func() (string, error) { return ghost, nil })
	if err != nil || got != ghost {
		t.Fatalf("got %q err %v", got, err)
	}
}
