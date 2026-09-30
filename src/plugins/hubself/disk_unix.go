//go:build unix

package hubself

import "syscall"

// statDisk 返回 path 所在文件系统的普通用户可用空间与总空间（字节）。
func statDisk(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) //nolint:gosec // Bsize 恒为正
	return st.Bavail * bsize, st.Blocks * bsize, nil
}
