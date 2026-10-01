//go:build linux

package system

import "testing"

func TestProcWriteBytesReadable(t *testing.T) {
	if _, ok := procWriteBytes(); !ok {
		t.Fatal("Linux 上应能读取 /proc/self/io")
	}
}
