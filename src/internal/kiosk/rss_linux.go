//go:build linux

package kiosk

// groupRSS 返回以 pgid 为进程组号的所有进程的 RSS 合计（字节）。
func groupRSS(pgid int) (int64, bool) { return sumGroupRSS("/proc", pgid) }
