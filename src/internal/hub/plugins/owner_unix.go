//go:build unix

package plugins

import (
	"io/fs"
	"syscall"
)

// ownerUID 返回文件属主 uid。
func ownerUID(info fs.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
