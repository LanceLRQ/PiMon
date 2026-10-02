//go:build !linux

package hubself

// defaultSys 在非 Linux 平台没有 /proc 与 /sys，浸泡指标一律为未知。
func defaultSys() Sys { return unsupportedSys() }
