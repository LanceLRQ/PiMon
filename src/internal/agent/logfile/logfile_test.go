package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriter_超过上限改名为点1再重开_只保留一个旧文件(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	for _, s := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n", "eeee\n"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	cur, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	if string(cur) != "eeee\n" || string(old) != "cccc\ndddd\n" {
		t.Fatalf("当前 %q，旧 %q", cur, old)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限 %v", fi.Mode().Perm())
	}
}

func TestWriter_重开时沿用已有大小(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 8)), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte("yyyy\n")); err != nil {
		t.Fatal(err)
	}
	if old, _ := os.ReadFile(path + ".1"); string(old) != strings.Repeat("x", 8) {
		t.Fatalf("应先轮转已有内容，旧文件 %q", old)
	}
}

func TestWriter_单条超过上限仍写入(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	w, err := Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte("0123456789\n")); err != nil {
		t.Fatal(err)
	}
	if cur, _ := os.ReadFile(path); string(cur) != "0123456789\n" {
		t.Fatalf("当前 %q", cur)
	}
}

func TestOpen_收紧已有文件的宽松权限(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限 %v", fi.Mode().Perm())
	}
}

func TestWriter_改名失败后不每条都重试(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	calls := 0
	rename = func(string, string) error { calls++; return os.ErrPermission }
	defer func() { rename = os.Rename }()
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	for i := 0; i < 4; i++ { // 每条 5 字节：第 3 条触发轮转并失败，之后要再写满一个上限才重试
		if _, err := w.Write([]byte("aaaa\n")); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("改名调用 %d 次，期望 1", calls)
	}
	if cur, _ := os.ReadFile(path); string(cur) != strings.Repeat("aaaa\n", 4) {
		t.Fatalf("改名失败时应继续写当前文件: %q", cur)
	}
}

func TestWriter_改名成功但重开失败_改回原名继续写原路径(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, os.ErrPermission }
	defer func() { openFile = os.OpenFile }()
	for _, s := range []string{"aaaa\n", "bbbb\n", "cccc\n"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if cur, _ := os.ReadFile(path); string(cur) != "aaaa\nbbbb\ncccc\n" {
		t.Fatalf("应写在原路径: %q", cur)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("不应留下 .1: %v", err)
	}
}
