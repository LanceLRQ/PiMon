package storagehealth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/plugintest"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

func raidOnly(cfg map[string]any) map[string]any {
	out := map[string]any{"smart": false, "btrfs": false}
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

func setMdadm(t *testing.T, f *cmdexec.Fake, dev string, res cmdexec.Result, err error) {
	t.Helper()
	f.Set(res, err, sudoPath, sudoArgsFor(t, mdadmPath, sudocmd.OpMdadmDetail, dev)...)
}

func detailSample(t *testing.T, name string) cmdexec.Result {
	return cmdexec.Result{Stdout: readTestdata(t, name)}
}

// raidPlugin 返回读取 mdstat 样本的插件。
func raidPlugin(t *testing.T, f *cmdexec.Fake, mdstat string) *Plugin {
	t.Helper()
	p := newLinux(f)
	p.readFile = fakeFiles{"/proc/mdstat": string(readTestdata(t, mdstat))}.read
	return p
}

func TestParseMdstat样本(t *testing.T) {
	cases := []struct {
		file string
		want []string // 每个阵列：名|级别|成员数|失效|[总/可用]|同步
	}{
		{"mdstat-fnos.txt", []string{"md0|raid1|1|0|1/1|"}},
		{"mdstat-none.txt", nil},
		{"mdstat-degraded.txt", []string{"md0|raid1|2|1|2/1|"}},
		{"mdstat-rebuilding.txt", []string{"md0|raid1|2|0|2/1|recovery 12.6", "md1|raid5|3|0|3/3|resync pending"}},
		{"mdstat-check.txt", []string{"md0|raid1|2|0|2/2|check 8.4"}},
		{"mdstat-inactive.txt", []string{"md127||3|0|0/0|"}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			var got []string
			for _, a := range parseMdstat(string(readTestdata(t, c.file))) {
				sync := ""
				switch {
				case a.syncPending != "":
					sync = a.syncAction + " pending"
				case a.syncPercent != nil:
					sync = a.syncAction + " " + strconv.FormatFloat(*a.syncPercent, 'f', -1, 64)
				}
				got = append(got, fmt.Sprintf("%s|%s|%d|%d|%d/%d|%s", a.name, a.level, len(a.members), len(a.failedMembers()), a.total, a.working, sync))
			}
			if strings.Join(got, ";") != strings.Join(c.want, ";") {
				t.Fatalf("got %v\nwant %v", got, c.want)
			}
		})
	}
}

func TestCollectRAID飞牛单盘RAID1(t *testing.T) {
	f := cmdexec.NewFake()
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-fnos.txt"), nil)
	p := raidPlugin(t, f, "mdstat-fnos.txt")
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	it := m["raid[/dev/md0]"]
	if it.State != report.StatusOK || it.Error != "" || !strings.Contains(it.Text, "fnOS:0") || !strings.Contains(it.Text, "RAID1") {
		t.Errorf("raid = %+v", it)
	}
	if _, ok := m["raid_sync[/dev/md0]"]; ok {
		t.Error("无同步时不应出现 raid_sync")
	}
	if rep.Status != report.StatusOK || !strings.Contains(rep.Summary, "阵列 1 个") {
		t.Errorf("status=%s summary=%q", rep.Status, rep.Summary)
	}
	calls := f.Calls()
	if len(calls) != 1 || calls[0].Args[2] != mdadmPath || calls[0].Args[3] != "--detail" || calls[0].Args[4] != "/dev/md0" {
		t.Errorf("调用 = %v", calls)
	}
}

func TestCollectRAID降级为critical(t *testing.T) {
	f := cmdexec.NewFake()
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-degraded.txt"), nil)
	p := raidPlugin(t, f, "mdstat-degraded.txt")
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	it := items(rep)["raid[/dev/md0]"]
	if it.State != report.StatusCritical || !strings.Contains(it.Text, "成员失效 sdb1") || !strings.Contains(it.Text, "降级 1/2") {
		t.Errorf("raid = %+v", it)
	}
	if rep.Status != report.StatusCritical || !strings.Contains(rep.Summary, "异常 1") {
		t.Errorf("status=%s summary=%q", rep.Status, rep.Summary)
	}
}

func TestCollectRAID重建中为warning并带进度(t *testing.T) {
	f := cmdexec.NewFake()
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-rebuilding.txt"), nil)
	setMdadm(t, f, "/dev/md1", detailSample(t, "mdadm-detail-fnos.txt"), nil)
	p := raidPlugin(t, f, "mdstat-rebuilding.txt")
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	if it := m["raid[/dev/md0]"]; it.State != report.StatusWarning || !strings.Contains(it.Text, "recovery 12.6%") {
		t.Errorf("md0 = %+v", it)
	}
	if v := val(t, m, "raid_sync[/dev/md0]"); v != 12.6 {
		t.Errorf("进度 = %v", v)
	}
	if it := m["raid[/dev/md1]"]; it.State != report.StatusWarning {
		t.Errorf("同步排队 = %+v", it)
	}
	if _, ok := m["raid_sync[/dev/md1]"]; ok {
		t.Error("排队中没有进度，不应出现 raid_sync")
	}
	if rep.Status != report.StatusWarning {
		t.Errorf("status = %s", rep.Status)
	}
}

func TestCollectRAID例行check为ok但有进度(t *testing.T) {
	f := cmdexec.NewFake()
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-fnos.txt"), nil)
	p := raidPlugin(t, f, "mdstat-check.txt")
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	if it := m["raid[/dev/md0]"]; it.State != report.StatusOK {
		t.Errorf("check = %+v", it)
	}
	if v := val(t, m, "raid_sync[/dev/md0]"); v != 8.4 {
		t.Errorf("进度 = %v", v)
	}
}

func TestCollectRAID未激活与NotStarted(t *testing.T) {
	f := cmdexec.NewFake()
	setMdadm(t, f, "/dev/md127", detailSample(t, "mdadm-detail-notstarted.txt"), nil)
	p := raidPlugin(t, f, "mdstat-inactive.txt")
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	it := items(rep)["raid[/dev/md127]"]
	if it.State != report.StatusCritical || !strings.Contains(it.Text, "未激活") || !strings.Contains(it.Text, "Not Started") {
		t.Errorf("raid = %+v", it)
	}
}

func TestCollectRAIDmdadm细节更严重时取更严重者(t *testing.T) {
	// mdstat 看起来正常，mdadm 报告 degraded：取 critical。
	f := cmdexec.NewFake()
	setMdadm(t, f, "/dev/md0", detailSample(t, "mdadm-detail-degraded.txt"), nil)
	p := raidPlugin(t, f, "mdstat-fnos.txt")
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	if it := items(rep)["raid[/dev/md0]"]; it.State != report.StatusCritical {
		t.Errorf("raid = %+v", it)
	}
}

func TestCollectRAID详情失败保留mdstat结论(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *cmdexec.Fake, p *Plugin)
		want  string
	}{
		{"无 sudo 权限", func(f *cmdexec.Fake, _ *Plugin) { setMdadm(t, f, "/dev/md0", sudoDenied, nil) }, "无 sudo 权限"},
		{"超时", func(f *cmdexec.Fake, _ *Plugin) {
			setMdadm(t, f, "/dev/md0", cmdexec.Result{}, context.DeadlineExceeded)
		}, "超时"},
		{"没有 mdadm", func(_ *cmdexec.Fake, p *Plugin) {
			p.resolve = func(string) (string, error) { return "", sudocmd.ErrNotFound }
		}, "未安装 mdadm"},
		{"输出无法解析", func(f *cmdexec.Fake, _ *Plugin) {
			setMdadm(t, f, "/dev/md0", cmdexec.Result{Stdout: []byte("garbage")}, nil)
		}, "无法解析"},
		{"非零退出", func(f *cmdexec.Fake, _ *Plugin) {
			setMdadm(t, f, "/dev/md0", cmdexec.Result{ExitCode: 1, Stderr: "boom"}, nil)
		}, "退出码 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cmdexec.NewFake()
			p := raidPlugin(t, f, "mdstat-fnos.txt")
			c.setup(f, p)
			rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
			if err != nil {
				t.Fatal(err)
			}
			it := items(rep)["raid[/dev/md0]"]
			if it.State != report.StatusOK || it.Error != "" || !strings.Contains(it.Text, "阵列详情不可用") || !strings.Contains(it.Text, c.want) {
				t.Errorf("mdstat 结论应保留且注明详情不可用: %+v", it)
			}
			if rep.Status != report.StatusWarning {
				t.Errorf("详情失败整体至少 warning: %s", rep.Status)
			}
		})
	}
}

func TestCollectRAIDmdstat不存在表示没有阵列(t *testing.T) {
	f := cmdexec.NewFake()
	p := newLinux(f)
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 0 || rep.Status != report.StatusOK || !strings.Contains(rep.Summary, "未发现 md 阵列") || len(f.Calls()) != 0 {
		t.Errorf("rep = %+v", rep)
	}
}

func TestCollectRAIDmdstat读取失败(t *testing.T) {
	f := cmdexec.NewFake()
	p := newLinux(f)
	p.readFile = func(string) ([]byte, error) { return nil, context.DeadlineExceeded }
	if _, err := p.Collect(context.Background(), input(t, raidOnly(nil), nil)); err == nil || !strings.Contains(err.Error(), "/proc/mdstat") {
		t.Fatalf("所有启用检查都失败应返回 error: %v", err)
	}
	last := &report.Report{Status: report.StatusOK, Items: []report.Item{
		{Key: "raid[/dev/md0]", Type: report.TypeState, State: report.StatusOK},
		{Key: "raid_sync[/dev/md0]", Type: report.TypeGauge, Unit: "%"},
	}}
	rep, err := p.Collect(context.Background(), input(t, raidOnly(nil), last))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, p.Manifest(), rep)
	m := items(rep)
	if it := m["raid[/dev/md0]"]; it.State != report.StatusUnknown || it.Error == "" {
		t.Errorf("沿用的阵列应为 unknown 带 error: %+v", it)
	}
	if it, ok := m["raid_sync[/dev/md0]"]; !ok || it.Value != nil {
		t.Errorf("同步进度项应沿用键且无值: %+v", it)
	}
	if rep.Status != report.StatusWarning {
		t.Errorf("status = %s", rep.Status)
	}
}

func TestParseMdDetail(t *testing.T) {
	d, ok := parseMdDetail(string(readTestdata(t, "mdadm-detail-rebuilding.txt")))
	if !ok || d.name != "fnOS:0" || d.state != "clean, degraded, recovering" || d.status() != report.StatusWarning {
		t.Errorf("detail = %+v", d)
	}
	if _, ok := parseMdDetail("无关内容\n"); ok {
		t.Error("没有 State 行应返回 false")
	}
}
