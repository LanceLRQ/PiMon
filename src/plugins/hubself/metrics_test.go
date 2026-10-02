package hubself

import (
	"errors"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

const statFixture = "1234 (pimon hub) (x) S 1 1234 1234 0 -1 4194560 100 0 0 0 500 250 0 0 20 0 10 0 99 1000 2000 18446744073709551615"

const statusFixture = "Name:\tpimon-hub\nVmPeak:\t  90000 kB\nVmRSS:\t   51200 kB\nThreads:\t12\n"

// fakeSys 是用夹具字符串回答 /proc、/sys 读取的 Sys。
type fakeSys struct {
	files    map[string]string
	links    map[string]string
	major    uint32
	minor    uint32
	devErr   error
	statPath string
}

func (f *fakeSys) sys() Sys {
	return Sys{
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := f.files[p]; ok {
				return []byte(s), nil
			}
			return nil, errors.New("not found: " + p)
		},
		Readlink: func(p string) (string, error) {
			if s, ok := f.links[p]; ok {
				return s, nil
			}
			return "", errors.New("no link: " + p)
		},
		StatDev: func(p string) (uint32, uint32, error) {
			f.statPath = p
			return f.major, f.minor, f.devErr
		},
	}
}

func newFakeSys() *fakeSys {
	return &fakeSys{
		files: map[string]string{
			"/proc/self/status":              statusFixture,
			"/proc/self/stat":                statFixture,
			"/sys/dev/block/179:2/partition": "2\n",
			"/sys/block/mmcblk0/stat":        "100 0 2000 50 300 0 4096 70 0 80 120\n",
		},
		links: map[string]string{
			"/sys/dev/block/179:2": "../../devices/platform/soc/fe340000.mmc/mmc_host/mmc0/mmc0:aaaa/block/mmcblk0/mmcblk0p2",
		},
		major: 179, minor: 2,
	}
}

type kioskStats struct {
	fakeStats
	rss int64
	ok  bool
}

func (k *kioskStats) KioskChromiumRSS() (int64, bool) { return k.rss, k.ok }

func boundWith(t *testing.T, st Stats, fs *fakeSys) (*Plugin, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0.Add(90 * time.Second))
	p := New(okDisk(40, 100))
	p.sys = fs.sys()
	p.Bind(st)
	return p, clk
}

func assertUnknown(t *testing.T, rep *report.Report, key string) {
	t.Helper()
	it := rep.Find(key)
	if it == nil {
		t.Fatalf("未知项也应输出（带 error），缺少 %s", key)
	}
	if it.Value != nil || it.Error == "" {
		t.Fatalf("%s 应为未知（无数值且带 error）: %+v", key, it)
	}
}

func TestHubRSSAndDiskWritten(t *testing.T) {
	fs := newFakeSys()
	p, clk := boundWith(t, &fakeStats{started: t0, dir: "/data"}, fs)
	rep := collect(t, p, clk, nil)
	if got := num(t, rep, "hub.rss"); got != 51200*1024 {
		t.Errorf("hub.rss 应为 VmRSS×1024: %v", got)
	}
	if it := rep.Find("hub.rss"); it.Unit != "B" || it.Type != report.TypeNumber {
		t.Errorf("hub.rss 应为单位 B 的 number: %+v", it)
	}
	if got := num(t, rep, "host.disk_written"); got != 4096*512 {
		t.Errorf("host.disk_written 应为第 7 列扇区×512: %v", got)
	}
	if fs.statPath != "/data" {
		t.Errorf("块设备应由数据目录解析: %q", fs.statPath)
	}
	if it := rep.Find("host.disk_written"); it.Unit != "B" {
		t.Errorf("host.disk_written 单位应为 B: %+v", it)
	}
}

func TestDiskWrittenWholeDevice(t *testing.T) {
	fs := newFakeSys()
	fs.major, fs.minor = 8, 0
	fs.files["/sys/dev/block/8:0/partition"] = ""
	delete(fs.files, "/sys/dev/block/8:0/partition")
	fs.links["/sys/dev/block/8:0"] = "../../devices/pci0000:00/block/sda"
	fs.files["/sys/block/sda/stat"] = "1 2 3 4 5 6 10 8 9 10 11"
	p, clk := boundWith(t, &fakeStats{started: t0, dir: "/data"}, fs)
	rep := collect(t, p, clk, nil)
	if got := num(t, rep, "host.disk_written"); got != 10*512 {
		t.Errorf("整盘设备应直接读自身 stat: %v", got)
	}
}

func TestDiskWrittenUnknownCases(t *testing.T) {
	cases := map[string]func(*fakeSys){
		"stat 失败":     func(f *fakeSys) { f.devErr = errors.New("boom") },
		"sys 链接不存在":   func(f *fakeSys) { delete(f.links, "/sys/dev/block/179:2") },
		"设备 stat 不可读": func(f *fakeSys) { delete(f.files, "/sys/block/mmcblk0/stat") },
		"stat 列数不足":   func(f *fakeSys) { f.files["/sys/block/mmcblk0/stat"] = "1 2 3" },
		"stat 非数字":    func(f *fakeSys) { f.files["/sys/block/mmcblk0/stat"] = "1 2 3 4 5 6 x 8" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			fs := newFakeSys()
			mut(fs)
			p, clk := boundWith(t, &fakeStats{started: t0, dir: "/data"}, fs)
			assertUnknown(t, collect(t, p, clk, nil), "host.disk_written")
		})
	}
}

func TestHubRSSUnknown(t *testing.T) {
	for name, body := range map[string]string{"文件缺失": "", "无 VmRSS": "Name:\tx\n", "格式错": "VmRSS:\tabc kB\n"} {
		t.Run(name, func(t *testing.T) {
			fs := newFakeSys()
			if body == "" {
				delete(fs.files, "/proc/self/status")
			} else {
				fs.files["/proc/self/status"] = body
			}
			p, clk := boundWith(t, &fakeStats{started: t0, dir: "/d"}, fs)
			assertUnknown(t, collect(t, p, clk, nil), "hub.rss")
		})
	}
}

func TestHubCPUDifferential(t *testing.T) {
	fs := newFakeSys()
	st := &fakeStats{started: t0, dir: "/d"}
	clk := clock.NewFake(t0.Add(time.Minute))
	p := New(okDisk(40, 100))
	p.sys = fs.sys()
	p.Bind(st)

	first := collect(t, p, clk, nil)
	assertUnknown(t, first, "hub.cpu")

	// 60 秒内多用 750 + 450 个 tick（CLK_TCK=100，即 12 秒 CPU）→ 20%。
	fs.files["/proc/self/stat"] = "1234 (pimon hub) (x) S 1 1234 1234 0 -1 4194560 100 0 0 0 1250 700 0 0 20 0 10 0 99 1000 2000 18446744073709551615"
	clk.Advance(60 * time.Second)
	second := collect(t, p, clk, nil)
	if got := num(t, second, "hub.cpu"); got < 19.999 || got > 20.001 {
		t.Errorf("hub.cpu 应为 20%%: %v", got)
	}
	if it := second.Find("hub.cpu"); it.Unit != "%" || it.Type != report.TypeNumber {
		t.Errorf("hub.cpu 应为单位 %% 的 number: %+v", it)
	}

	// 计数回退（异常）→ 未知并重置基线，下次重新有值。
	fs.files["/proc/self/stat"] = statFixture
	clk.Advance(60 * time.Second)
	assertUnknown(t, collect(t, p, clk, nil), "hub.cpu")
	fs.files["/proc/self/stat"] = "1234 (pimon hub) (x) S 1 1234 1234 0 -1 4194560 100 0 0 0 560 250 0 0 20 0 10 0 99 1000 2000 18446744073709551615"
	clk.Advance(60 * time.Second)
	if got := num(t, collect(t, p, clk, nil), "hub.cpu"); got < 0.999 || got > 1.001 {
		t.Errorf("基线重置后应继续算: %v", got)
	}
}

func TestHubCPUUnknownWhenUnreadable(t *testing.T) {
	fs := newFakeSys()
	delete(fs.files, "/proc/self/stat")
	p, clk := boundWith(t, &fakeStats{started: t0, dir: "/d"}, fs)
	assertUnknown(t, collect(t, p, clk, nil), "hub.cpu")
}

func TestKioskChromiumRSS(t *testing.T) {
	fs := newFakeSys()
	st := &kioskStats{fakeStats: fakeStats{started: t0, dir: "/d"}, rss: 1_000_000_000, ok: true}
	p, clk := boundWith(t, st, fs)
	rep := collect(t, p, clk, nil)
	if got := num(t, rep, "kiosk.chromium_rss"); got != 1_000_000_000 {
		t.Errorf("kiosk.chromium_rss: %v", got)
	}
	if it := rep.Find("kiosk.chromium_rss"); it.Unit != "B" {
		t.Errorf("单位应为 B: %+v", it)
	}
	st.ok = false
	assertUnknown(t, collect(t, p, clk, nil), "kiosk.chromium_rss")

	// 来源未实现可选接口时同样为未知。
	p2, clk2 := boundWith(t, &fakeStats{started: t0, dir: "/d"}, fs)
	assertUnknown(t, collect(t, p2, clk2, nil), "kiosk.chromium_rss")
}

func TestUnsupportedPlatformAllUnknown(t *testing.T) {
	p, clk := boundWith(t, &fakeStats{started: t0, dir: "/d"}, newFakeSys())
	p.sys = unsupportedSys()
	rep := collect(t, p, clk, nil)
	for _, k := range []string{"hub.rss", "hub.cpu", "host.disk_written"} {
		assertUnknown(t, rep, k)
	}
	if rep.Status != report.StatusOK {
		t.Errorf("指标未知不应拉高状态: %v", rep.Status)
	}
}

func TestManifestDeclaresMetrics(t *testing.T) {
	p := New(nil)
	have := map[string]bool{}
	for _, o := range p.Manifest().Outputs {
		have[o.Key] = true
	}
	for _, k := range []string{"hub.rss", "hub.cpu", "kiosk.chromium_rss", "host.disk_written"} {
		if !have[k] {
			t.Errorf("plugin.yaml 缺少输出 %s", k)
		}
	}
}
