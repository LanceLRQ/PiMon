//go:build darwin

package ctl

import (
	"net"

	"golang.org/x/sys/unix"
)

// maxPathLen 是 macOS unix 套接字路径的上限（sun_path 104 字节，留一个结尾 NUL）。
const maxPathLen = 103

// peerUID 用 LOCAL_PEERCRED 读取对端 uid。
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *unix.Xucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if cerr != nil {
		return -1, cerr
	}
	return int(cred.Uid), nil
}
