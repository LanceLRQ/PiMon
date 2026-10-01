//go:build !linux

package system

// procWriteBytes 在非 Linux 平台没有进程 IO 统计，返回未知。
func procWriteBytes() (int64, bool) { return 0, false }
