//go:build linux

package devnum

import "syscall"

// StatDev 返回 path 所在文件系统的设备号。
func StatDev(path string) (major, minor uint32, err error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, err
	}
	major, minor = Decode(uint64(st.Dev)) //nolint:unconvert // 各架构 Dev 类型不同
	return major, minor, nil
}

// StatRdev 返回设备节点 path 自身代表的设备号。
func StatRdev(path string) (major, minor uint32, err error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, err
	}
	major, minor = Decode(uint64(st.Rdev)) //nolint:unconvert // 各架构 Rdev 类型不同
	return major, minor, nil
}
