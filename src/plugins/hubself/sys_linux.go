//go:build linux

package hubself

import (
	"os"

	"github.com/LanceLRQ/PiMon/src/pkg/devnum"
)

// defaultSys 读取真实的 /proc、/sys 与数据目录设备号。
func defaultSys() Sys {
	return Sys{
		ReadFile: os.ReadFile,
		Readlink: os.Readlink,
		StatDev:  devnum.StatDev,
	}
}
