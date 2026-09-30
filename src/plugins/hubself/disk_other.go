//go:build !unix

package hubself

import "errors"

// statDisk 在非 unix 平台不可用；中枢只面向 linux 与 darwin，这里仅保证可编译。
func statDisk(string) (free, total uint64, err error) {
	return 0, 0, errors.New("当前平台不支持查询磁盘空间")
}
