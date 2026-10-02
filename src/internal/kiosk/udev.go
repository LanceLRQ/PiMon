package kiosk

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/devnum"
)

const (
	// DefaultInputDir 是输入设备目录。
	DefaultInputDir = "/dev/input"
	// DefaultUdevDataDir 是 udev 数据库目录，条目名为 c<major>:<minor>。
	DefaultUdevDataDir = "/run/udev/data"
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

// DetectTouch 用真实的 stat 设备号检测触摸屏，见 DetectTouchWith。
func DetectTouch(inputDir, udevDir string) TouchResult {
	return DetectTouchWith(inputDir, udevDir, devnum.StatRdev)
}

// DetectTouchWith 枚举 inputDir 下的 event* 设备，用 rdev 取每个设备节点的 major:minor，
// 读 udevDir/c<major>:<minor> 中是否有 E:ID_INPUT_TOUCHSCREEN=1。
// rdev 失败或 udev 条目读不到的设备视为未知；所有设备都未知时结果为未知。
func DetectTouchWith(inputDir, udevDir string, rdev func(path string) (major, minor uint32, err error)) TouchResult {
	matches, err := filepath.Glob(filepath.Join(inputDir, "event*"))
	if err != nil {
		return TouchResult{}
	}
	sort.Strings(matches)
	var res TouchResult
	for _, dev := range matches {
		if n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(dev), "event")); err != nil || n < 0 {
			continue
		}
		major, minor, err := rdev(dev)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(udevDir, fmt.Sprintf("c%d:%d", major, minor)))
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
