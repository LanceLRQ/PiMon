// Package system 汇总中枢自身的运行信息（版本、运行时长、资源占用、插件统计）与内存日志，
// 供管理界面的系统页使用。
package system

import (
	"io/fs"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// dataDirTTL 是数据目录用量的缓存时长，避免每次刷新都遍历磁盘。
const dataDirTTL = 60 * time.Second

// day 是「近 24 小时」的窗口。
const day = 24 * time.Hour

// PluginSource 是插件注册表中系统页需要的部分。
type PluginSource interface {
	Snapshot() plugins.Snapshot
	Dir() string
}

// Config 是 Service 的依赖。
type Config struct {
	Clock   clock.Clock
	Version string
	DataDir string
	Plugins PluginSource
	// Ring 是内存日志缓冲，为 nil 时日志接口返回空列表。
	Ring *logging.Ring
	// ProcWriteBytes 返回进程自启动以来的磁盘写入字节数，ok=false 表示本平台无法得知；
	// 缺省按平台读取（Linux 读 /proc/self/io）。
	ProcWriteBytes func() (bytes int64, ok bool)
	// Memory 返回 Go 运行时的 Sys 与 HeapAlloc；缺省读 runtime.MemStats。
	Memory func() (sys, heap int64)
}

// Service 提供系统页的数据。
type Service struct {
	cfg     Config
	started time.Time

	mu       sync.Mutex
	usedAt   time.Time
	used     *int64
	usedDone bool
}

// New 创建 Service，启动时刻取自注入的时钟。
func New(c Config) *Service {
	if c.ProcWriteBytes == nil {
		c.ProcWriteBytes = procWriteBytes
	}
	if c.Memory == nil {
		c.Memory = runtimeMemory
	}
	return &Service{cfg: c, started: c.Clock.Now()}
}

func runtimeMemory() (sys, heap int64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.Sys), int64(m.HeapAlloc) //nolint:gosec // 内存字节数远小于 int64 上限
}

// Info 返回当前的系统信息。
func (s *Service) Info() model.SystemInfo {
	now := s.cfg.Clock.Now()
	uptime := now.Sub(s.started)
	sys, heap := s.cfg.Memory()
	info := model.SystemInfo{
		Version:       s.cfg.Version,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		StartedAt:     s.started,
		UptimeSeconds: int64(uptime / time.Second),
		Memory:        model.SystemMemory{SysBytes: sys, HeapBytes: heap},
		DataDir:       model.SystemDataDir{Path: s.cfg.DataDir, UsedBytes: s.dataDirUsed(now)},
		DiskWrites:    s.diskWrites(uptime),
		Plugins:       s.pluginStats(),
	}
	return info
}

func (s *Service) diskWrites(uptime time.Duration) *model.SystemDiskWrites {
	total, ok := s.cfg.ProcWriteBytes()
	if !ok {
		return nil
	}
	w := &model.SystemDiskWrites{SinceStartBytes: total, Last24hBytes: total}
	if uptime > day {
		// 没有按小时的采样，超过一天后按启动以来的平均速率折算。
		w.Last24hBytes = int64(float64(total) * float64(day) / float64(uptime))
		w.Last24hEstimated = true
	}
	return w
}

func (s *Service) pluginStats() model.SystemPlugins {
	out := model.SystemPlugins{PluginDir: s.cfg.Plugins.Dir()}
	snap := s.cfg.Plugins.Snapshot()
	out.Total = len(snap.Plugins)
	for _, p := range snap.Plugins {
		if p.Origin == plugins.OriginExec {
			out.Exec++
		} else {
			out.Builtin++
		}
	}
	for _, is := range snap.Issues {
		if is.Kind == plugins.IssueConflict {
			out.Conflicts++
		} else {
			out.Errors++
		}
	}
	return out
}

// dataDirUsed 返回数据目录用量，结果缓存 dataDirTTL；目录不可读时为 nil（未知）。
func (s *Service) dataDirUsed(now time.Time) *int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usedDone && now.Sub(s.usedAt) < dataDirTTL {
		return s.used
	}
	s.used, s.usedAt, s.usedDone = dirSize(s.cfg.DataDir), now, true
	return s.used
}

func dirSize(root string) *int64 {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			// 遍历中途消失或无权读取的文件不影响整体统计。
			return nil
		}
		if d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return &total
}

// Logs 返回级别不低于 min 的最近 limit 条日志。
func (s *Service) Logs(min slog.Level, limit int) model.LogList {
	out := model.LogList{Entries: []model.LogEntry{}}
	if s.cfg.Ring == nil {
		return out
	}
	out.Capacity = s.cfg.Ring.Capacity()
	for _, e := range s.cfg.Ring.Entries(min, limit) {
		out.Entries = append(out.Entries, model.LogEntry{Time: e.Time, Level: levelName(e.Level), Message: e.Message, Attrs: e.Attrs})
	}
	return out
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// parseWriteBytes 从 /proc/self/io 的内容里取 write_bytes。
func parseWriteBytes(content string) (int64, bool) {
	for _, line := range strings.Split(content, "\n") {
		k, v, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(k) != "write_bytes" {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
