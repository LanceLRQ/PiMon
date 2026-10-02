package kiosk

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// DefaultInputDir 是输入设备目录。
	DefaultInputDir = "/dev/input"
	// DefaultUdevDataDir 是 udev 数据库目录，条目名为 c<major>:<minor>。
	DefaultUdevDataDir = "/run/udev/data"
	// inputMajor 是 evdev 字符设备的主设备号；eventN 的次设备号是 evdevMinorBase+N。
	inputMajor     = 13
	evdevMinorBase = 64
)

// TouchResult 是触摸屏检测结果。
type TouchResult struct {
	// Known 为 false 表示读不到 udev 数据库，无法判断。
	Known bool
	// Touch 表示发现了带 ID_INPUT_TOUCHSCREEN=1 的输入设备（仅 Known 时有意义）。
	Touch bool
	// Devices 是触摸设备的 /dev/input/eventN 路径，已排序。
	Devices []string
}

// Touchscreen 转成上报用的 bool|null：未知为 nil。
func (r TouchResult) Touchscreen() *bool {
	if !r.Known {
		return nil
	}
	v := r.Touch
	return &v
}

// DetectTouch 枚举 inputDir 下的 event* 设备，读 udevDir/c13:<minor> 中是否有
// E:ID_INPUT_TOUCHSCREEN=1。一个设备的 udev 条目都读不到时结果为未知。
func DetectTouch(inputDir, udevDir string) TouchResult {
	matches, err := filepath.Glob(filepath.Join(inputDir, "event*"))
	if err != nil {
		return TouchResult{}
	}
	sort.Strings(matches)
	var res TouchResult
	for _, dev := range matches {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(dev), "event"))
		if err != nil || n < 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(udevDir, fmt.Sprintf("c%d:%d", inputMajor, evdevMinorBase+n)))
		if err != nil {
			continue
		}
		res.Known = true
		if hasTouchProperty(string(data)) {
			res.Touch = true
			res.Devices = append(res.Devices, dev)
		}
	}
	return res
}

func hasTouchProperty(udevData string) bool {
	for _, line := range strings.Split(udevData, "\n") {
		if strings.TrimSpace(line) == "E:ID_INPUT_TOUCHSCREEN=1" {
			return true
		}
	}
	return false
}
