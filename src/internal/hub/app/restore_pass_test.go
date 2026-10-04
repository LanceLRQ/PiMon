package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
)

func TestPassphraseFromFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for body, want := range map[string]string{"secret-pass\n": "secret-pass", "secret-pass\r\n": "secret-pass", "secret-pass": "secret-pass", " a b \n": " a b "} {
		got, err := PassphraseFromFile(write("p", body))()
		if err != nil || got != want {
			t.Fatalf("%q -> %q %v", body, got, err)
		}
	}
	for name, p := range map[string]string{"空文件": write("e", ""), "只有换行": write("n", "\r\n"), "不存在": filepath.Join(dir, "nope")} {
		if _, err := PassphraseFromFile(p)(); err == nil {
			t.Fatalf("%s 应报错", name)
		}
	}
}

func TestPassphrasePromptNonTerminal(t *testing.T) {
	var out bytes.Buffer
	got, err := PassphrasePrompt(strings.NewReader("my-pass\nrest"), &out, false)()
	if err != nil || got != "my-pass" {
		t.Fatalf("%q %v", got, err)
	}
	if strings.Contains(out.String(), "my-pass") {
		t.Fatal("口令不得写到输出")
	}
	if _, err := PassphrasePrompt(strings.NewReader(""), &out, false)(); err == nil {
		t.Fatal("空输入应报错")
	}
	if _, err := PassphrasePrompt(strings.NewReader("\n"), &out, false)(); err == nil {
		t.Fatal("空口令应报错")
	}
}

func encryptFile(t *testing.T, plainPath, pass string) string {
	t.Helper()
	src, err := os.Open(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	r, err := backup.EncryptReader(src, pass, 10)
	if err != nil {
		t.Fatal(err)
	}
	p := plainPath + ".age"
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, r); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRestoreEncryptedWithPassphrase(t *testing.T) {
	cfg := testConfig(t)
	a := openApp(t, cfg)
	info, err := a.backups.Create(context.Background(), backup.ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	enc := encryptFile(t, filepath.Join(cfg.BackupDir(), info.Name), "correct horse battery")
	ask := func(p string) backup.PassphraseFunc { return func() (string, error) { return p, nil } }

	dbBefore, _ := os.ReadFile(cfg.DBPath())
	var out bytes.Buffer
	err = Restore(cfg, enc, &out, ask("wrong passphrase!!"))
	if !errors.Is(err, backup.ErrPassphraseOrCorrupt) {
		t.Fatalf("错误口令: %v", err)
	}
	if after, _ := os.ReadFile(cfg.DBPath()); !bytes.Equal(after, dbBefore) {
		t.Fatal("错误口令不应改动数据库")
	}
	assertNoRestoreLeftovers(t, cfg.DataDir)

	if err := os.Remove(cfg.DBPath()); err != nil {
		t.Fatal(err)
	}
	if err := Restore(cfg, enc, &out, ask("correct horse battery")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		t.Fatalf("恢复后数据库应存在: %v", err)
	}
	assertNoRestoreLeftovers(t, cfg.DataDir)
}

func assertNoRestoreLeftovers(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && (strings.HasSuffix(p, ".restore") || strings.HasSuffix(p, ".pre-restore")) {
			t.Errorf("残留临时文件: %s", p)
		}
		return nil
	})
}
