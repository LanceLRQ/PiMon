//go:build linux

package system

import "os"

// procWriteBytes 读取 /proc/self/io 的 write_bytes（进程实际下发到存储层的写入字节数）。
func procWriteBytes() (int64, bool) {
	b, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, false
	}
	return parseWriteBytes(string(b))
}
