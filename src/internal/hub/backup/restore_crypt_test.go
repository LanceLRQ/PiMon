package backup

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encryptArchive 把明文 tar.gz 加密成 age 文件并返回路径。
func encryptArchive(t *testing.T, plain, pass string) string {
	t.Helper()
	src, err := os.Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	r, err := EncryptReader(src, pass, 10)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "x.tar.gz.age")
	out, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, r); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func goodFiles() map[string]string {
	return map[string]string{"pimon.db": validDB(), "secret.key": validSecret()}
}

func fixedPass(p string) PassphraseFunc { return func() (string, error) { return p, nil } }

func TestRestoreFileEncrypted(t *testing.T) {
	e := newRestoreEnv(t)
	enc := encryptArchive(t, makeArchive(t, goodFiles()), "correct horse battery")
	if err := RestoreFile(enc, e.db, e.sk, fixedPass("correct horse battery")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.sk); string(b) != validSecret() {
		t.Fatalf("密钥未恢复: %q", b)
	}
	des, _ := os.ReadDir(e.dir)
	if len(des) != 2 {
		t.Fatalf("成功后不应残留临时文件: %v", des)
	}
}

func TestRestoreFileWrongPassphraseLeavesDataUntouched(t *testing.T) {
	e := newRestoreEnv(t)
	enc := encryptArchive(t, makeArchive(t, goodFiles()), "correct horse battery")
	err := RestoreFile(enc, e.db, e.sk, fixedPass("wrong passphrase!!"))
	if !errors.Is(err, ErrPassphraseOrCorrupt) {
		t.Fatalf("应为口令错误或文件损坏: %v", err)
	}
	if !strings.Contains(err.Error(), "口令错误或文件损坏") {
		t.Fatalf("文案: %v", err)
	}
	e.assertUntouched(t)
}

func TestRestoreFileTamperedBodyLeavesDataUntouched(t *testing.T) {
	e := newRestoreEnv(t)
	enc := encryptArchive(t, makeArchive(t, goodFiles()), "correct horse battery")
	b, _ := os.ReadFile(enc)
	b[len(b)-1] ^= 0xff // 篡改最后一块
	if err := os.WriteFile(enc, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreFile(enc, e.db, e.sk, fixedPass("correct horse battery")); !errors.Is(err, ErrPassphraseOrCorrupt) {
		t.Fatalf("应为口令错误或文件损坏: %v", err)
	}
	e.assertUntouched(t)
}

func TestRestoreFileTruncatedLeavesDataUntouched(t *testing.T) {
	e := newRestoreEnv(t)
	enc := encryptArchive(t, makeArchive(t, goodFiles()), "correct horse battery")
	b, _ := os.ReadFile(enc)
	if err := os.WriteFile(enc, b[:len(b)-20], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreFile(enc, e.db, e.sk, fixedPass("correct horse battery")); err == nil {
		t.Fatal("截断应报错")
	}
	e.assertUntouched(t)
}

func TestRestoreFilePlainIgnoresPassphraseSource(t *testing.T) {
	e := newRestoreEnv(t)
	called := false
	ask := func() (string, error) { called = true; return "x", nil }
	if err := RestoreFile(makeArchive(t, goodFiles()), e.db, e.sk, ask); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("非加密备份不应索取口令")
	}
}

func TestRestoreFileEncryptedNeedsPassphraseSource(t *testing.T) {
	e := newRestoreEnv(t)
	enc := encryptArchive(t, makeArchive(t, goodFiles()), "correct horse battery")
	if err := Restore(enc, e.db, e.sk); err == nil || !strings.Contains(err.Error(), "加密") {
		t.Fatalf("应提示备份已加密: %v", err)
	}
	e.assertUntouched(t)
}

func TestRestoreFilePassphraseSourceError(t *testing.T) {
	e := newRestoreEnv(t)
	enc := encryptArchive(t, makeArchive(t, goodFiles()), "correct horse battery")
	boom := errors.New("读不到口令")
	err := RestoreFile(enc, e.db, e.sk, func() (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("应透传口令来源错误: %v", err)
	}
	e.assertUntouched(t)
}

func TestRestoreFromPlainStream(t *testing.T) {
	e := newRestoreEnv(t)
	b, _ := os.ReadFile(makeArchive(t, goodFiles()))
	if err := RestoreFrom(bytes.NewReader(b), e.db, e.sk); err != nil {
		t.Fatal(err)
	}
}
