package model

import "time"

// SystemInfo 是 GET /api/system 的响应：中枢版本、运行时长、资源占用与插件统计。
// 取不到的数据用 null 表示未知，不当作零。
type SystemInfo struct {
	// Version 是构建版本，与前端 snapshot 下发的 build 同源。
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	// StartedAt 是本次启动时刻；UptimeSeconds 是已运行秒数。
	StartedAt     time.Time         `json:"started_at"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	Memory        SystemMemory      `json:"memory"`
	DataDir       SystemDataDir     `json:"data_dir"`
	DiskWrites    *SystemDiskWrites `json:"disk_writes" tstype:"SystemDiskWrites | null,required"`
	Plugins       SystemPlugins     `json:"plugins"`
}

// SystemMemory 是中枢进程的内存占用（Go 运行时统计），单位字节。
type SystemMemory struct {
	// SysBytes 是向操作系统申请的内存总量（runtime.MemStats.Sys）。
	SysBytes int64 `json:"sys_bytes"`
	// HeapBytes 是已分配的堆内存（runtime.MemStats.HeapAlloc）。
	HeapBytes int64 `json:"heap_bytes"`
}

// SystemDataDir 是数据目录及其占用。
type SystemDataDir struct {
	Path string `json:"path"`
	// UsedBytes 是目录下全部文件大小之和（缓存 60 秒）；遍历失败时为 null。
	UsedBytes *int64 `json:"used_bytes" tstype:"number | null,required"`
}

// SystemDiskWrites 是中枢进程的磁盘写入量，只在能读取进程 IO 统计的平台（Linux）提供。
type SystemDiskWrites struct {
	// SinceStartBytes 是自启动以来的写入字节数。
	SinceStartBytes int64 `json:"since_start_bytes"`
	// Last24hBytes 是近 24 小时写入字节数；运行不足 24 小时等于 SinceStartBytes，
	// 超过 24 小时按启动以来的平均速率折算（Last24hEstimated 为 true）。
	Last24hBytes     int64 `json:"last_24h_bytes"`
	Last24hEstimated bool  `json:"last_24h_estimated"`
}

// SystemPlugins 是插件统计。
type SystemPlugins struct {
	Total     int `json:"total"`
	Builtin   int `json:"builtin"`
	Exec      int `json:"exec"`
	Errors    int `json:"errors"`
	Conflicts int `json:"conflicts"`
	// PluginDir 是 exec 插件目录的绝对路径。
	PluginDir string `json:"plugin_dir"`
}

// LogEntry 是中枢内存日志缓冲里的一条日志。
type LogEntry struct {
	Time time.Time `json:"time"`
	// Level 是 debug | info | warn | error。
	Level   string `json:"level"`
	Message string `json:"message"`
	// Attrs 是 key=value 属性文本（含 mono 单调时长），敏感键的值已替换为 ***。
	Attrs string `json:"attrs"`
}

// LogList 是 GET /api/system/logs 的响应，按时间正序。
type LogList struct {
	Entries []LogEntry `json:"entries"`
	// Capacity 是缓冲容量（最多保留的条数）。
	Capacity int `json:"capacity"`
}
