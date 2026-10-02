//go:build linux

package kiosk

import "syscall"

// launchAttr 让 Chromium 自成进程组；kiosk 异常退出时内核向 Chromium 发 SIGTERM，避免孤儿。
func launchAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
