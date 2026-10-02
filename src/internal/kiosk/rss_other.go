//go:build !linux

package kiosk

// groupRSS 在非 Linux 上没有 /proc，始终未知。
func groupRSS(int) (int64, bool) { return 0, false }
