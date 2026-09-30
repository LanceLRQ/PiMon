package hostmetrics

import (
	"context"
	"errors"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
)

// systemSampler 是基于 gopsutil 的真实取数来源。
type systemSampler struct{ tio throttleIO }

func newSystemSampler(tio throttleIO) Sampler { return &systemSampler{tio: tio} }

func (s *systemSampler) CPUTimes(ctx context.Context) (busy, total float64, err error) {
	ts, err := cpu.TimesWithContext(ctx, false)
	if err != nil || len(ts) == 0 {
		return 0, 0, errors.New("无法读取 CPU 时间")
	}
	t := ts[0]
	idle := t.Idle + t.Iowait
	total = t.User + t.System + t.Nice + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal
	return total - idle, total, nil
}

func (s *systemSampler) Load1(ctx context.Context) (float64, error) {
	a, err := load.AvgWithContext(ctx)
	if err != nil {
		return 0, err
	}
	return a.Load1, nil
}

func (s *systemSampler) Memory(ctx context.Context) (used, total uint64, err error) {
	v, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return 0, 0, err
	}
	return v.Used, v.Total, nil
}

// tempSensorHints 是优先采用的传感器名关键词（CPU/SoC 温度）。
var tempSensorHints = []string{"cpu", "soc", "coretemp", "k10temp", "thermal", "package"}

func (s *systemSampler) Temperature(ctx context.Context) (float64, error) {
	ts, _ := sensors.TemperaturesWithContext(ctx) // 部分传感器失败时仍可能有可用读数
	return pickTemperature(ts)
}

// pickTemperature 优先取名字像 CPU 的传感器，否则取第一个有效读数；没有则报错。
func pickTemperature(ts []sensors.TemperatureStat) (float64, error) {
	var first *float64
	for i := range ts {
		if ts[i].Temperature <= 0 {
			continue
		}
		if first == nil {
			first = &ts[i].Temperature
		}
		key := strings.ToLower(ts[i].SensorKey)
		for _, h := range tempSensorHints {
			if strings.Contains(key, h) {
				return ts[i].Temperature, nil
			}
		}
	}
	if first == nil {
		return 0, errors.New("没有温度传感器")
	}
	return *first, nil
}

func (s *systemSampler) Partitions(ctx context.Context) ([]Partition, error) {
	ps, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]Partition, 0, len(ps))
	for _, p := range ps {
		out = append(out, Partition{Mount: p.Mountpoint, FSType: p.Fstype, Device: p.Device})
	}
	return out, nil
}

func (s *systemSampler) Usage(ctx context.Context, mount string) (DiskUsage, error) {
	u, err := disk.UsageWithContext(ctx, mount)
	if err != nil {
		return DiskUsage{}, err
	}
	return DiskUsage{Used: u.Used, Total: u.Total}, nil
}

// virtualNICPrefixes 是不计入网速的回环与虚拟网卡名前缀，避免容器流量被重复统计。
var virtualNICPrefixes = []string{"lo", "docker", "veth", "br-", "virbr", "cni", "flannel", "utun", "awdl", "llw", "bridge", "gif", "stf", "ap"}

func (s *systemSampler) NetCounters(ctx context.Context) (rx, tx uint64, err error) {
	stats, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil || len(stats) == 0 {
		return 0, 0, errors.New("无法读取网卡计数器")
	}
	rx, tx, ok := sumNIC(stats)
	if !ok {
		return 0, 0, errors.New("没有可统计的网卡")
	}
	return rx, tx, nil
}

// sumNIC 累加物理网卡的收发字节数。
func sumNIC(stats []gnet.IOCountersStat) (rx, tx uint64, ok bool) {
	for _, st := range stats {
		virtual := false
		for _, p := range virtualNICPrefixes {
			if strings.HasPrefix(st.Name, p) {
				virtual = true
				break
			}
		}
		if virtual {
			continue
		}
		rx, tx, ok = rx+st.BytesRecv, tx+st.BytesSent, true
	}
	return rx, tx, ok
}

func (s *systemSampler) Uptime(ctx context.Context) (uint64, error) {
	return host.UptimeWithContext(ctx)
}

func (s *systemSampler) Throttled(ctx context.Context) (uint32, error) {
	return readThrottled(ctx, s.tio)
}
