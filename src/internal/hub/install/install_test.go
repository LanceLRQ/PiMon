package install

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFreshInstallCommandSequence(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
	want := []string{
		"/usr/sbin/useradd --system --user-group --home-dir /var/lib/pimon --no-create-home --shell /usr/sbin/nologin pimon",
		"/usr/bin/systemctl is-active pimon-hub.service",
		"/usr/sbin/runuser -u pimon -- /usr/local/bin/pimon-hub setup-code --if-needed",
		"/usr/bin/systemctl daemon-reload",
		"/usr/bin/systemctl enable --now pimon-hub.service",
		"/usr/sbin/usermod -aG pimon lancelrq",
	}
	if !slices.Equal(e.run.log, want) {
		t.Fatalf("命令序列不符:\n got %q\nwant %q", e.run.log, want)
	}
	if !slices.Equal(e.fs.fixes, []string{"mkdir /var/lib/pimon"}) {
		t.Fatalf("数据目录操作: %v", e.fs.fixes)
	}
	if d := e.fs.files[dataDir]; d.mode != 0o750 || d.uid != 998 || d.gid != 998 {
		t.Fatalf("数据目录 = %+v", d)
	}
	bin := e.wrote(binPath)
	if bin == nil || bin.mode != 0o755 || e.fs.files[binPath].data != "BIN-V1" {
		t.Fatalf("二进制 = %+v", bin)
	}
	unit := e.wrote(unitPath)
	if unit == nil || unit.mode != 0o644 || e.fs.files[unitPath].data != unitContent {
		t.Fatalf("unit = %+v", unit)
	}
	if e.wrote(sysctlPath) != nil {
		t.Fatal("ping_group_range 已满足时不应写 sysctl 配置")
	}
	out := e.out.String()
	for _, s := range []string{"[完成] 创建系统用户 pimon", "首次设置码：ABCD-2345（有效期至 2026-10-03 10:00:00）", "http://10.22.33.198:31415", "重新登录"} {
		if !strings.Contains(out, s) {
			t.Errorf("输出缺少 %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "    http://127.0.0.1") {
		t.Errorf("回环地址不应出现在访问地址里:\n%s", out)
	}
}

func TestSecondRunIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	// 第二次：服务在运行、占用端口的正是自身，管理员已设置（setup-code 以 3 退出）。
	e.run.log, e.fs.writes, e.fs.fixes = nil, nil, nil
	e.out.Reset()
	e.run.setupExit = 3
	e.ports.ls = []Listener{{PID: 321, Exe: binPath}, {PID: 321, Exe: binPath}}
	if err := e.install(t); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
	want := []string{
		"/usr/bin/systemctl is-active pimon-hub.service",
		"/usr/bin/systemctl daemon-reload",
		"/usr/bin/systemctl enable --now pimon-hub.service",
		"/usr/bin/systemctl show -p ActiveEnterTimestampMonotonic --value pimon-hub.service",
		"/usr/sbin/runuser -u pimon -- /usr/local/bin/pimon-hub setup-code --if-needed",
	}
	if !slices.Equal(e.run.log, want) {
		t.Fatalf("二次运行命令序列:\n got %q\nwant %q", e.run.log, want)
	}
	if len(e.fs.writes) != 0 || len(e.fs.fixes) != 0 {
		t.Fatalf("二次运行不应写文件: %v %v", e.fs.writes, e.fs.fixes)
	}
	out := e.out.String()
	for _, s := range []string{"[已存在] 系统用户 pimon", "[已存在] 数据目录", "[已存在] 二进制", "[已存在] 端口 31415（pimon-hub.service 已在运行）",
		"[跳过] 设置码（已设置管理员）", "[已存在] systemd 单元", "[已存在] 桌面用户 lancelrq 在 pimon 组", "[已存在] net.ipv4.ping_group_range", "管理员已设置"} {
		if !strings.Contains(out, s) {
			t.Errorf("输出缺少 %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "重新登录") || strings.Contains(out, "首次设置码：") {
		t.Errorf("二次运行不应提示重新登录或设置码:\n%s", out)
	}
}

func TestUpgradeReplacesBinaryAndRestarts(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	e.run.log, e.fs.writes = nil, nil
	e.out.Reset()
	e.fs.files["/tmp/new/pimon-hub"].data = "BIN-V2"
	e.run.setupExit = 3
	// 旧进程的二进制已被替换前的 exe 链接就是这个形式；替换后带 (deleted)。
	e.ports.ls = []Listener{{PID: 321, Exe: binPath + " (deleted)"}}
	if err := e.install(t); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
	if e.fs.files[binPath].data != "BIN-V2" || e.wrote(binPath) == nil {
		t.Fatal("二进制应被替换")
	}
	if e.wrote(unitPath) != nil {
		t.Fatal("unit 没变不应重写")
	}
	if indexOfCmd(e.run.log, " restart ") < 0 {
		t.Fatalf("升级后应重启服务，命令序列 %q", e.run.log)
	}
	if !strings.Contains(e.out.String(), "[完成] 升级二进制") || !strings.Contains(e.out.String(), "重启 pimon-hub.service（二进制已升级）") {
		t.Fatalf("输出:\n%s", e.out)
	}
}

func TestUnitChangeRestartsRunningService(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	e.run.log, e.fs.writes = nil, nil
	e.run.setupExit = 3
	e.ports.ls = []Listener{{PID: 1, Exe: binPath}}
	e.fs.files[unitPath].data = "旧版 unit"
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.fs.files[unitPath].data != unitContent {
		t.Fatal("unit 应被更新")
	}
	if indexOfCmd(e.run.log, " restart ") < 0 {
		t.Fatalf("unit 变化应重启: %q", e.run.log)
	}
}

func TestPortHeldByOtherProcessStopsInstall(t *testing.T) {
	e := newTestEnv(t)
	e.ports.ls = []Listener{{PID: 777, Exe: "/usr/bin/python3"}}
	err := e.install(t)
	if err == nil || !strings.Contains(err.Error(), "检查端口") {
		t.Fatalf("err = %v", err)
	}
	out := e.out.String()
	for _, s := range []string{"[失败] 检查端口：端口 31415 已被占用：pid 777（/usr/bin/python3）", "已完成的步骤", "完成：创建系统用户 pimon"} {
		if !strings.Contains(out, s) {
			t.Errorf("输出缺少 %q:\n%s", s, out)
		}
	}
	for _, c := range e.run.log {
		if strings.Contains(c, "setup-code") || strings.Contains(c, "enable") {
			t.Fatalf("端口冲突后不应继续: %q", e.run.log)
		}
	}
}

func TestPortHeldByOwnBinaryButServiceInactiveIsError(t *testing.T) {
	e := newTestEnv(t)
	e.ports.ls = []Listener{{PID: 5, Exe: binPath}}
	// 服务没在运行（is-active 非零），占用者即使是同一二进制也可能是手工启动的 serve。
	if err := e.install(t); err == nil || !strings.Contains(err.Error(), "检查端口") {
		t.Fatalf("err = %v", err)
	}
}

func TestPortUnknownOwner(t *testing.T) {
	e := newTestEnv(t)
	e.ports.ls = []Listener{{}}
	err := e.install(t)
	if err == nil || !strings.Contains(e.out.String(), "未知进程") {
		t.Fatalf("err=%v\n%s", err, e.out)
	}
}

func TestNoDesktopUserSkipsGroup(t *testing.T) {
	e := newTestEnv(t)
	delete(e.fs.files, lightdmMainConf)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.run.log {
		if strings.Contains(c, "usermod") {
			t.Fatalf("没有桌面用户不应加组: %q", e.run.log)
		}
	}
	if !strings.Contains(e.out.String(), "[跳过] 桌面用户加入 pimon 组") || !strings.Contains(e.out.String(), "--desktop-user") {
		t.Fatalf("输出:\n%s", e.out)
	}
}

func TestDesktopUserFlagOverridesLightdm(t *testing.T) {
	e := newTestEnv(t)
	e.users.users["pi"] = User{UID: 1001, GID: 1001}
	e.opts.DesktopUser = "pi"
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(e.run.log, "/usr/sbin/usermod -aG pimon pi") || slices.Contains(e.run.log, "/usr/sbin/usermod -aG pimon lancelrq") {
		t.Fatalf("命令序列 %q", e.run.log)
	}
}

func TestDesktopUserFlagUnknownUserFails(t *testing.T) {
	e := newTestEnv(t)
	e.opts.DesktopUser = "ghost"
	if err := e.install(t); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectedDesktopUserMissingSkips(t *testing.T) {
	e := newTestEnv(t)
	delete(e.users.users, "lancelrq")
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "在系统里不存在") {
		t.Fatalf("输出:\n%s", e.out)
	}
}

func TestSysctlWrittenWhenGroupNotCovered(t *testing.T) {
	e := newTestEnv(t)
	e.fs.files[pingRange].data = "1\t0\n"
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	w := e.wrote(sysctlPath)
	if w == nil || e.fs.files[sysctlPath].data != "net.ipv4.ping_group_range = 0 2147483647\n" {
		t.Fatalf("sysctl 配置 = %+v", w)
	}
	if e.run.log[len(e.run.log)-1] != "/usr/sbin/sysctl -p /etc/sysctl.d/99-pimon.conf" {
		t.Fatalf("命令序列 %q", e.run.log)
	}
}

func TestSysctlRangeNotCoveringPimonGID(t *testing.T) {
	e := newTestEnv(t)
	e.fs.files[pingRange].data = "0\t100\n" // pimon gid 998 不在区间内
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.wrote(sysctlPath) == nil {
		t.Fatal("区间不含 pimon gid 时应写入配置")
	}
}

func TestEnvironmentChecks(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*testEnv)
		want string
	}{
		{"非 Linux", func(e *testEnv) { e.deps.GOOS = "darwin" }, "仅支持 Linux"},
		{"非 root", func(e *testEnv) { e.deps.EUID = 1000 }, "必须以 root 运行"},
		{"无 systemd", func(e *testEnv) { delete(e.fs.files, "/run/systemd/system") }, "systemd"},
	}
	for _, c := range cases {
		e := newTestEnv(t)
		c.mod(e)
		err := e.install(t)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if len(e.run.log) != 0 || len(e.fs.writes) != 0 {
			t.Errorf("%s: 检测失败时不应有任何改动: %q %v", c.name, e.run.log, e.fs.writes)
		}
		if !strings.Contains(e.out.String(), "尚未完成任何步骤") {
			t.Errorf("%s: 输出 %q", c.name, e.out)
		}
	}
}

func TestHealthTimeoutFails(t *testing.T) {
	e := newTestEnv(t)
	*e.health = 1000
	err := e.install(t)
	if err == nil || !strings.Contains(err.Error(), "journalctl") {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(e.run.log, "/usr/sbin/usermod -aG pimon lancelrq") {
		t.Fatal("服务未就绪后不应继续加组")
	}
}

func TestHealthRetriesUntilReady(t *testing.T) {
	e := newTestEnv(t)
	*e.health = 5
	if err := e.install(t); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
}

func TestTokenNeverAppearsFails(t *testing.T) {
	e := newTestEnv(t)
	e.run.failOn = nil
	// 启动成功但令牌文件不出现。
	e.deps.Runner = runnerFunc(func(ctx context.Context, argv ...string) (string, error) {
		out, err := e.run.Run(ctx, argv...)
		delete(e.fs.files, tokenPath)
		return out, err
	})
	err := e.install(t)
	if err == nil || !strings.Contains(err.Error(), "令牌") {
		t.Fatalf("err = %v", err)
	}
}

type runnerFunc func(ctx context.Context, argv ...string) (string, error)

func (f runnerFunc) Run(ctx context.Context, argv ...string) (string, error) { return f(ctx, argv...) }

func TestSetupCodeFailureStops(t *testing.T) {
	e := newTestEnv(t)
	e.run.failOn = map[string]error{cmdRunuser: errors.New("boom")}
	err := e.install(t)
	if err == nil || !strings.Contains(err.Error(), "生成首次设置码") {
		t.Fatalf("err = %v", err)
	}
	if e.wrote(unitPath) != nil {
		t.Fatal("设置码失败后不应写 unit")
	}
}

func TestSetupCodeOutputWithoutCodeFails(t *testing.T) {
	e := newTestEnv(t)
	e.run.setupOut = "什么也没有\n"
	if err := e.install(t); err == nil || !strings.Contains(err.Error(), "没有设置码") {
		t.Fatalf("err = %v", err)
	}
}

func TestDataDirWrongOwnerIsFixed(t *testing.T) {
	e := newTestEnv(t)
	e.users.users["pimon"] = User{UID: 998, GID: 998}
	e.fs.files[dataDir] = &fakeFile{dir: true, mode: 0o755, uid: 0, gid: 0}
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e.fs.fixes, []string{"fix /var/lib/pimon"}) || e.fs.files[dataDir].uid != 998 || e.fs.files[dataDir].mode != 0o750 {
		t.Fatalf("fixes=%v dir=%+v", e.fs.fixes, e.fs.files[dataDir])
	}
	if slices.ContainsFunc(e.run.log, func(s string) bool { return strings.Contains(s, "useradd") }) {
		t.Fatal("用户已存在不应 useradd")
	}
}

func TestUnitContent(t *testing.T) {
	for _, line := range []string{
		"Type=notify", "NotifyAccess=main", "WatchdogSec=30s", "Restart=always", "User=pimon", "Group=pimon",
		"SupplementaryGroups=video", "ProtectSystem=strict", "ReadWritePaths=/var/lib/pimon", "ProtectHome=yes",
		"PrivateTmp=yes", "NoNewPrivileges=yes", "TimeoutStopSec=20s", "ExecStart=/usr/local/bin/pimon-hub serve",
	} {
		if !slices.Contains(strings.Split(unitContent, "\n"), line) {
			t.Errorf("unit 缺少 %q", line)
		}
	}
	for _, bad := range []string{"PrivateDevices", "DevicePolicy", "RestrictAddressFamilies"} {
		if strings.Contains(unitContent, bad) {
			t.Errorf("unit 不应包含 %s", bad)
		}
	}
}

func TestCommandRejectsBadArgs(t *testing.T) {
	var ue UsageError
	if err := Command(context.Background(), []string{"extra"}, &strings.Builder{}); !errors.As(err, &ue) {
		t.Fatalf("位置参数应是用法错误: %v", err)
	}
	if err := Command(context.Background(), []string{"--bogus"}, &strings.Builder{}); !errors.As(err, &ue) {
		t.Fatalf("未知参数应是用法错误: %v", err)
	}
}

func (f runnerFunc) RunEnv(ctx context.Context, _ []string, argv ...string) (string, error) {
	return f(ctx, argv...)
}

func indexOfCmd(log []string, sub string) int {
	return slices.IndexFunc(log, func(s string) bool { return strings.Contains(s, sub) })
}

func TestRunningServiceSetupCodeAfterRestartAndReady(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	e.run.log, e.run.healthAt = nil, -1
	e.fs.files["/tmp/new/pimon-hub"].data = "BIN-V2"
	e.ports.ls = []Listener{{PID: 321, Exe: binPath + " (deleted)"}}
	if err := e.install(t); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
	restart, setup := indexOfCmd(e.run.log, " restart "), indexOfCmd(e.run.log, "setup-code")
	if restart < 0 || setup < 0 || setup < restart || setup < e.run.healthAt {
		t.Fatalf("服务已在运行时 setup-code 必须在 restart 与就绪之后: restart=%d health=%d setup=%d %q", restart, e.run.healthAt, setup, e.run.log)
	}
}

func TestFreshInstallSetupCodeBeforeServiceStart(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if setup, enable := indexOfCmd(e.run.log, "setup-code"), indexOfCmd(e.run.log, " enable "); setup < 0 || setup > enable {
		t.Fatalf("全新安装必须先取码再启动服务: %q", e.run.log)
	}
}

func TestSetupCodeRunsWithCleanEnvironment(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	got := e.run.envs["/usr/sbin/runuser -u pimon -- /usr/local/bin/pimon-hub setup-code --if-needed"]
	if !slices.Equal(got, []string{"PIMON_DATA_DIR=/var/lib/pimon"}) {
		t.Fatalf("setup-code 的环境覆盖 = %q", got)
	}
}

func TestBuildEnvDropsInheritedPimonVars(t *testing.T) {
	got := buildEnv([]string{"PATH=/usr/bin", "PIMON_ADDR=:1", "PIMON_DATA_DIR=/root/x", "PIMON_LOG_LEVEL=debug", "HOME=/root"}, []string{"PIMON_DATA_DIR=/var/lib/pimon"})
	want := []string{"PATH=/usr/bin", "HOME=/root", "PIMON_DATA_DIR=/var/lib/pimon"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRerunAfterFailedUpgradeStillRestarts(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	e.fs.files["/tmp/new/pimon-hub"].data = "BIN-V2"
	e.run.setupExit = 3
	e.ports.ls = []Listener{{PID: 321, Exe: binPath}}
	e.run.failOn = map[string]error{cmdSystemctl + " daemon-reload": errors.New("boom")}
	if err := e.install(t); err == nil {
		t.Fatal("第一次升级应失败")
	}
	// 重跑：二进制已是新的，但旧进程仍在跑（exe 已是 deleted）。
	e.run.failOn, e.run.log = nil, nil
	e.ports.ls = []Listener{{PID: 321, Exe: binPath + " (deleted)"}}
	if err := e.install(t); err != nil {
		t.Fatalf("%v\n%s", err, e.out)
	}
	if indexOfCmd(e.run.log, " restart ") < 0 {
		t.Fatalf("重跑应重启仍在运行旧代码的服务: %q", e.run.log)
	}
}

func TestRerunAfterUnitWrittenButRestartFailedStillRestarts(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	e.run.setupExit = 3
	e.ports.ls = []Listener{{PID: 1, Exe: binPath}}
	e.run.startedAt = e.run.startedAt.Add(-30 * time.Minute) // 服务比 unit 文件旧
	e.fs.files[unitPath].data = "旧版 unit"
	e.run.failOn = map[string]error{cmdSystemctl + " restart": errors.New("boom")}
	if err := e.install(t); err == nil {
		t.Fatal("第一次应在 restart 失败")
	}
	e.run.failOn, e.run.log = nil, nil
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.wrote(unitPath) == nil || indexOfCmd(e.run.log, " restart ") < 0 {
		t.Fatalf("unit 已写入但服务未重启，重跑应补重启: %q", e.run.log)
	}
	// 重启成功后再跑不应反复重启。
	e.run.log = nil
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if indexOfCmd(e.run.log, " restart ") >= 0 {
		t.Fatalf("服务已比 unit 新，不应再重启: %q", e.run.log)
	}
}
