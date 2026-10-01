package system

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

type fakePlugins struct{ snap plugins.Snapshot }

func (f fakePlugins) Snapshot() plugins.Snapshot { return f.snap }
func (fakePlugins) Dir() string                  { return "/var/lib/pimon/plugins" }

func newSvc(t *testing.T, mod func(*Config)) (*Service, *clock.Fake, string) {
	t.Helper()
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cfg := Config{
		Clock: clk, Version: "v-test", DataDir: dir,
		Plugins: fakePlugins{plugins.Snapshot{
			Plugins: []plugins.Plugin{{ID: "a", Origin: plugins.OriginBuiltin}, {ID: "b", Origin: plugins.OriginBuiltin}, {ID: "c", Origin: plugins.OriginExec}},
			Issues:  []plugins.Issue{{Kind: plugins.IssueInvalidManifest}, {Kind: plugins.IssueConflict}},
		}},
		ProcWriteBytes: func() (int64, bool) { return 0, false },
		Memory:         func() (int64, int64) { return 1000, 400 },
	}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg), clk, dir
}

func TestInfoBasics(t *testing.T) {
	s, clk, dir := newSvc(t, nil)
	clk.Advance(90 * time.Second)
	info := s.Info()
	if info.Version != "v-test" || info.UptimeSeconds != 90 || !info.StartedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("版本或运行时长不对: %+v", info)
	}
	if info.GoVersion == "" || info.OS == "" || info.Arch == "" {
		t.Fatalf("缺运行时信息: %+v", info)
	}
	if info.Memory.SysBytes != 1000 || info.Memory.HeapBytes != 400 {
		t.Fatalf("内存: %+v", info.Memory)
	}
	if info.DataDir.Path != dir {
		t.Fatalf("数据目录: %+v", info.DataDir)
	}
	p := info.Plugins
	if p.Total != 3 || p.Builtin != 2 || p.Exec != 1 || p.Errors != 1 || p.Conflicts != 1 || p.PluginDir != "/var/lib/pimon/plugins" {
		t.Fatalf("插件统计: %+v", p)
	}
}

func TestDiskWritesNullWhenUnsupported(t *testing.T) {
	s, _, _ := newSvc(t, nil)
	if w := s.Info().DiskWrites; w != nil {
		t.Fatalf("不支持的平台应为 nil，得到 %+v", w)
	}
}

func TestDiskWritesWithinFirstDay(t *testing.T) {
	s, clk, _ := newSvc(t, func(c *Config) { c.ProcWriteBytes = func() (int64, bool) { return 5000, true } })
	clk.Advance(2 * time.Hour)
	w := s.Info().DiskWrites
	if w == nil || w.SinceStartBytes != 5000 || w.Last24hBytes != 5000 || w.Last24hEstimated {
		t.Fatalf("不足 24 小时: %+v", w)
	}
}

func TestDiskWritesEstimatedAfterFirstDay(t *testing.T) {
	s, clk, _ := newSvc(t, func(c *Config) { c.ProcWriteBytes = func() (int64, bool) { return 4800, true } })
	clk.Advance(48 * time.Hour)
	w := s.Info().DiskWrites
	if w == nil || w.SinceStartBytes != 4800 || w.Last24hBytes != 2400 || !w.Last24hEstimated {
		t.Fatalf("应按平均速率折算: %+v", w)
	}
}

func TestDataDirUsageSumsAndCaches(t *testing.T) {
	s, clk, dir := newSvc(t, nil)
	write := func(name string, n int) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.db", 100)
	write("backups/b.tar.gz", 50)
	used := func() int64 {
		u := s.Info().DataDir.UsedBytes
		if u == nil {
			t.Fatal("UsedBytes 不应为 nil")
		}
		return *u
	}
	if got := used(); got != 150 {
		t.Fatalf("用量 = %d", got)
	}
	write("c", 10)
	clk.Advance(59 * time.Second)
	if got := used(); got != 150 {
		t.Fatalf("60 秒内应命中缓存，得到 %d", got)
	}
	clk.Advance(2 * time.Second)
	if got := used(); got != 160 {
		t.Fatalf("缓存过期后应重算，得到 %d", got)
	}
}

func TestDataDirUsageUnknownWhenMissing(t *testing.T) {
	s, _, _ := newSvc(t, func(c *Config) { c.DataDir = filepath.Join(os.TempDir(), "pimon-no-such-dir-xyz") })
	if u := s.Info().DataDir.UsedBytes; u != nil {
		t.Fatalf("目录不存在应为 nil，得到 %d", *u)
	}
}

func TestLogsFilterAndNilRing(t *testing.T) {
	ring := logging.NewRing(5)
	s, _, _ := newSvc(t, func(c *Config) { c.Ring = ring })
	lg := slog.New(ring.Handler(slog.LevelDebug))
	lg.Info("i")
	lg.Warn("w")
	lg.Error("e")
	l := s.Logs(slog.LevelWarn, 10)
	if l.Capacity != 5 || len(l.Entries) != 2 || l.Entries[0].Level != "warn" || l.Entries[1].Level != "error" || l.Entries[1].Message != "e" {
		t.Fatalf("日志: %+v", l)
	}
	empty, _, _ := newSvc(t, nil)
	if l := empty.Logs(slog.LevelDebug, 10); l.Capacity != 0 || l.Entries == nil || len(l.Entries) != 0 {
		t.Fatalf("无缓冲应返回空数组: %+v", l)
	}
}

func TestParseProcIOWriteBytes(t *testing.T) {
	in := "rchar: 1\nwchar: 2\nsyscr: 3\nsyscw: 4\nread_bytes: 5\nwrite_bytes: 4096\ncancelled_write_bytes: 0\n"
	if n, ok := parseWriteBytes(in); !ok || n != 4096 {
		t.Fatalf("n=%d ok=%v", n, ok)
	}
	if _, ok := parseWriteBytes("rchar: 1\n"); ok {
		t.Fatal("缺 write_bytes 应为未知")
	}
	if _, ok := parseWriteBytes("write_bytes: abc\n"); ok {
		t.Fatal("非数字应为未知")
	}
}

func TestDataDirUsageConcurrentRequestsShareOneWalk(t *testing.T) {
	s, _, dir := newSvc(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 7), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if u := s.Info().DataDir.UsedBytes; u == nil || *u != 7 {
				t.Errorf("用量不对: %v", u)
			}
		}()
	}
	wg.Wait()
}
