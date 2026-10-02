package kiosk

import (
	"net"
	"path/filepath"
	"testing"
)

func TestSessionSocketPath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	p, err := SessionSocketPath(env(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "wayland-0"}))
	if err != nil || p != "/run/user/1000/wayland-0" {
		t.Fatalf("got %q %v", p, err)
	}
	p, err = SessionSocketPath(env(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "WAYLAND_DISPLAY": "/tmp/abs"}))
	if err != nil || p != "/tmp/abs" {
		t.Fatalf("绝对路径应原样使用: got %q %v", p, err)
	}
	for _, m := range []map[string]string{
		{"WAYLAND_DISPLAY": "wayland-0"},
		{"XDG_RUNTIME_DIR": "/run/user/1000"},
		{},
	} {
		if _, err := SessionSocketPath(env(m)); err == nil {
			t.Fatalf("环境变量缺失必须报错: %v", m)
		}
	}
}

func TestSocketProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.sock")
	probe := SocketProbe(path)
	if err := probe(); err == nil {
		t.Fatal("socket 不存在应失败")
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := probe(); err != nil {
		t.Fatalf("socket 存在应成功: %v", err)
	}
}
