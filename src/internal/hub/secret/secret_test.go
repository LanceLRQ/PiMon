package secret

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keyPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "secret.key")
}

func TestLoadOrCreate_创建后再读取得到同一密钥(t *testing.T) {
	p := keyPath(t)
	a, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := a.Encrypt("hello")
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Decrypt(ct)
	if err != nil || got != "hello" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestLoadOrCreate_文件权限0600且长度32(t *testing.T) {
	p := keyPath(t)
	if _, err := LoadOrCreate(p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 || st.Size() != 32 {
		t.Fatalf("perm %v size %d", st.Mode().Perm(), st.Size())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("目录中残留临时文件: %v", entries)
	}
}

func TestLoadOrCreate_已有文件长度不对报错且不覆盖(t *testing.T) {
	p := keyPath(t)
	if err := os.WriteFile(p, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(p); err == nil {
		t.Fatal("期望报错")
	}
	data, _ := os.ReadFile(p)
	if string(data) != "short" {
		t.Fatal("文件被覆盖")
	}
}

func TestLoadOrCreate_创建时文件已被他人写入则读取已有(t *testing.T) {
	p := keyPath(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	// 模拟另一进程在本进程发布前已写好密钥
	hookBeforePublish = func() {
		if err := os.WriteFile(p, key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { hookBeforePublish = nil })

	a, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := New(key)
	ct, _ := ref.Encrypt("x")
	if got, err := a.Decrypt(ct); err != nil || got != "x" {
		t.Fatalf("未采用已有密钥: %q %v", got, err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != string(key) {
		t.Fatal("已有密钥被覆盖")
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Fatalf("残留临时文件: %v", entries)
	}
}

func TestNew_密钥长度不对报错(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := New(make([]byte, n)); err == nil {
			t.Fatalf("长度 %d 应报错", n)
		}
	}
}

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncryptDecrypt_往返一致(t *testing.T) {
	b := newBox(t)
	for _, s := range []string{"", "p@ss", strings.Repeat("中", 1000)} {
		ct, err := b.Encrypt(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.Decrypt(ct)
		if err != nil || got != s {
			t.Fatalf("%q -> %q err %v", s, got, err)
		}
	}
}

func TestEncrypt_同一明文两次结果不同(t *testing.T) {
	b := newBox(t)
	a, _ := b.Encrypt("same")
	c, _ := b.Encrypt("same")
	if a == c {
		t.Fatal("nonce 未随机")
	}
}

func TestDecrypt_篡改与过短报错(t *testing.T) {
	b := newBox(t)
	ct, _ := b.Encrypt("secret")
	raw, _ := base64.StdEncoding.DecodeString(ct)
	raw[len(raw)-1] ^= 1
	if _, err := b.Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("篡改应报错")
	}
	if _, err := b.Decrypt(base64.StdEncoding.EncodeToString(raw[:5])); err == nil {
		t.Fatal("过短应报错")
	}
	if _, err := b.Decrypt("!!!非base64"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	other, _ := New(append(make([]byte, 31), 1))
	if _, err := other.Decrypt(ct); err == nil {
		t.Fatal("换密钥应报错")
	}
}
