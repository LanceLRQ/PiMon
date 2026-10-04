//go:build !linux && !darwin

package ctl

import (
	"errors"
	"io/fs"
	"net"
)

// maxPathLen 取较保守的 BSD 上限。
const maxPathLen = 103

// peerUID 在不支持的平台上一律失败（连接被拒绝）。
func peerUID(*net.UnixConn) (int, error) {
	return -1, errors.New("本平台不支持读取控制套接字对端凭据")
}

// ownerUID 在不支持的平台上跳过属主检查。
func ownerUID(fs.FileInfo) (uint32, bool) { return 0, false }
