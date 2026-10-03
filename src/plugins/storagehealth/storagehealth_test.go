package storagehealth

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/plugintest"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

var baseTime = time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)

const (
	sudoPath  = "/usr/bin/sudo"
	smartPath = "/usr/sbin/smartctl"
	mdadmPath = "/usr/sbin/mdadm"
	btrfsPath = "/usr/bin/btrfs"
)

// fakeFiles 模拟 /proc 下的文件；未登记的路径视为不存在。
type fakeFiles map[string]string

func (f fakeFiles) read(name string) ([]byte, error) {
	if v, ok := f[name]; ok {
		return []byte(v), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// links 模拟 /dev/disk/by-id 等符号链接；不在表内的路径原样返回。
var links = map[string]string{
	"/dev/disk/by-id/ata-WDC_WD40EFRX": "/dev/sda",
	"/dev/disk/by-id/usb-JMicron_X":    "/dev/sdb",
	"/dev/disk/by-id/loop":             "/dev/loop0",
}

func evalLinks(p string) (string, error) {
	if r, ok := links[p]; ok {
		return r, nil
	}
	if strings.HasPrefix(p, "/dev/disk/") {
		return "", os.ErrNotExist
	}
	return p, nil
}

// newLinux 返回固定为 Linux、sudo 与 smartctl 均可用的插件，命令由 Fake 回放。
func newLinux(f *cmdexec.Fake) *Plugin {
	p := New(f)
	p.goos = "linux"
	p.lookPath = func(string) (string, error) { return sudoPath, nil }
	p.resolve = func(name string) (string, error) {
		switch name {
		case sudocmd.Smartctl:
			return smartPath, nil
		case sudocmd.Mdadm:
			return mdadmPath, nil
		case sudocmd.Btrfs:
			return btrfsPath, nil
		}
		return "", sudocmd.ErrNotFound
	}
	p.evalSymlinks = evalLinks
	p.readFile = fakeFiles{"/proc/mounts": ""}.read
	p.loc = time.UTC
	return p
}

func sudoArgsFor(t *testing.T, path string, op sudocmd.Op, dev string) []string {
	t.Helper()
	args, err := sudocmd.Args(op, dev)
	if err != nil {
		t.Fatal(err)
	}
	return sudocmd.SudoArgv(path, args)
}

func sudoArgs(t *testing.T, op sudocmd.Op, dev string) []string {
	t.Helper()
	return sudoArgsFor(t, smartPath, op, dev)
}

// smartOnly 关掉 RAID 与 Btrfs 检查，其余配置照给。
func smartOnly(cfg map[string]any) map[string]any {
	out := map[string]any{"raid": false, "btrfs": false}
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

func setScan(t *testing.T, f *cmdexec.Fake, res cmdexec.Result, err error) {
	f.Set(res, err, sudoPath, sudoArgs(t, sudocmd.OpSmartScan, "")...)
}

func setDisk(t *testing.T, f *cmdexec.Fake, dev string, sat bool, res cmdexec.Result, err error) {
	op := sudocmd.OpSmart
	if sat {
		op = sudocmd.OpSmartSat
	}
	f.Set(res, err, sudoPath, sudoArgs(t, op, dev)...)
}

func sample(t *testing.T, name string, exit int) cmdexec.Result {
	return cmdexec.Result{Stdout: readTestdata(t, name), ExitCode: exit}
}

// input 用 schema 整理配置（补默认值），与运行时一致。
func input(t *testing.T, cfg map[string]any, last *report.Report) runtime.Input {
	t.Helper()
	m := New(cmdexec.NewFake()).Manifest()
	clean, errs := schema.Prepare(m.ConfigSchema, cfg)
	if errs != nil {
		t.Fatalf("配置不合法: %v", errs)
	}
	return runtime.Input{Config: clean, Last: last, Clock: clock.NewFake(baseTime)}
}

func items(rep *report.Report) map[string]report.Item {
	m := map[string]report.Item{}
	for _, it := range rep.Items {
		m[it.Key] = it
	}
	return m
}

func val(t *testing.T, m map[string]report.Item, key string) float64 {
	t.Helper()
	it, ok := m[key]
	if !ok {
		t.Fatalf("缺少数据项 %s", key)
	}
	if it.Value == nil {
		t.Fatalf("%s 没有值", key)
	}
	return *it.Value
}

func TestManifest与小组件尺寸(t *testing.T) {
	m := New(cmdexec.NewFake()).Manifest()
	plugintest.AssertWidgetSizes(t, m)
	if m.ID != "storage-health" || len(m.RunsOn) != 1 || m.RunsOn[0] != "agent" {
		t.Fatalf("manifest = %s %v", m.ID, m.RunsOn)
	}
	clean, errs := schema.Prepare(m.ConfigSchema, map[string]any{})
	if errs != nil {
		t.Fatal(errs)
	}
	for k, want := range map[string]any{"smart": true, "raid": true, "btrfs": true, "temp_warn": 50.0, "temp_critical": 60.0, "nvme_temp_warn": 70.0, "nvme_temp_critical": 80.0, "scrub_max_age_days": 35.0} {
		if clean[k] != want {
			t.Errorf("默认值 %s = %v，期望 %v", k, clean[k], want)
		}
	}
	if errs := schema.Validate(m.ConfigSchema, map[string]any{"smart_devices": []any{"/dev/sda -s off"}}); len(errs) == 0 {
		t.Error("含空格的设备应被 pattern 拒绝")
	}
}

func TestCollect自动发现与各类盘(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, cmdexec.Result{Stdout: readTestdata(t, "scan-open-linux.txt")}, nil)
	setDisk(t, f, "/dev/sda", true, sample(t, "smart-ata-healthy.json", 0), nil)
	setDisk(t, f, "/dev/sdb", false, sample(t, "smart-standby.json", 2), nil)
	setDisk(t, f, "/dev/sdc", true, sample(t, "smart-ata-failing.json", 216), nil)
	setDisk(t, f, "/dev/nvme0", false, sample(t, "smart-nvme-macos.json", 4), nil)
	p := newLinux(f)
	rep, err := p.Collect(context.Background(), input(t, map[string]any{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)

	if it := m["smart[/dev/sda]"]; it.State != report.StatusOK || it.Error != "" {
		t.Errorf("sda = %+v", it)
	}
	if val(t, m, "smart_temp[/dev/sda]") != 32 || val(t, m, "smart_realloc[/dev/sda]") != 0 || val(t, m, "smart_pending[/dev/sda]") != 0 || val(t, m, "smart_hours[/dev/sda]") != 1730 {
		t.Error("sda 数值不符")
	}
	if it := m["smart[/dev/sdb]"]; it.State != report.StatusUnknown || it.Error != "" || !strings.Contains(it.Text, "休眠中") {
		t.Errorf("休眠盘 = %+v", it)
	}
	if _, ok := m["smart_temp[/dev/sdb]"]; ok {
		t.Error("首次运行的休眠盘不应凭空产出温度项")
	}
	if it := m["smart[/dev/sdc]"]; it.State != report.StatusCritical || !strings.Contains(it.Text, "SMART 判定失败") || !strings.Contains(it.Text, "待定扇区 8") {
		t.Errorf("失败盘 = %+v", it)
	}
	if val(t, m, "smart_realloc[/dev/sdc]") != 1975 || val(t, m, "smart_pending[/dev/sdc]") != 8 {
		t.Error("失败盘扇区数不符")
	}
	nv := m["smart[/dev/nvme0]"]
	if nv.State != report.StatusOK || nv.Error != "" || !strings.Contains(nv.Text, "部分 SMART 命令失败") {
		t.Errorf("NVMe = %+v", nv)
	}
	if val(t, m, "smart_temp[/dev/nvme0]") != 54 || val(t, m, "smart_hours[/dev/nvme0]") != 1489 {
		t.Error("NVMe 数值不符")
	}
	for _, k := range []string{"smart_realloc[/dev/nvme0]", "smart_pending[/dev/nvme0]", "smart[/dev/sdd]", "smart[/dev/bus/0]"} {
		if _, ok := m[k]; ok {
			t.Errorf("不应产出 %s", k)
		}
	}
	if rep.Status != report.StatusCritical {
		t.Errorf("整体状态 = %s", rep.Status)
	}
	if !strings.Contains(rep.Summary, "磁盘 4 块") || !strings.Contains(rep.Summary, "休眠 1") {
		t.Errorf("summary = %q", rep.Summary)
	}
	// 每条 sudo 调用都是 `sudo -n -- <smartctl> …`，USB SAT 盘带 -d sat。
	for _, c := range f.Calls() {
		if c.Name != sudoPath || c.Args[0] != "-n" || c.Args[1] != "--" || c.Args[2] != smartPath {
			t.Errorf("调用形态不符: %v", c)
		}
	}
}

func TestCollect配置设备解析符号链接并校验白名单(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sda", false, sample(t, "smart-ata-healthy.json", 0), nil)
	setDisk(t, f, "/dev/sdb", true, sample(t, "smart-usb-sat-partial.json", 0), nil)
	p := newLinux(f)
	cfg := map[string]any{
		"smart_devices": []any{"/dev/disk/by-id/ata-WDC_WD40EFRX", "/dev/disk/by-id/usb-JMicron_X", "/dev/disk/by-id/loop", "/dev/disk/by-id/missing"},
		"sat_devices":   []any{"/dev/disk/by-id/usb-JMicron_X"},
	}
	rep, err := p.Collect(context.Background(), input(t, cfg, nil))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	if it := m["smart[/dev/disk/by-id/ata-WDC_WD40EFRX]"]; it.State != report.StatusOK || !strings.Contains(it.Text, "/dev/sda") {
		t.Errorf("by-id 盘 = %+v", it)
	}
	for _, k := range []string{"smart[/dev/disk/by-id/loop]", "smart[/dev/disk/by-id/missing]"} {
		if it := m[k]; it.State != report.StatusUnknown || it.Error == "" {
			t.Errorf("%s = %+v", k, it)
		}
	}
	if e := m["smart[/dev/disk/by-id/loop]"].Error; !strings.Contains(e, "不支持的设备路径") {
		t.Errorf("loop 应判不支持: %q", e)
	}
	if e := m["smart[/dev/disk/by-id/missing]"].Error; !strings.Contains(e, "设备不存在或无法解析") || strings.Contains(e, "不支持") {
		t.Errorf("不存在的设备应单独说明: %q", e)
	}
	if len(f.Calls()) != 2 {
		t.Errorf("白名单外的设备不应执行 sudo: %v", f.Calls())
	}
	if rep.Status != report.StatusWarning {
		t.Errorf("部分条目失败整体应为 warning: %s", rep.Status)
	}
}

func TestCollect缺失字段为未知不当0(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sdb", true, sample(t, "smart-usb-sat-partial.json", 0), nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sdb"}, "sat_devices": []any{"/dev/sdb"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if it := m["smart[/dev/sdb]"]; it.State != report.StatusUnknown || it.Error != "" || !strings.Contains(it.Text, "健康状态未知") {
		t.Errorf("缺 smart_status 应判未知: %+v", it)
	}
	if it, ok := m["smart_hours[/dev/sdb]"]; !ok || it.Value != nil {
		t.Errorf("缺 power_on_time 的通电时长应无值: %+v", it)
	}
	if val(t, m, "smart_temp[/dev/sdb]") != 29 {
		t.Error("温度不符")
	}
	if rep.Status != report.StatusUnknown || !strings.Contains(rep.Summary, "未知 1") {
		t.Errorf("全部条目为未知时整体应为 unknown 且 summary 含未知计数: %s %q", rep.Status, rep.Summary)
	}
}

func TestCollectNVMe判定规则(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		cfg    map[string]any
		want   report.Status
		substr []string
	}{
		{"严重警告与备用空间不足", "smart-nvme-degraded.json", nil, report.StatusCritical, []string{"严重警告 0x04", "备用空间 5% 低于阈值 10%", "寿命已用 97%", "温度偏高"}},
		{"介质错误", "smart-nvme-media-errors.json", nil, report.StatusCritical, []string{"介质错误 7"}},
		{"NVMe 用自己的温度阈值", "smart-nvme-macos.json", nil, report.StatusOK, nil},
		{"NVMe 温度超警告", "smart-nvme-macos.json", map[string]any{"nvme_temp_warn": 50.0}, report.StatusWarning, []string{"温度偏高"}},
		{"NVMe 温度超严重", "smart-nvme-macos.json", map[string]any{"nvme_temp_warn": 40.0, "nvme_temp_critical": 50.0}, report.StatusCritical, []string{"温度过高"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cmdexec.NewFake()
			setDisk(t, f, "/dev/nvme0", false, sample(t, c.file, 0), nil)
			cfg := map[string]any{"smart_devices": []any{"/dev/nvme0"}}
			for k, v := range c.cfg {
				cfg[k] = v
			}
			rep, err := newLinux(f).Collect(context.Background(), input(t, cfg, nil))
			if err != nil {
				t.Fatal(err)
			}
			it := items(rep)["smart[/dev/nvme0]"]
			if it.State != c.want {
				t.Errorf("状态 = %s，期望 %s（%s）", it.State, c.want, it.Text)
			}
			for _, s := range c.substr {
				if !strings.Contains(it.Text, s) {
					t.Errorf("text %q 缺少 %q", it.Text, s)
				}
			}
		})
	}
}

func TestCollectNVMe仅温度位为警告(t *testing.T) {
	doc := strings.Replace(string(readTestdata(t, "smart-nvme-macos.json")), `"critical_warning": 0`, `"critical_warning": 2`, 1)
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/nvme0", false, cmdexec.Result{Stdout: []byte(doc)}, nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/nvme0"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if it := items(rep)["smart[/dev/nvme0]"]; it.State != report.StatusWarning || !strings.Contains(it.Text, "温度越限") {
		t.Errorf("仅温度位 = %+v", it)
	}
}

func TestCollectATA温度阈值(t *testing.T) {
	cases := []struct {
		cfg  map[string]any
		want report.Status
	}{
		{nil, report.StatusOK},
		{map[string]any{"temp_warn": 32.0}, report.StatusWarning},
		{map[string]any{"temp_warn": 30.0, "temp_critical": 32.0}, report.StatusCritical},
	}
	for _, c := range cases {
		f := cmdexec.NewFake()
		setDisk(t, f, "/dev/sda", false, sample(t, "smart-ata-healthy.json", 0), nil)
		cfg := map[string]any{"smart_devices": []any{"/dev/sda"}}
		for k, v := range c.cfg {
			cfg[k] = v
		}
		rep, err := newLinux(f).Collect(context.Background(), input(t, cfg, nil))
		if err != nil {
			t.Fatal(err)
		}
		if it := items(rep)["smart[/dev/sda]"]; it.State != c.want {
			t.Errorf("cfg %v：状态 = %s，期望 %s", c.cfg, it.State, c.want)
		}
	}
}

func TestCollect退出码按位解读(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		exit    int
		state   report.Status
		errPart string
		text    string
	}{
		{"bit3 判定失败", "smart-ata-healthy.json", 8, report.StatusCritical, "", "SMART 判定失败"},
		{"bit2 只注明", "smart-ata-healthy.json", 4, report.StatusOK, "", "部分 SMART 命令失败"},
		{"bit4 只写文字", "smart-ata-healthy.json", 16, report.StatusOK, "", "预失效属性"},
		{"bit5 只写文字", "smart-ata-healthy.json", 32, report.StatusOK, "", "曾低于阈值"},
		{"bit6 只写文字", "smart-ata-healthy.json", 64, report.StatusOK, "", "错误日志"},
		{"bit7 只写文字", "smart-ata-healthy.json", 128, report.StatusOK, "", "自检日志"},
		{"bit0 命令行错误", "smart-ata-healthy.json", 1, report.StatusUnknown, "命令行错误", ""},
		{"bit1 打开失败", "smart-open-failed.json", 2, report.StatusUnknown, "No such device", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cmdexec.NewFake()
			setDisk(t, f, "/dev/sda", false, sample(t, c.file, c.exit), nil)
			rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sda"}}, nil))
			if err != nil {
				t.Fatal(err)
			}
			it := items(rep)["smart[/dev/sda]"]
			if it.State != c.state || !strings.Contains(it.Error, c.errPart) || !strings.Contains(it.Text, c.text) {
				t.Errorf("条目 = %+v", it)
			}
			if c.errPart != "" && rep.Status != report.StatusWarning {
				t.Errorf("条目 error 时整体应为 warning: %s", rep.Status)
			}
			if c.errPart == "" && it.Error != "" {
				t.Errorf("非致命位不应带 error: %q", it.Error)
			}
		})
	}
}

func TestCollect信号终止与单条命令超时为条目错误(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sda", false, cmdexec.Result{}, cmdexec.ErrSignaled)
	setDisk(t, f, "/dev/sdb", false, cmdexec.Result{}, context.DeadlineExceeded)
	setDisk(t, f, "/dev/sdc", false, sample(t, "smart-ata-healthy.json", 0), nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sda", "/dev/sdb", "/dev/sdc"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if !strings.Contains(m["smart[/dev/sda]"].Error, "信号") {
		t.Errorf("信号终止 = %+v", m["smart[/dev/sda]"])
	}
	if !strings.Contains(m["smart[/dev/sdb]"].Error, "超时") {
		t.Errorf("超时 = %+v", m["smart[/dev/sdb]"])
	}
	if m["smart[/dev/sdc]"].State != report.StatusOK || rep.Status != report.StatusWarning {
		t.Errorf("其余盘照常、整体 warning: %+v %s", m["smart[/dev/sdc]"], rep.Status)
	}
}

func TestCollectCtx结束返回错误(t *testing.T) {
	f := cmdexec.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	setDisk(t, f, "/dev/sda", false, cmdexec.Result{}, context.Canceled)
	if _, err := newLinux(f).Collect(ctx, input(t, smartOnly(map[string]any{"smart_devices": []any{"/dev/sda"}}), nil)); err == nil {
		t.Fatal("ctx 结束应返回 error")
	}
	setScan(t, f, cmdexec.Result{}, context.Canceled)
	if _, err := newLinux(f).Collect(ctx, input(t, smartOnly(nil), nil)); err == nil {
		t.Fatal("发现阶段 ctx 结束应返回 error")
	}
}

var sudoDenied = cmdexec.Result{Stderr: "sudo: a password is required\n", ExitCode: 1}

func TestCollect无sudo权限条目带error整体warning(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sda", false, sudoDenied, nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sda"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	it := items(rep)["smart[/dev/sda]"]
	if it.State != report.StatusUnknown || !strings.Contains(it.Error, "无 sudo 权限") || !strings.Contains(it.Error, "a password is required") {
		t.Errorf("条目 = %+v", it)
	}
	if rep.Status != report.StatusWarning {
		t.Errorf("整体 = %s", rep.Status)
	}
}

func TestCollect发现失败沿用Last键集(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, sudoDenied, nil)
	p := newLinux(f)
	if _, err := p.Collect(context.Background(), input(t, smartOnly(nil), nil)); err == nil || !strings.Contains(err.Error(), "无 sudo 权限") {
		t.Fatalf("首次运行发现失败应返回采集错误: %v", err)
	}
	v := 40.0
	last := &report.Report{Status: report.StatusOK, Items: []report.Item{
		{Key: "smart[/dev/sda]", Type: report.TypeState, State: report.StatusOK},
		{Key: "smart_temp[/dev/sda]", Type: report.TypeGauge, Unit: "°C", Value: &v},
		{Key: "smart_realloc[/dev/sda]", Type: report.TypeNumber, Value: &v},
		{Key: "smart[/dev/nvme0]", Type: report.TypeState, State: report.StatusOK},
		{Key: "raid[/dev/md0]", Type: report.TypeState, State: report.StatusOK},
	}}
	rep, err := p.Collect(context.Background(), input(t, map[string]any{}, last))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	for _, k := range []string{"smart[/dev/sda]", "smart[/dev/nvme0]"} {
		if it := m[k]; it.State != report.StatusUnknown || !strings.Contains(it.Error, "无 sudo 权限") {
			t.Errorf("%s = %+v", k, it)
		}
	}
	for _, k := range []string{"smart_temp[/dev/sda]", "smart_realloc[/dev/sda]"} {
		if it, ok := m[k]; !ok || it.Value != nil {
			t.Errorf("%s 应沿用键且无值: %+v", k, it)
		}
	}
	if _, ok := m["raid[/dev/md0]"]; ok {
		t.Error("不应沿用非 SMART 的键")
	}
	if len(rep.Items) != 4 || rep.Status != report.StatusWarning {
		t.Errorf("items=%d status=%s", len(rep.Items), rep.Status)
	}
}

func TestCollect休眠盘沿用Last子项(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sdb", false, sample(t, "smart-standby.json", 2), nil)
	v := 30.0
	last := &report.Report{Status: report.StatusOK, Items: []report.Item{
		{Key: "smart[/dev/sdb]", Type: report.TypeState, State: report.StatusOK},
		{Key: "smart_temp[/dev/sdb]", Type: report.TypeGauge, Unit: "°C", Value: &v},
		{Key: "smart_hours[/dev/sdb]", Type: report.TypeNumber, Unit: "h", Value: &v},
	}}
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sdb"}}, last))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if it := m["smart_temp[/dev/sdb]"]; it.Value != nil || it.Unit != "°C" {
		t.Errorf("温度项应沿用且无值: %+v", it)
	}
	if _, ok := m["smart_hours[/dev/sdb]"]; !ok {
		t.Error("通电时长项应沿用")
	}
	if rep.Status != report.StatusUnknown || !strings.Contains(rep.Summary, "休眠 1") {
		t.Errorf("仅有休眠盘时整体为 unknown: %s %q", rep.Status, rep.Summary)
	}
}

func TestCollect扫描中打开失败的已知盘带error(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, cmdexec.Result{Stdout: []byte("# /dev/sdd -d scsi # /dev/sdd, SCSI device open failed: No medium found\n# /dev/sde -d scsi # /dev/sde, SCSI device open failed: No medium found\n")}, nil)
	last := &report.Report{Status: report.StatusOK, Items: []report.Item{{Key: "smart[/dev/sdd]", Type: report.TypeState, State: report.StatusOK}}}
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{}, last))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if it := m["smart[/dev/sdd]"]; !strings.Contains(it.Error, "No medium found") {
		t.Errorf("已知盘打开失败 = %+v", it)
	}
	if _, ok := m["smart[/dev/sde]"]; ok {
		t.Error("未知的打开失败设备（读卡器等）应跳过")
	}
}

func TestCollect扫描为空(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, cmdexec.Result{}, nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 0 || rep.Status != report.StatusOK || !strings.Contains(rep.Summary, "未发现磁盘") {
		t.Errorf("rep = %+v", rep)
	}
}

func TestCollect扫描非零退出(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, cmdexec.Result{ExitCode: 1, Stderr: "boom"}, nil)
	if _, err := newLinux(f).Collect(context.Background(), input(t, smartOnly(nil), nil)); err == nil || !strings.Contains(err.Error(), "退出码 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectSmartctl版本过旧(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sda", false, cmdexec.Result{Stdout: []byte("=======> UNRECOGNIZED OPTION: j\n"), Stderr: "smartctl: invalid option -- 'j'\n", ExitCode: 1}, nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sda"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if it := items(rep)["smart[/dev/sda]"]; !strings.Contains(it.Error, "版本过旧") {
		t.Errorf("条目 = %+v", it)
	}
}

func TestCollect无法解析输出(t *testing.T) {
	f := cmdexec.NewFake()
	setDisk(t, f, "/dev/sda", false, cmdexec.Result{Stdout: []byte("garbage"), ExitCode: 0}, nil)
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sda"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if it := items(rep)["smart[/dev/sda]"]; !strings.Contains(it.Error, "无法解析") {
		t.Errorf("条目 = %+v", it)
	}
}

func TestCollect环境缺失(t *testing.T) {
	cases := []struct {
		name  string
		setup func(p *Plugin)
		want  string
	}{
		{"没有 sudo", func(p *Plugin) {
			p.lookPath = func(string) (string, error) { return "", errors.New("not found") }
		}, "未安装 sudo"},
		{"没有 smartctl", func(p *Plugin) {
			p.resolve = func(string) (string, error) { return "", sudocmd.ErrNotFound }
		}, "未安装 smartctl"},
		{"smartctl 不安全", func(p *Plugin) {
			p.resolve = func(string) (string, error) { return "", sudocmd.ErrUnsafe }
		}, "属主或权限不安全"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cmdexec.NewFake()
			p := newLinux(f)
			c.setup(p)
			rep, err := p.Collect(context.Background(), input(t, map[string]any{"smart_devices": []any{"/dev/sda"}}, nil))
			if err != nil {
				t.Fatal(err)
			}
			if it := items(rep)["smart[/dev/sda]"]; !strings.Contains(it.Error, c.want) {
				t.Errorf("条目 = %+v", it)
			}
			if rep.Status != report.StatusWarning || len(f.Calls()) != 0 {
				t.Errorf("status=%s calls=%v", rep.Status, f.Calls())
			}
			// 自动发现时环境缺失且没有 Last：返回采集错误。
			if _, err := p.Collect(context.Background(), input(t, smartOnly(nil), nil)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("发现阶段 err = %v", err)
			}
		})
	}
}

func TestCollect非Linux返回错误(t *testing.T) {
	p := newLinux(cmdexec.NewFake())
	p.goos = "darwin"
	if _, err := p.Collect(context.Background(), input(t, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "仅支持 Linux") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollect关闭SMART不执行命令(t *testing.T) {
	f := cmdexec.NewFake()
	rep, err := newLinux(f).Collect(context.Background(), input(t, map[string]any{"smart": false}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 0 || len(f.Calls()) != 0 || rep.Status != report.StatusOK {
		t.Errorf("rep=%+v calls=%v", rep, f.Calls())
	}
}

func TestParseScan(t *testing.T) {
	devs := parseScan(string(readTestdata(t, "scan-open-linux.txt")))
	got := []string{}
	for _, d := range devs {
		s := d.name + "|" + d.typ
		if d.openErr != "" {
			s += "|" + d.openErr
		}
		got = append(got, s)
	}
	want := "/dev/sda|sat,/dev/sdb|scsi,/dev/sdc|sat,/dev/sdd|scsi|No medium found,/dev/nvme0|nvme,/dev/bus/0|megaraid,0"
	if strings.Join(got, ",") != want {
		t.Fatalf("parseScan = %v", got)
	}
	if len(parseScan("# scan_smart_devices: glob(3) aborted matching pattern /dev/discs/disc*\n")) != 0 {
		t.Fatal("注释行应忽略")
	}
}

func TestCollect某项检查失败其余照常产出报告(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, sudoDenied, nil)
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-fnos.txt"), nil)
	p := raidPlugin(t, f, "mdstat-fnos.txt")
	rep, err := p.Collect(context.Background(), input(t, map[string]any{"btrfs": false}, nil))
	if err != nil {
		t.Fatalf("RAID 有结果时不应整体报错: %v", err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	if it := items(rep)["raid[/dev/md0]"]; it.State != report.StatusOK {
		t.Errorf("raid = %+v", it)
	}
	if rep.Status != report.StatusWarning || !strings.Contains(rep.Summary, "SMART 设备发现失败") || !strings.Contains(rep.Summary, "阵列 1 个") {
		t.Errorf("status=%s summary=%q", rep.Status, rep.Summary)
	}
}

func TestCollect全部启用的检查都失败才返回错误(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, sudoDenied, nil)
	p := newLinux(f)
	p.readFile = func(string) ([]byte, error) { return nil, context.DeadlineExceeded }
	_, err := p.Collect(context.Background(), input(t, map[string]any{}, nil))
	if err == nil || !strings.Contains(err.Error(), "SMART") || !strings.Contains(err.Error(), "/proc/mdstat") || !strings.Contains(err.Error(), "/proc/mounts") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollect三类检查合并(t *testing.T) {
	f := cmdexec.NewFake()
	setScan(t, f, cmdexec.Result{Stdout: []byte("/dev/sda -d sat # /dev/sda [SAT], ATA device\n")}, nil)
	setDisk(t, f, "/dev/sda", true, sample(t, "smart-ata-healthy.json", 0), nil)
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-fnos.txt"), nil)
	p := fnosBtrfs(t, f, "btrfs-device-stats-clean.txt", "btrfs-scrub-new-ok.txt")
	p.readFile = fakeFiles{
		"/proc/mdstat": string(readTestdata(t, "mdstat-fnos.txt")),
		"/proc/mounts": string(readTestdata(t, "mounts-fnos.txt")),
	}.read
	in := input(t, nil, nil)
	in.Clock = clock.NewFake(scrubStart.Add(24 * time.Hour))
	rep, err := p.Collect(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	for _, k := range []string{"smart[/dev/sda]", "raid[/dev/md0]", "btrfs[/vol1]", "scrub[/vol1]"} {
		if m[k].State != report.StatusOK {
			t.Errorf("%s = %+v", k, m[k])
		}
	}
	if rep.Status != report.StatusOK || strings.Count(rep.Summary, " | ") != 2 {
		t.Errorf("status=%s summary=%q", rep.Status, rep.Summary)
	}
}

func TestCollect未启用任何检查(t *testing.T) {
	rep, err := newLinux(cmdexec.NewFake()).Collect(context.Background(), input(t, map[string]any{"smart": false, "raid": false, "btrfs": false}, nil))
	if err != nil || rep.Status != report.StatusOK || !strings.Contains(rep.Summary, "未启用任何检查") {
		t.Errorf("rep=%+v err=%v", rep, err)
	}
}
