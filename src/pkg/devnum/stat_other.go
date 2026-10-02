//go:build !linux

package devnum

import "errors"

var errUnsupported = errors.New("当前平台不支持读取设备号")

// StatDev 在非 Linux 平台不可用。
func StatDev(string) (major, minor uint32, err error) { return 0, 0, errUnsupported }

// StatRdev 在非 Linux 平台不可用。
func StatRdev(string) (major, minor uint32, err error) { return 0, 0, errUnsupported }
