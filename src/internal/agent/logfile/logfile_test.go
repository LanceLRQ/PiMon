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
