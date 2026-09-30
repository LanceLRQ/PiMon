package hostmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

var errNA = errors.New("不可用")

// fakeSampler 是可编程的取数替身；err* 非空时对应方法返回错误。
type fakeSampler struct {
	busy, total float64
	cpuErr      error
	load        float64
	memUsed     uint64
	memTotal    uint64
	temp        float64
	tempErr     error
	parts       []Partition
	usage       map[string]DiskUsage
	rx, tx      uint64
	netErr      error
	uptime      uint64
	throttled   uint32
	throttleErr error
}

func (f *fakeSampler) CPUTimes(context.Context) (float64, float64, error) {
	return f.busy, f.total, f.cpuErr
}
func (f *fakeSampler) Load1(context.Context) (float64, error) { return f.load, nil }
func (f *fakeSampler) Memory(context.Context) (uint64, uint64, error) {
	return f.memUsed, f.memTotal, nil
}
func (f *fakeSampler) Temperature(context.Context) (float64, error) { return f.temp, f.tempErr }
func (f *fakeSampler) Partitions(context.Context) ([]Partition, error) {
	return f.parts, nil
}
func (f *fakeSampler) Usage(_ context.Context, mount string) (DiskUsage, error) {
	u, ok := f.usage[mount]
	if !ok {
		return DiskUsage{}, errNA
	}
	return u, nil
}
func (f *fakeSampler) NetCounters(context.Context) (uint64, uint64, error) {
	return f.rx, f.tx, f.netErr
}
func (f *fakeSampler) Uptime(context.Context) (uint64, error)    { return f.uptime, nil }
func (f *fakeSampler) Throttled(context.Context) (uint32, error) { return f.throttled, f.throttleErr }

func baseSampler() *fakeSampler {
	return &fakeSampler{
		busy: 10, total: 100, load: 0.5, memUsed: 2 << 30, memTotal: 8 << 30, tempErr: errNA,
		rx: 1000, tx: 500, uptime: 3600, throttleErr: errNA,
	}
}

func run(t *testing.T, p *Plugin, clk clock.Clock, state string, config map[string]any) *report.Report {
	t.Helper()
	rep, err := p.Collect(context.Background(), runtime.Input{Clock: clk, State: state, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func val(t *testing.T, rep *report.Report, key string) float64 {
	t.Helper()
	it := rep.Find(key)
	if it == nil || it.Value == nil {
		t.Fatalf("缺少数据项 %s: %+v", key, rep.Items)
	}
	return *it.Value
}

func TestRegisteredAndManifest(t *testing.T) {
	s, ok := runtime.Builtin("host-metrics")
	if !ok {
		t.Fatal("host-metrics 应在 init 中注册")
	}
	var dyn bool
	for _, o := range s.Manifest().Outputs {
		if o.Key == "disk[*]" {
			dyn = true
		}
	}
	if !dyn {
		t.Fatal("outputs 应声明 disk[*]")
	}
}

func TestFirstRunOmitsRates(t *testing.T) {
	rep := run(t, New(baseSampler()), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	for _, k := range []string{"cpu", "net_rx", "net_tx"} {
		if rep.Find(k) != nil {
			t.Errorf("首次运行不应输出 %s（无从计算差值，也不能写 0）", k)
		}
	}
	if val(t, rep, "mem") != 25 || val(t, rep, "load") != 0.5 || val(t, rep, "uptime") != 3600 {
		t.Fatalf("内存、负载、运行时长不符: %+v", rep.Items)
	}
	if rep.State == "" {
		t.Fatal("应写出私有 state 供下次计算")
	}
}

func TestRatesFromFakeCounters(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	s := baseSampler()
	p := New(s)
	first := run(t, p, clk, "", nil)

	clk.Advance(10 * time.Second)
	s.busy, s.total = 60, 200 // 忙碌增 50 / 总增 100
	s.rx, s.tx = 6000, 2500
	second := run(t, p, clk, first.State, nil)

	if got := val(t, second, "cpu"); got != 50 {
		t.Errorf("CPU 应为 50%%，得 %v", got)
	}
	if got := val(t, second, "net_rx"); got != 500 {
		t.Errorf("下载应为 500 B/s，得 %v", got)
	}
	if got := val(t, second, "net_tx"); got != 200 {
		t.Errorf("上传应为 200 B/s，得 %v", got)
	}
}

func TestCounterResetOmitsRate(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	s := baseSampler()
	p := New(s)
	first := run(t, p, clk, "", nil)
	clk.Advance(10 * time.Second)
	s.rx, s.tx = 10, 10 // 计数器回绕
	s.busy, s.total = 1, 10
	rep := run(t, p, clk, first.State, nil)
	for _, k := range []string{"cpu", "net_rx", "net_tx"} {
		if rep.Find(k) != nil {
			t.Errorf("计数器回绕时不应输出 %s", k)
		}
	}
}

func TestCorruptStateTreatedAsFirstRun(t *testing.T) {
	rep := run(t, New(baseSampler()), clock.NewFake(time.Unix(1_800_000_000, 0)), "{not json", nil)
	if rep.Find("net_rx") != nil || rep.Find("mem") == nil {
		t.Fatalf("损坏的 state 应按首次运行处理: %+v", rep.Items)
	}
}

func TestMissingTemperatureIsGraceful(t *testing.T) {
	rep := run(t, New(baseSampler()), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if rep.Find("temp") != nil || rep.Status != report.StatusOK {
		t.Fatalf("无温度应缺项且状态 ok: %+v", rep)
	}
}

func TestTemperatureThresholds(t *testing.T) {
	for temp, want := range map[float64]report.Status{50: report.StatusOK, 75: report.StatusWarning, 85: report.StatusCritical} {
		s := baseSampler()
		s.temp, s.tempErr = temp, nil
		rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
		if rep.Status != want || val(t, rep, "temp") != temp {
			t.Errorf("%v℃ 应为 %s，得 %s", temp, want, rep.Status)
		}
	}
	// 阈值可配置。
	s := baseSampler()
	s.temp, s.tempErr = 60, nil
	rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", map[string]any{"temp_warn": 55.0})
	if rep.Status != report.StatusWarning {
		t.Fatalf("自定义阈值应生效: %s", rep.Status)
	}
}

func TestDisksDynamicKeysAndFiltering(t *testing.T) {
	s := baseSampler()
	s.parts = []Partition{
		{Mount: "/", FSType: "ext4", Device: "/dev/sda1"},
		{Mount: "/mnt/data", FSType: "btrfs", Device: "/dev/sdb1"},
		{Mount: "/mnt/data/sub", FSType: "btrfs", Device: "/dev/sdb1"}, // 同设备重复挂载
		{Mount: "/run/user/1000", FSType: "tmpfs", Device: "tmpfs"},
		{Mount: "/snap/core/1", FSType: "squashfs", Device: "/dev/loop0"},
		{Mount: "/var/lib/docker/overlay2/x", FSType: "overlay", Device: "overlay"},
		{Mount: "/mnt/broken", FSType: "ext4", Device: "/dev/sdc1"},
	}
	s.usage = map[string]DiskUsage{
		"/":         {Used: 50 << 30, Total: 100 << 30},
		"/mnt/data": {Used: 96 << 30, Total: 100 << 30},
	}
	rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)

	var keys []string
	for _, it := range rep.Items {
		if k, err := report.ParseKey(it.Key); err == nil && k.Dynamic && k.Prefix == "disk" {
			keys = append(keys, it.Key)
		}
	}
	want := []string{"disk[/]", "disk[/mnt/broken]", "disk[/mnt/data]"}
	if len(keys) != len(want) {
		t.Fatalf("磁盘项应为 %v，得 %v", want, keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("磁盘项应为 %v，得 %v", want, keys)
		}
	}
	root := rep.Find("disk[/]")
	if *root.RemainingPct != 50 || *root.Used != float64(50<<30) {
		t.Fatalf("根分区数值不符: %+v", root)
	}
	if b := rep.Find("disk[/mnt/broken]"); b.Error == "" || b.RemainingPct != nil {
		t.Fatalf("取不到容量的挂载点应只标错误，不写 0: %+v", b)
	}
	if rep.Status != report.StatusCritical {
		t.Fatalf("96%% 使用率应使整体 critical: %s", rep.Status)
	}
	if len(rep.Select("disk[*]")) != 3 {
		t.Fatal("disk[*] 通配应命中全部磁盘项")
	}
}

func TestRealMountFilter(t *testing.T) {
	for _, c := range []struct {
		p    Partition
		want bool
	}{
		{Partition{Mount: "/", FSType: "ext4"}, true},
		{Partition{Mount: "/boot/firmware", FSType: "vfat"}, true},
		{Partition{Mount: "/System/Volumes/Data", FSType: "apfs"}, true},
		{Partition{Mount: "/System/Volumes/VM", FSType: "apfs"}, false},
		{Partition{Mount: "/dev", FSType: "devtmpfs"}, false},
		{Partition{Mount: "/mnt/x", FSType: "tmpfs"}, false},
		{Partition{Mount: "/run/lock", FSType: "ext4"}, false},
		{Partition{Mount: "/runner", FSType: "ext4"}, true},
		{Partition{Mount: "", FSType: "ext4"}, false},
	} {
		if got := realMount(c.p); got != c.want {
			t.Errorf("%+v 应为 %v", c.p, c.want)
		}
	}
}

func TestThrottledItem(t *testing.T) {
	s := baseSampler()
	if rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil); rep.Find("throttled") != nil {
		t.Fatal("读不到欠压标志时不应输出该项")
	}
	s.throttleErr, s.throttled = nil, 0x50005
	rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if it := rep.Find("throttled"); it == nil || it.State != report.StatusWarning || rep.Status != report.StatusWarning {
		t.Fatalf("当前欠压应为 warning: %+v", rep)
	}
	s.throttled = 0x50000 // 只有历史
	rep = run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if it := rep.Find("throttled"); it == nil || it.State != report.StatusOK || rep.Status != report.StatusOK {
		t.Fatalf("仅历史记录应为 ok: %+v", rep)
	}
	s.throttled = 0
	rep = run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if it := rep.Find("throttled"); it == nil || it.State != report.StatusOK {
		t.Fatalf("无标志应为 ok: %+v", rep)
	}
}

func TestSamplerFailuresOmitItems(t *testing.T) {
	s := baseSampler()
	s.cpuErr, s.netErr = errNA, errNA
	rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if rep.Find("cpu") != nil || rep.Find("net_rx") != nil {
		t.Fatalf("取不到的量不应输出: %+v", rep.Items)
	}
}

func TestParseThrottled(t *testing.T) {
	for in, want := range map[string]uint32{
		"throttled=0x50005\n": 0x50005,
		"0x0":                 0,
		"50005\n":             0x50005, // sysfs 的无前缀十六进制
	} {
		got, err := parseThrottled(in)
		if err != nil || got != want {
			t.Errorf("%q → %#x, %v，期望 %#x", in, got, err, want)
		}
	}
	if _, err := parseThrottled("garbage"); err == nil {
		t.Error("乱码应报错")
	}
}

func TestReadThrottledFallbacks(t *testing.T) {
	fail := func(context.Context, string, ...string) ([]byte, error) { return nil, errNA }
	ok := func(context.Context, string, ...string) ([]byte, error) { return []byte("throttled=0x1\n"), nil }
	file := func(path string) ([]byte, error) {
		if path != sysfsThrottled {
			t.Errorf("读了意外路径 %s", path)
		}
		return []byte("4\n"), nil
	}
	none := func(string) ([]byte, error) { return nil, errNA }
	ctx := context.Background()

	if v, err := readThrottled(ctx, throttleIO{run: ok, readFile: none}); err != nil || v != 1 {
		t.Errorf("vcgencmd 可用时应取其值: %v %v", v, err)
	}
	if v, err := readThrottled(ctx, throttleIO{run: fail, readFile: file}); err != nil || v != 4 {
		t.Errorf("vcgencmd 不可用时应回退 sysfs: %v %v", v, err)
	}
	if _, err := readThrottled(ctx, throttleIO{run: fail, readFile: none}); err == nil {
		t.Error("两者都没有应报错（调用方据此不输出）")
	}
}

func TestPickTemperature(t *testing.T) {
	got, err := pickTemperature([]sensors.TemperatureStat{
		{SensorKey: "nvme_composite", Temperature: 40},
		{SensorKey: "cpu_thermal", Temperature: 55},
	})
	if err != nil || got != 55 {
		t.Errorf("应优先取 CPU 传感器: %v %v", got, err)
	}
	got, err = pickTemperature([]sensors.TemperatureStat{{SensorKey: "nvme", Temperature: 40}})
	if err != nil || got != 40 {
		t.Errorf("无 CPU 传感器时取第一个有效读数: %v %v", got, err)
	}
	if _, err := pickTemperature([]sensors.TemperatureStat{{SensorKey: "x", Temperature: 0}}); err == nil {
		t.Error("只有 0 读数应视为没有传感器")
	}
	if _, err := pickTemperature(nil); err == nil {
		t.Error("没有传感器应报错")
	}
}

func TestSumNIC(t *testing.T) {
	rx, tx, ok := sumNIC([]gnet.IOCountersStat{
		{Name: "lo0", BytesRecv: 999, BytesSent: 999},
		{Name: "docker0", BytesRecv: 5, BytesSent: 5},
		{Name: "eth0", BytesRecv: 100, BytesSent: 40},
		{Name: "wlan0", BytesRecv: 10, BytesSent: 4},
	})
	if !ok || rx != 110 || tx != 44 {
		t.Errorf("应只累加物理网卡: %d %d %v", rx, tx, ok)
	}
	if _, _, ok := sumNIC([]gnet.IOCountersStat{{Name: "lo"}}); ok {
		t.Error("只有回环时应视为不可用")
	}
}

// 真实取数来源在本机上至少要能构造并返回内存数据，保证 gopsutil 接线没断。
func TestSystemSamplerSmoke(t *testing.T) {
	s := newSystemSampler(throttleIO{
		run:      func(context.Context, string, ...string) ([]byte, error) { return nil, errNA },
		readFile: func(string) ([]byte, error) { return nil, errNA },
	})
	if _, total, err := s.Memory(context.Background()); err != nil || total == 0 {
		t.Fatalf("内存读取失败: %v", err)
	}
	if _, err := s.Throttled(context.Background()); err == nil {
		t.Fatal("替身全部失败时应报错")
	}
}

// 断言报告通过 Validate，且每个键都落在 manifest outputs 声明内（动态集合按前缀匹配）。
func assertValidAgainstManifest(t *testing.T, p *Plugin, rep *report.Report) {
	t.Helper()
	if err := rep.Validate(nil); err != nil {
		t.Fatalf("报告应通过 Validate: %v", err)
	}
	for _, it := range rep.Items {
		ok := false
		for _, o := range p.Manifest().Outputs {
			k, err := report.ParseKey(o.Key)
			if err != nil {
				t.Fatal(err)
			}
			if k.Matches(it.Key) || (!k.Dynamic && o.Key == it.Key) {
				ok = o.Type == it.Type
				break
			}
		}
		if !ok {
			t.Errorf("键 %s（%s）不在 manifest outputs 内或类型不符", it.Key, it.Type)
		}
	}
}

func diskSampler(parts ...Partition) *fakeSampler {
	s := baseSampler()
	s.parts = parts
	s.usage = map[string]DiskUsage{}
	for _, p := range parts {
		s.usage[p.Mount] = DiskUsage{Used: 1 << 30, Total: 4 << 30}
	}
	s.temp, s.tempErr = 50, nil
	s.throttled, s.throttleErr = 0, nil
	return s
}

func diskKeys(rep *report.Report) []string {
	var out []string
	for _, it := range rep.Select("disk[*]") {
		out = append(out, it.Key)
	}
	return out
}

func TestSameDeviceKeepsShortestMountRegardlessOfOrder(t *testing.T) {
	a := Partition{Mount: "/mnt/data", FSType: "btrfs", Device: "/dev/sdb1"}
	b := Partition{Mount: "/mnt/data/sub", FSType: "btrfs", Device: "/dev/sdb1"}
	for name, parts := range map[string][]Partition{"正序": {a, b}, "反序": {b, a}} {
		p := New(diskSampler(parts...))
		rep := run(t, p, clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
		if got := diskKeys(rep); len(got) != 1 || got[0] != "disk[/mnt/data]" {
			t.Errorf("%s：应稳定保留最短路径，得 %v", name, got)
		}
		assertValidAgainstManifest(t, p, rep)
	}
}

func TestDuplicateMountPointDeduped(t *testing.T) {
	// 叠加挂载：同一挂载点、不同设备，不能产生重复键，否则整份报告会被 Validate 拒绝。
	p := New(diskSampler(
		Partition{Mount: "/mnt/x", FSType: "ext4", Device: "/dev/sdb1"},
		Partition{Mount: "/mnt/x", FSType: "ext4", Device: "/dev/sdc1"},
		Partition{Mount: "/", FSType: "ext4", Device: "/dev/sda1"},
	))
	rep := run(t, p, clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if got := diskKeys(rep); len(got) != 2 || got[0] != "disk[/]" || got[1] != "disk[/mnt/x]" {
		t.Fatalf("同挂载点应去重: %v", got)
	}
	assertValidAgainstManifest(t, p, rep)
}

func TestFullReportValidAgainstManifest(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	s := diskSampler(Partition{Mount: "/", FSType: "ext4", Device: "/dev/sda1"})
	p := New(s)
	first := run(t, p, clk, "", nil)
	clk.Advance(10 * time.Second)
	s.busy, s.total, s.rx, s.tx = 60, 200, 6000, 2500
	assertValidAgainstManifest(t, p, run(t, p, clk, first.State, nil))
}

func TestSumNICExcludesOverlayInterfaces(t *testing.T) {
	var stats []gnet.IOCountersStat
	for _, n := range []string{"br0", "br-abc", "bond0", "tailscale0", "wg0", "zt12345", "docker0", "tun0"} {
		stats = append(stats, gnet.IOCountersStat{Name: n, BytesRecv: 1000, BytesSent: 1000})
	}
	stats = append(stats,
		gnet.IOCountersStat{Name: "eth0", BytesRecv: 7, BytesSent: 3},
		gnet.IOCountersStat{Name: "apcli0", BytesRecv: 5, BytesSent: 2}, // 以 ap 开头的真实网卡不应被误排
	)
	rx, tx, ok := sumNIC(stats)
	if !ok || rx != 12 || tx != 5 {
		t.Fatalf("应只统计 eth0 与 apcli0: %d %d %v", rx, tx, ok)
	}
}
