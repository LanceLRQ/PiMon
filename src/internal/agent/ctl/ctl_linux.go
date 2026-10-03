//go:build linux

package ctl

import (
	"net"

	"golang.org/x/sys/unix"
)

// maxPathLen 是 Linux unix 套接字路径的上限（sun_path 108 字节，留一个结尾 NUL）。
const maxPathLen = 107

// peerUID 用 SO_PEERCRED 读取对端 uid。
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if cerr != nil {
		return -1, cerr
	}
	return int(cred.Uid), nil
}
