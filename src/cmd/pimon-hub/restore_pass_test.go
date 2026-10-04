package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
)

func TestTakeValueFlag(t *testing.T) {
	cases := []struct {
		in   []string
		rest []string
		val  string
		bad  bool
	}{
		{[]string{"--passphrase-file", "p", "x"}, []string{"x"}, "p", false},
		{[]string{"a", "--passphrase-file=p q", "b"}, []string{"a", "b"}, "p q", false},
		{[]string{"a", "b"}, []string{"a", "b"}, "", false},
		{[]string{"--", "--passphrase-file", "p"}, []string{"--", "--passphrase-file", "p"}, "", false},
		{[]string{"--passphrase-file"}, nil, "", true},
		{[]string{"--passphrase-file="}, nil, "", true},
		{[]string{"--passphrase-file", ""}, nil, "", true},
	}
	for _, c := range cases {
		rest, val, err := takeValueFlag(c.in, "--passphrase-file")
		if (err != nil) != c.bad {
			t.Fatalf("%v: err=%v", c.in, err)
		}
		if c.bad {
			continue
		}
		if !reflect.DeepEqual(rest, c.rest) || val != c.val {
			t.Fatalf("%v: rest=%v val=%q", c.in, rest, val)
		}
	}
}

// writeEncryptedBackup 生成含合法 pimon.db 与 secret.key 的加密备份，返回路径。
func writeEncryptedBackup(t *testing.T, pass string) string {
	t.Helper()
	var plain bytes.Buffer
	gz := gzip.NewWriter(&plain)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"pimon.db":   "SQLite format 3\x00" + strings.Repeat("x", 64),
		"secret.key": strings.Repeat("k", 32),
	} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	r, err := backup.EncryptReader(&plain, pass, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "pimon-backup-x.tar.gz.age")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runWithStdin(stdin string, args ...string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(stdin), &o, &e, func(string) string { return "" }, nil)
	return code, o.String(), e.String()
}

func TestRestoreEncryptedPassphraseFile(t *testing.T) {
	stubRootRun(t, nil)
	enc := writeEncryptedBackup(t, "correct horse battery")
	pf := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(pf, []byte("correct horse battery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, form := range [][]string{{"--passphrase-file", pf}, {"--passphrase-file=" + pf}} {
		dir := t.TempDir()
		args := append([]string{"restore", "--data-dir", dir}, form...)
		code, _, errOut := runArgs(append(args, enc)...)
		if code != 0 {
			t.Fatalf("%v: code=%d err=%q", form, code, errOut)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "secret.key")); len(b) != 32 {
			t.Fatalf("密钥未恢复: %q", b)
		}
	}
}

func TestRestoreEncryptedWrongPassphraseFile(t *testing.T) {
	stubRootRun(t, nil)
	enc := writeEncryptedBackup(t, "correct horse battery")
	pf := filepath.Join(t.TempDir(), "pass")
	_ = os.WriteFile(pf, []byte("wrong passphrase!!\n"), 0o600)
	dir := t.TempDir()
	code, _, errOut := runArgs("restore", "--data-dir", dir, "--passphrase-file", pf, enc)
	if code != 1 || !strings.Contains(errOut, "口令错误或文件损坏") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	des, _ := os.ReadDir(dir)
	for _, d := range des {
		if d.Name() != ".lock" && !strings.HasSuffix(d.Name(), ".lock") {
			t.Fatalf("数据目录不应出现 %s", d.Name())
		}
	}
}

func TestRestoreEncryptedPromptsFromStdin(t *testing.T) {
	stubRootRun(t, nil)
	enc := writeEncryptedBackup(t, "correct horse battery")
	dir := t.TempDir()
	code, out, errOut := runWithStdin("correct horse battery\n", "restore", "--data-dir", dir, enc)
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if strings.Contains(out+errOut, "correct horse battery") {
		t.Fatal("口令不得出现在输出里")
	}
}

func TestRestoreEncryptedEmptyStdinFails(t *testing.T) {
	stubRootRun(t, nil)
	enc := writeEncryptedBackup(t, "correct horse battery")
	code, _, errOut := runWithStdin("", "restore", "--data-dir", t.TempDir(), enc)
	if code != 1 || errOut == "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestPassphraseFileOnlyForRestore(t *testing.T) {
	code, _, errOut := runArgs("setup-code", "--passphrase-file", "x")
	if code != 2 {
		t.Fatalf("非 restore 命令应把它当未知参数: code=%d err=%q", code, errOut)
	}
}
