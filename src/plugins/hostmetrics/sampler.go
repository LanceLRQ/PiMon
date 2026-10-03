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
	// ReadOnly 表示以只读方式挂载（挂载选项含 ro），例如 macOS 的密封系统卷与磁盘映像。
	ReadOnly bool
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
	// 网络文件系统：容量属于远端，不代表本机存储。
	"smbfs": true, "nfs": true, "nfs3": true, "nfs4": true, "cifs": true, "smb3": true, "afpfs": true,
	"webdav": true, "fuse.sshfs": true, "9p": true,
}

// pseudoMountPrefixes 是不计入统计的挂载点前缀（系统目录与容器、快照挂载）。
var pseudoMountPrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/snap", "/boot/efi", "/var/lib/docker", "/var/lib/containers",
	"/private/var/vm", "/System/Volumes/VM", "/System/Volumes/Preboot", "/System/Volumes/Update",
	"/System/Volumes/xarts", "/System/Volumes/iSCPreboot", "/System/Volumes/Hardware",
}

// realMount 判断一个挂载点是否是值得监控的真实存储。
// 只读挂载（macOS 的密封系统卷、磁盘映像）不计入；macOS 的 /System/Volumes/Data 是真实数据卷，
// 它是读写挂载，不在前缀表里，因此保留。
func realMount(p Partition) bool {
	if p.Mount == "" || p.ReadOnly || pseudoFS[strings.ToLower(p.FSType)] {
		return false
	}
	for _, pre := range pseudoMountPrefixes {
		if p.Mount == pre || strings.HasPrefix(p.Mount, pre+"/") {
			return false
		}
	}
	return true
}
