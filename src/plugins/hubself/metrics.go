package hubself

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 浸泡指标的数据项键名。
const (
	keyHubRSS        = "hub.rss"
	keyHubCPU        = "hub.cpu"
	keyKioskRSS      = "kiosk.chromium_rss"
	keyHostDiskWrite = "host.disk_written"

	// clockTicks 是 /proc/*/stat 中 utime、stime 的单位（CLK_TCK，树莓派与常见 Linux 恒为 100）。
	clockTicks = 100
	// sectorBytes 是 /sys/block/*/stat 中扇区计数的固定单位，与设备真实扇区大小无关。
	sectorBytes = 512
)

// KioskStats 是可选接口：Stats 的实现同时实现它时，hub-self 输出 kiosk.chromium_rss。
type KioskStats interface {
	// KioskChromiumRSS 返回 kiosk 最近上报的 Chromium 进程组 RSS 字节数；
	// 从未上报、kiosk 离线或上报为空时 ok 为 false（未知，不是 0）。
	KioskChromiumRSS() (bytes int64, ok bool)
}

// Sys 抽象出 /proc、/sys 与数据目录设备号的读取，便于用夹具测试。
type Sys struct {
	ReadFile func(path string) ([]byte, error)
	Readlink func(path string) (string, error)
	// StatDev 返回 path 所在文件系统的设备号（major、minor）。
	StatDev func(path string) (major, minor uint32, err error)
}

var errUnsupported = errors.New("当前平台不支持")

// unsupportedSys 对每次读取都返回错误，指标一律为未知。
func unsupportedSys() Sys {
	return Sys{
		ReadFile: func(string) ([]byte, error) { return nil, errUnsupported },
		Readlink: func(string) (string, error) { return "", errUnsupported },
		StatDev:  func(string) (uint32, uint32, error) { return 0, 0, errUnsupported },
	}
}

// cpuBaseline 是上一次采集的进程 CPU tick 累计值与采集时刻。
type cpuBaseline struct {
	ticks uint64
	at    time.Time
	valid bool
}

// metricItems 返回 4 个浸泡指标数据项；拿不到的指标输出带 error 的无值项（未知，不写 0）。
func (p *Plugin) metricItems(st Stats, now time.Time) []report.Item {
	return []report.Item{
		p.hubRSSItem(),
		p.hubCPUItem(now),
		kioskRSSItem(st),
		p.diskWrittenItem(st.DataDir()),
	}
}

func unknownNumber(key, unit, msg string) report.Item {
	return report.Item{Key: key, Type: report.TypeNumber, Unit: unit, Error: msg}
}

func (p *Plugin) hubRSSItem() report.Item {
	raw, err := p.sys.ReadFile("/proc/self/status")
	if err != nil {
		return unknownNumber(keyHubRSS, "B", "无法读取进程内存 / Cannot read process memory")
	}
	v, err := parseVmRSS(string(raw))
	if err != nil {
		return unknownNumber(keyHubRSS, "B", "无法解析进程内存 / Cannot parse process memory")
	}
	return report.Item{Key: keyHubRSS, Type: report.TypeNumber, Unit: "B", Value: ptr(float64(v))}
}

func (p *Plugin) hubCPUItem(now time.Time) report.Item {
	unknown := func(msg string) report.Item { return unknownNumber(keyHubCPU, "%", msg) }
	raw, err := p.sys.ReadFile("/proc/self/stat")
	if err != nil {
		return unknown("无法读取进程 CPU / Cannot read process CPU")
	}
	ticks, err := parseStatTicks(string(raw))
	if err != nil {
		return unknown("无法解析进程 CPU / Cannot parse process CPU")
	}
	p.mu.Lock()
	prev := p.cpu
	p.cpu = cpuBaseline{ticks: ticks, at: now, valid: true}
	p.mu.Unlock()
	if !prev.valid {
		return unknown("等待下一次采集计算差分 / Waiting for the next sample")
	}
	elapsed := now.Sub(prev.at).Seconds()
	if ticks < prev.ticks || elapsed <= 0 {
		return unknown("CPU 计数异常 / Inconsistent CPU counters")
	}
	pct := float64(ticks-prev.ticks) / clockTicks / elapsed * 100
	return report.Item{Key: keyHubCPU, Type: report.TypeNumber, Unit: "%", Value: ptr(pct)}
}

func kioskRSSItem(st Stats) report.Item {
	if ks, ok := st.(KioskStats); ok {
		if v, ok := ks.KioskChromiumRSS(); ok && v >= 0 {
			return report.Item{Key: keyKioskRSS, Type: report.TypeNumber, Unit: "B", Value: ptr(float64(v))}
		}
	}
	return unknownNumber(keyKioskRSS, "B", "kiosk 未上报 / No kiosk report")
}

func (p *Plugin) diskWrittenItem(dir string) report.Item {
	v, err := p.diskWritten(dir)
	if err != nil {
		return unknownNumber(keyHostDiskWrite, "B", "无法读取磁盘写入量 / Cannot read disk writes")
	}
	return report.Item{Key: keyHostDiskWrite, Type: report.TypeNumber, Unit: "B", Value: ptr(float64(v))}
}

// diskWritten 由数据目录的设备号解析出块设备（分区取其父设备），返回开机以来的累计写入字节数。
func (p *Plugin) diskWritten(dir string) (uint64, error) {
	major, minor, err := p.sys.StatDev(dir)
	if err != nil {
		return 0, err
	}
	node := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	target, err := p.sys.Readlink(node)
	if err != nil {
		return 0, err
	}
	dev := path.Base(target)
	if _, err := p.sys.ReadFile(node + "/partition"); err == nil {
		dev = path.Base(path.Dir(target))
	}
	raw, err := p.sys.ReadFile("/sys/block/" + dev + "/stat")
	if err != nil {
		return 0, err
	}
	sectors, err := parseWrittenSectors(string(raw))
	if err != nil {
		return 0, err
	}
	return sectors * sectorBytes, nil
}

// parseVmRSS 从 /proc/self/status 文本取 VmRSS（kB）并换算为字节。
func parseVmRSS(s string) (uint64, error) {
	for _, line := range strings.Split(s, "\n") {
		rest, ok := strings.CutPrefix(line, "VmRSS:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) != 2 || f[1] != "kB" {
			return 0, errors.New("VmRSS 格式不符")
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	return 0, errors.New("缺少 VmRSS")
}

// parseStatTicks 返回 /proc/self/stat 的 utime+stime（tick）。
// comm 字段可含空格与括号，所以从最后一个 ')' 之后开始切分：其后第 1 个字段是 state（总第 3 项），
// utime、stime 为总第 14、15 项，即切分后的下标 11、12。
func parseStatTicks(s string) (uint64, error) {
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, errors.New("stat 缺少 comm 结束符")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0, errors.New("stat 字段不足")
	}
	ut, err := strconv.ParseUint(f[11], 10, 64)
	if err != nil {
		return 0, err
	}
	st, err := strconv.ParseUint(f[12], 10, 64)
	if err != nil {
		return 0, err
	}
	return ut + st, nil
}

// parseWrittenSectors 返回 /sys/block/<dev>/stat 第 7 列（写入扇区累计）。
func parseWrittenSectors(s string) (uint64, error) {
	f := strings.Fields(s)
	if len(f) < 7 {
		return 0, errors.New("块设备 stat 列数不足")
	}
	return strconv.ParseUint(f[6], 10, 64)
}
