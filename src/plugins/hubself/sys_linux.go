//go:build linux

package hubself

import (
	"os"
	"syscall"
)

// defaultSys 读取真实的 /proc、/sys 与数据目录设备号。
func defaultSys() Sys {
	return Sys{
		ReadFile: os.ReadFile,
		Readlink: os.Readlink,
		StatDev: func(path string) (uint32, uint32, error) {
			var st syscall.Stat_t
			if err := syscall.Stat(path, &st); err != nil {
				return 0, 0, err
			}
			dev := uint64(st.Dev) //nolint:unconvert // 各架构 Dev 类型不同
			// glibc 的 major()/minor() 编码。
			major := uint32((dev>>8)&0xfff | (dev>>32)&^0xfff) //nolint:gosec // 设备号位运算
			minor := uint32((dev & 0xff) | (dev>>12)&^0xff)    //nolint:gosec // 设备号位运算
			return major, minor, nil
		},
	}
}
