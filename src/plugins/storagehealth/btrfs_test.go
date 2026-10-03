package storagehealth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/plugintest"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

const fnosMapper = "/dev/mapper/trim_5d1a0f0e_7c3b_2a19_8f4e_6d210a9b3c77-0"

func btrfsOnly(cfg map[string]any) map[string]any {
	out := map[string]any{"smart": false, "raid": false}
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

// scrubStart 是样本里 scrub 的开始时间（按 UTC 解析）。
var scrubStart = time.Date(2025, 9, 30, 3, 0, 1, 0, time.UTC)

func btrfsInput(t *testing.T, cfg map[string]any, now time.Time, last *report.Report) runtime.Input {
	in := input(t, btrfsOnly(cfg), last)
	in.Clock = clock.NewFake(now)
	return in
}

func setBtrfs(t *testing.T, f *cmdexec.Fake, op sudocmd.Op, dev string, res cmdexec.Result, err error) {
	t.Helper()
	f.Set(res, err, sudoPath, sudoArgsFor(t, btrfsPath, op, dev)...)
}

// fnosBtrfs 返回挂载了飞牛 btrfs（/dev/mapper → /dev/dm-0）的插件，stats 与 scrub 输出由样本文件给出。
func fnosBtrfs(t *testing.T, f *cmdexec.Fake, stats, scrub string) *Plugin {
	t.Helper()
	p := newLinux(f)
	p.readFile = fakeFiles{"/proc/mounts": string(readTestdata(t, "mounts-fnos.txt"))}.read
	base := p.evalSymlinks
	p.evalSymlinks = func(s string) (string, error) {
		if s == fnosMapper {
			return "/dev/dm-0", nil
		}
		return base(s)
	}
	setBtrfs(t, f, sudocmd.OpBtrfsDeviceStats, "/dev/dm-0", sample(t, stats, 0), nil)
	setBtrfs(t, f, sudocmd.OpBtrfsScrubStatus, "/dev/dm-0", sample(t, scrub, 0), nil)
	return p
}

func TestBtrfs挂载去重取最短路径(t *testing.T) {
	p := newLinux(cmdexec.NewFake())
	base := p.evalSymlinks
	p.evalSymlinks = func(s string) (string, error) {
		if s == fnosMapper {
			return "/dev/dm-0", nil
		}
		return base(s)
	}
	fss := p.groupBtrfs(parseBtrfsMounts(string(readTestdata(t, "mounts-fnos.txt"))))
	if len(fss) != 1 || fss[0].point != "/vol1" || fss[0].dev != "/dev/dm-0" || fss[0].err != "" {
		t.Fatalf("fss = %+v", fss)
	}
	// 顺序颠倒、同长度时按字典序。
	mounts := []btrfsMount{{fnosMapper, "/vol1/@docker"}, {"/dev/dm-0", "/b"}, {fnosMapper, "/a"}, {"/dev/sdb1", "/data"}}
	fss = p.groupBtrfs(mounts)
	if len(fss) != 2 || fss[0].point != "/a" || fss[1].point != "/data" {
		t.Fatalf("fss = %+v", fss)
	}
	if got := parseBtrfsMounts(string(readTestdata(t, "mounts-fnos.txt")))[2].point; got != "/var/lib/my data" {
		t.Errorf("挂载点转义 = %q", got)
	}
	if len(parseBtrfsMounts(string(readTestdata(t, "mounts-ubuntu.txt")))) != 0 {
		t.Error("ext4 不应识别为 btrfs")
	}
}

func TestCollectBtrfs飞牛正常(t *testing.T) {
	f := cmdexec.NewFake()
	p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
	rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart.Add(3*24*time.Hour), nil))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	if len(rep.Items) != 2 {
		t.Fatalf("同一文件系统只应有一组条目: %v", rep.Items)
	}
	if it := m["btrfs[/vol1]"]; it.State != report.StatusOK || it.Error != "" {
		t.Errorf("btrfs = %+v", it)
	}
	if it := m["scrub[/vol1]"]; it.State != report.StatusOK || it.Error != "" || !strings.Contains(it.Text, "2025-09-30") {
		t.Errorf("scrub = %+v", it)
	}
	if rep.Status != report.StatusOK || !strings.Contains(rep.Summary, "Btrfs 1 个") {
		t.Errorf("status=%s summary=%q", rep.Status, rep.Summary)
	}
	for _, c := range f.Calls() {
		if c.Args[2] != btrfsPath || c.Args[len(c.Args)-1] != "/dev/dm-0" {
			t.Errorf("调用应经 dm-N 与 btrfs 绝对路径: %v", c)
		}
	}
}

func TestCollectBtrfs设备错误计数为critical(t *testing.T) {
	f := cmdexec.NewFake()
	p := fnosBtrfs(t, f, "btrfs-device-stats-errors.txt", "btrfs-scrub-new-ok.txt")
	rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart.Add(time.Hour), nil))
	if err != nil {
		t.Fatal(err)
	}
	it := items(rep)["btrfs[/vol1]"]
	if it.State != report.StatusCritical || !strings.Contains(it.Text, "read_io_errs=3") || !strings.Contains(it.Text, "corruption_errs=12") || strings.Contains(it.Text, "write_io_errs") {
		t.Errorf("btrfs = %+v", it)
	}
	if rep.Status != report.StatusCritical {
		t.Errorf("status = %s", rep.Status)
	}
}

func TestCollectBtrfsScrub各格式(t *testing.T) {
	cases := []struct {
		file   string
		state  report.Status
		substr string
	}{
		{"btrfs-scrub-new-ok.txt", report.StatusOK, "无错误"},
		{"btrfs-scrub-new-errors.txt", report.StatusCritical, "csum=2"},
		{"btrfs-scrub-new-aborted.txt", report.StatusWarning, "未完成"},
		{"btrfs-scrub-old-ok.txt", report.StatusOK, "无错误"},
		{"btrfs-scrub-old-errors.txt", report.StatusCritical, "2 个错误"},
		{"btrfs-scrub-never.txt", report.StatusWarning, "从未执行"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			f := cmdexec.NewFake()
			p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", c.file)
			rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart.Add(24*time.Hour), nil))
			if err != nil {
				t.Fatal(err)
			}
			it := items(rep)["scrub[/vol1]"]
			if it.State != c.state || it.Error != "" || !strings.Contains(it.Text, c.substr) {
				t.Errorf("scrub = %+v", it)
			}
		})
	}
}

func TestCollectBtrfsScrub过期阈值边界(t *testing.T) {
	cases := []struct {
		name  string
		cfg   map[string]any
		after time.Duration
		want  report.Status
	}{
		{"恰好 35 天不过期", nil, 35 * 24 * time.Hour, report.StatusOK},
		{"超过 35 天一秒", nil, 35*24*time.Hour + time.Second, report.StatusWarning},
		{"自定义阈值 7 天内", map[string]any{"scrub_max_age_days": 7.0}, 7 * 24 * time.Hour, report.StatusOK},
		{"自定义阈值 7 天外", map[string]any{"scrub_max_age_days": 7.0}, 8 * 24 * time.Hour, report.StatusWarning},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cmdexec.NewFake()
			p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
			rep, err := p.Collect(context.Background(), btrfsInput(t, c.cfg, scrubStart.Add(c.after), nil))
			if err != nil {
				t.Fatal(err)
			}
			if it := items(rep)["scrub[/vol1]"]; it.State != c.want {
				t.Errorf("scrub = %+v，期望 %s", it, c.want)
			}
		})
	}
}

func TestCollectBtrfsScrub时间按注入时区解析(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)
	started := time.Date(2025, 9, 30, 3, 0, 1, 0, cst) // 打印的本地时间是 03:00:01 +08:00
	for _, c := range []struct {
		after time.Duration
		want  report.Status
	}{{35 * 24 * time.Hour, report.StatusOK}, {35*24*time.Hour + time.Second, report.StatusWarning}} {
		f := cmdexec.NewFake()
		p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
		p.loc = cst
		rep, err := p.Collect(context.Background(), btrfsInput(t, nil, started.Add(c.after), nil))
		if err != nil {
			t.Fatal(err)
		}
		if it := items(rep)["scrub[/vol1]"]; it.State != c.want {
			t.Errorf("after=%v：%+v，期望 %s", c.after, it, c.want)
		}
	}
}

func TestCollectBtrfsScrub时间无法解析为未知(t *testing.T) {
	f := cmdexec.NewFake()
	p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
	setBtrfs(t, f, sudocmd.OpBtrfsScrubStatus, "/dev/dm-0", cmdexec.Result{Stdout: []byte("Scrub started:    someday\nStatus:           finished\nError summary:    no errors found\n")}, nil)
	rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart, nil))
	if err != nil {
		t.Fatal(err)
	}
	if it := items(rep)["scrub[/vol1]"]; it.State != report.StatusUnknown || !strings.Contains(it.Text, "无法解析 scrub 时间") {
		t.Errorf("scrub = %+v", it)
	}
}

func TestCollectBtrfs命令失败为条目error(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *cmdexec.Fake, p *Plugin)
		want  string
	}{
		{"无 sudo 权限", func(f *cmdexec.Fake, _ *Plugin) {
			setBtrfs(t, f, sudocmd.OpBtrfsDeviceStats, "/dev/dm-0", sudoDenied, nil)
			setBtrfs(t, f, sudocmd.OpBtrfsScrubStatus, "/dev/dm-0", sudoDenied, nil)
		}, "无 sudo 权限"},
		{"超时", func(f *cmdexec.Fake, _ *Plugin) {
			setBtrfs(t, f, sudocmd.OpBtrfsDeviceStats, "/dev/dm-0", cmdexec.Result{}, context.DeadlineExceeded)
			setBtrfs(t, f, sudocmd.OpBtrfsScrubStatus, "/dev/dm-0", cmdexec.Result{}, context.DeadlineExceeded)
		}, "超时"},
		{"没有 btrfs", func(_ *cmdexec.Fake, p *Plugin) {
			p.resolve = func(string) (string, error) { return "", sudocmd.ErrNotFound }
		}, "未安装 btrfs"},
		{"非零退出", func(f *cmdexec.Fake, _ *Plugin) {
			setBtrfs(t, f, sudocmd.OpBtrfsDeviceStats, "/dev/dm-0", cmdexec.Result{ExitCode: 1, Stderr: "ERROR: boom"}, nil)
			setBtrfs(t, f, sudocmd.OpBtrfsScrubStatus, "/dev/dm-0", cmdexec.Result{ExitCode: 1, Stderr: "ERROR: boom"}, nil)
		}, "退出码 1"},
		{"输出无法解析", func(f *cmdexec.Fake, _ *Plugin) {
			setBtrfs(t, f, sudocmd.OpBtrfsDeviceStats, "/dev/dm-0", cmdexec.Result{Stdout: []byte("garbage")}, nil)
			setBtrfs(t, f, sudocmd.OpBtrfsScrubStatus, "/dev/dm-0", cmdexec.Result{Stdout: []byte("garbage")}, nil)
		}, "无法解析"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cmdexec.NewFake()
			p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
			c.setup(f, p)
			rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart, nil))
			if err != nil {
				t.Fatal(err)
			}
			m := items(rep)
			for _, k := range []string{"btrfs[/vol1]", "scrub[/vol1]"} {
				if it := m[k]; it.State != report.StatusUnknown || !strings.Contains(it.Error, c.want) {
					t.Errorf("%s = %+v", k, it)
				}
			}
			if rep.Status != report.StatusWarning {
				t.Errorf("status = %s", rep.Status)
			}
		})
	}
}

func TestCollectBtrfs设备路径问题分开说明(t *testing.T) {
	f := cmdexec.NewFake()
	p := newLinux(f)
	p.readFile = fakeFiles{"/proc/mounts": "/dev/disk/by-uuid/zz /vol1 btrfs rw 0 0\n/dev/loop3 /vol2 btrfs rw 0 0\nbtrfs /vol3 btrfs rw 0 0\n"}.read
	rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart, nil))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if e := m["btrfs[/vol1]"].Error; !strings.Contains(e, "设备不存在或无法解析") || strings.Contains(e, "不支持") {
		t.Errorf("vol1 = %q", e)
	}
	for _, k := range []string{"btrfs[/vol2]", "scrub[/vol2]", "btrfs[/vol3]"} {
		if e := m[k].Error; !strings.Contains(e, "不支持的设备路径") {
			t.Errorf("%s = %q", k, e)
		}
	}
	if len(f.Calls()) != 0 {
		t.Errorf("不应执行 sudo: %v", f.Calls())
	}
}

func TestCollectBtrfs没有btrfs挂载(t *testing.T) {
	f := cmdexec.NewFake()
	p := newLinux(f)
	p.readFile = fakeFiles{"/proc/mounts": string(readTestdata(t, "mounts-ubuntu.txt"))}.read
	rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 0 || rep.Status != report.StatusOK || !strings.Contains(rep.Summary, "未发现 Btrfs") {
		t.Errorf("rep = %+v", rep)
	}
}

func TestCollectBtrfsCtx结束返回错误(t *testing.T) {
	f := cmdexec.NewFake()
	p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Collect(ctx, btrfsInput(t, nil, scrubStart, nil)); err == nil {
		t.Fatal("ctx 结束应返回 error")
	}
}

func TestCollectBtrfs读取mounts失败沿用Last(t *testing.T) {
	p := newLinux(cmdexec.NewFake())
	p.readFile = func(string) ([]byte, error) { return nil, context.DeadlineExceeded }
	if _, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart, nil)); err == nil {
		t.Fatal("首次运行读取失败且只有这一项检查，应返回 error")
	}
	last := &report.Report{Items: []report.Item{
		{Key: "btrfs[/vol1]", Type: report.TypeState, State: report.StatusOK},
		{Key: "scrub[/vol1]", Type: report.TypeState, State: report.StatusOK},
	}}
	rep, err := p.Collect(context.Background(), btrfsInput(t, nil, scrubStart, last))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if len(rep.Items) != 2 || m["btrfs[/vol1]"].Error == "" || rep.Status != report.StatusWarning {
		t.Errorf("rep = %+v", rep)
	}
}

func TestParseScrubStatus(t *testing.T) {
	si, ok := parseScrubStatus(string(readTestdata(t, "btrfs-scrub-old-errors.txt")))
	if !ok || si.status != "finished" || si.errors != 2 || si.started != "Tue Sep 30 03:00:01 2025" {
		t.Errorf("old = %+v", si)
	}
	si, ok = parseScrubStatus("scrub status for x\n\tscrub started at Tue Sep 30 03:00:01 2025, running for 00:00:07\n\ttotal bytes scrubbed: 1.00GiB with 0 errors\n")
	if !ok || si.status != "running" || si.started != "Tue Sep 30 03:00:01 2025" || si.errors != 0 {
		t.Errorf("running = %+v", si)
	}
	si, ok = parseScrubStatus("scrub status for x\n\tscrub started at Tue Sep 30 03:00:01 2025 and was aborted after 00:00:05\n\ttotal bytes scrubbed: 1.00GiB with 0 errors\n")
	if !ok || si.status != "aborted" {
		t.Errorf("old aborted = %+v", si)
	}
	si, _ = parseScrubStatus("UUID: x\nScrub started:    Tue Sep  2 03:00:01 2025\nStatus: finished\nError summary:    read=1 super=2\n")
	if si.errors != 3 {
		t.Errorf("read+super = %+v", si)
	}
	if _, ok := parseScrubStatus("garbage"); ok {
		t.Error("无法识别的输出应返回 false")
	}
	if tm, ok := parseScrubTime("Tue Sep  2 03:00:01 2025", time.UTC); !ok || tm.Day() != 2 {
		t.Errorf("单位数日期 = %v %v", tm, ok)
	}
}
