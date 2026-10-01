//go:build !linux

package system

import "testing"

func TestProcWriteBytesUnknownOffLinux(t *testing.T) {
	if _, ok := procWriteBytes(); ok {
		t.Fatal("非 Linux 平台应返回未知")
	}
}
