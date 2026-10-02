//go:build !linux

package kiosk

import "syscall"

// launchAttr 让 Chromium 自成进程组（非 Linux 没有 Pdeathsig）。
func launchAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
