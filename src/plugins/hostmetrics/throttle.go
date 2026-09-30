package hostmetrics

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 树莓派 get_throttled 的位含义（当前状态为低 4 位，历史状态为 16–19 位）。
const (
	bitUnderVoltage = 1 << 0
	bitFreqCapped   = 1 << 1
	bitThrottled    = 1 << 2
	bitSoftTempCap  = 1 << 3
	currentMask     = 0xF
	historyShift    = 16
)

// sysfsThrottled 是内核暴露的 get_throttled 文件。
const sysfsThrottled = "/sys/devices/platform/soc/soc:firmware/get_throttled"

// throttleIO 是读取欠压标志用到的外部能力，测试用替身。
type throttleIO struct {
	run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	readFile func(path string) ([]byte, error)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// readThrottled 先试 vcgencmd，再试 sysfs 文件；都没有时返回错误，调用方不输出该项。
func readThrottled(ctx context.Context, tio throttleIO) (uint32, error) {
	if out, err := tio.run(ctx, "vcgencmd", "get_throttled"); err == nil {
		if v, err := parseThrottled(string(out)); err == nil {
			return v, nil
		}
	}
	if out, err := tio.readFile(sysfsThrottled); err == nil {
		if v, err := parseThrottled(string(out)); err == nil {
			return v, nil
		}
	}
	return 0, errors.New("无法读取 get_throttled")
}

// parseThrottled 解析 "throttled=0x50005"、"0x50005" 或 sysfs 的无前缀十六进制 "50005"。
func parseThrottled(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "throttled=")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// throttleState 解释位掩码：当前有欠压或降频为 warning，只有历史记录为 ok 但在文字里注明。
func throttleState(v uint32) (st report.Status, text string) {
	cur := v & currentMask
	hist := (v >> historyShift) & currentMask
	describe := func(bits uint32) string {
		var parts []string
		for _, b := range []struct {
			bit  uint32
			name string
		}{
			{bitUnderVoltage, "欠压 under-voltage"},
			{bitFreqCapped, "频率受限 freq-capped"},
			{bitThrottled, "降频 throttled"},
			{bitSoftTempCap, "软温度限制 soft-temp-limit"},
		} {
			if bits&b.bit != 0 {
				parts = append(parts, b.name)
			}
		}
		return strings.Join(parts, "、")
	}
	switch {
	case cur != 0:
		return report.StatusWarning, describe(cur)
	case hist != 0:
		return report.StatusOK, "曾出现 / occurred since boot: " + describe(hist)
	}
	return report.StatusOK, "正常 / OK"
}
