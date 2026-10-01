package hostmetrics

import (
	"context"
	"strings"
)

// Partition 是一个待统计的挂载点。
type Partition struct {
	Mount  string
	FSType string
	Device string
}

// DiskUsage 是某挂载点的容量，单位字节。
type DiskUsage struct {
	Used, Total uint64
}

// Sampler 是 host-metrics 取数的来源，真实实现基于 gopsutil，测试用替身。
// 取不到的量返回错误，插件据此不输出对应数据项，不会写 0。
type Sampler interface {
	// CPUTimes 返回累计的忙碌时间与总时间（任意同一单位）。
	CPUTimes(ctx context.Context) (busy, total float64, err error)
	// Load1 返回 1 分钟平均负载。
	Load1(ctx context.Context) (float64, error)
	// Memory 返回已用与总内存（字节）。
	Memory(ctx context.Context) (used, total uint64, err error)
	// Temperature 返回 CPU 温度（℃）；没有传感器时返回错误。
	Temperature(ctx context.Context) (float64, error)
	// Partitions 列出候选挂载点，过滤由插件完成。
	Partitions(ctx context.Context) ([]Partition, error)
	// Usage 返回挂载点容量。
	Usage(ctx context.Context, mount string) (DiskUsage, error)
	// NetCounters 返回各物理网卡累计收发字节数之和。
	NetCounters(ctx context.Context) (rx, tx uint64, err error)
	// Uptime 返回系统运行秒数。
	Uptime(ctx context.Context) (uint64, error)
	// Throttled 返回树莓派的 get_throttled 位掩码；不是树莓派或读不到时返回错误。
	Throttled(ctx context.Context) (uint32, error)
}

// pseudoFS 是不计入磁盘统计的伪文件系统或容器层。
var pseudoFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "autofs": true, "devfs": true, "fusectl": true, "debugfs": true,
	"tracefs": true, "securityfs": true, "pstore": true, "configfs": true, "mqueue": true, "hugetlbfs": true,
	"binfmt_misc": true, "bpf": true, "ramfs": true, "nsfs": true, "efivarfs": true, "rpc_pipefs": true,
}

// pseudoMountPrefixes 是不计入统计的挂载点前缀（系统目录与容器、快照挂载）。
var pseudoMountPrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/snap", "/var/lib/docker", "/var/lib/containers",
	"/private/var/vm", "/System/Volumes/VM", "/System/Volumes/Preboot", "/System/Volumes/Update",
	"/System/Volumes/xarts", "/System/Volumes/iSCPreboot", "/System/Volumes/Hardware",
}

// realMount 判断一个挂载点是否是值得监控的真实存储。
func realMount(p Partition) bool {
	if p.Mount == "" || pseudoFS[strings.ToLower(p.FSType)] {
		return false
	}
	for _, pre := range pseudoMountPrefixes {
		if p.Mount == pre || strings.HasPrefix(p.Mount, pre+"/") {
			return false
		}
	}
	return true
}
