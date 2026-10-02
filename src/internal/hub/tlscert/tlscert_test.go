package tlscert

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadOrCreate_使用中枢CN且可重复加载(t *testing.T) {
	dir := t.TempDir()
	c, k := filepath.Join(dir, "hub.crt"), filepath.Join(dir, "hub.key")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := LoadOrCreate(c, k, now, Env{})
	if err != nil {
		t.Fatal(err)
	}
	if cn := first.Leaf.Subject.CommonName; cn != "PiMon Hub" {
		t.Fatalf("CN = %q", cn)
	}
	second, err := LoadOrCreate(c, k, now.Add(time.Hour), Env{})
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) != string(second.Certificate[0]) {
		t.Fatal("再次读取应得到同一张证书")
	}
}
