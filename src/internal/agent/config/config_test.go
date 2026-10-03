package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() Config {
	return Config{
		Mode: ModeDialIn, HubURL: "wss://10.0.0.2:31418", HubPin: "SHA256:abc",
		Credential: "super-secret-credential", HubHostname: "pimon.local",
		Listen: ":31418", CredentialHash: "0123456789abcdef0123", PrevCredentialHash: "fedcba9876543210",
		Name: "nas", Role: "nas",
	}
}

func TestSaveLoad往返(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	want := sample()
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode(), err)
	}
	got, err := Load(path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	raw, _ := os.ReadFile(path)
	for _, k := range []string{`"mode"`, `"hub_url"`, `"hub_pin"`, `"credential"`, `"hub_hostname"`, `"listen"`, `"credential_hash"`, `"prev_credential_hash"`, `"name"`, `"role"`} {
		if !bytes.Contains(raw, []byte(k)) {
			t.Errorf("缺少字段 %s", k)
		}
	}
}

func TestSave覆盖不留临时文件(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	for i := 0; i < 2; i++ {
		if err := Save(path, sample()); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("目录残留: %v", entries)
	}
}

func TestLoad权限过宽拒绝(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, os.Geteuid())
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoad属主不符拒绝(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 下无法构造属主不符")
	}
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, os.Geteuid()+1); err == nil || !strings.Contains(err.Error(), "属主") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoad缺失与非法(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "none.json"), os.Geteuid()); !os.IsNotExist(unwrapAll(err)) {
		t.Fatalf("err=%v", err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"mode":"weird"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad, os.Geteuid()); err == nil {
		t.Fatal("非法模式应报错")
	}
	garbage := filepath.Join(dir, "g.json")
	if err := os.WriteFile(garbage, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(garbage, os.Geteuid()); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

func unwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}

func TestCredential不出现在格式化与日志(t *testing.T) {
	c := sample()
	const secret = "super-secret-credential"
	outs := []string{
		fmt.Sprint(c), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c),
		fmt.Sprintf("%v", &c), fmt.Sprint(c.Credential), fmt.Sprintf("%#v", c.Credential), c.String(),
	}
	var jb, tb bytes.Buffer
	slog.New(slog.NewJSONHandler(&jb, nil)).Info("x", "cfg", c, "cred", c.Credential)
	slog.New(slog.NewTextHandler(&tb, nil)).Info("x", "cfg", c, "cred", c.Credential)
	outs = append(outs, jb.String(), tb.String())
	for _, o := range outs {
		if strings.Contains(o, secret) {
			t.Errorf("泄露凭据: %s", o)
		}
	}
	if !strings.Contains(c.String(), "01234567") || strings.Contains(c.String(), "0123456789abcdef0123") {
		t.Errorf("凭据哈希应只显示前 8 位: %s", c.String())
	}
	// 文件里必须是原文
	b, _ := json.Marshal(c)
	if !bytes.Contains(b, []byte(secret)) {
		t.Error("JSON 编码应保留原文")
	}
}
