package kiosk

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"time"
)

// SessionSocketPath 按 Wayland 约定拼出合成器 socket 路径：
// WAYLAND_DISPLAY 为绝对路径时原样使用，否则位于 XDG_RUNTIME_DIR 下。
func SessionSocketPath(getenv func(string) string) (string, error) {
	display := getenv("WAYLAND_DISPLAY")
	if display == "" {
		return "", errors.New("环境变量 WAYLAND_DISPLAY 未设置：kiosk 必须在 Wayland 图形会话内启动")
	}
	if strings.HasPrefix(display, "/") {
		return display, nil
	}
	runtimeDir := getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", errors.New("环境变量 XDG_RUNTIME_DIR 未设置：kiosk 必须在 Wayland 图形会话内启动")
	}
	return filepath.Join(runtimeDir, display), nil
}

// SocketProbe 返回一个探测函数：能连上 socket 即会话仍然存活。
func SocketProbe(path string) func() error {
	return func() error {
		c, err := net.DialTimeout("unix", path, 2*time.Second)
		if err != nil {
			return err
		}
		return c.Close()
	}
}
