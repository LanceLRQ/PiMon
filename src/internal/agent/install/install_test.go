package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

const wantSystemUnit = `[Unit]
Description=PiMon agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/pimon-agent run
User=pimon
Group=pimon
Restart=always
RestartSec=2s
RuntimeDirectory=pimon-agent
RuntimeDirectoryMode=0750
ProtectSystem=strict
# 守护进程会原子改写 /etc/pimon-agent/agent.json（同目录临时文件 + rename），配置目录也要可写。
ReadWritePaths=/var/lib/pimon-agent /etc/pimon-agent
PrivateTmp=yes
# 不设置 NoNewPrivileges=yes：storage-health 插件（M2d）要经 sudo 调用 smartctl 等命令，
# 开启后 sudo 无法提权。

[Install]
WantedBy=multi-user.target
`

const wantUserUnit = `[Unit]
Description=PiMon agent（用户模式）

[Service]
ExecStart=/home/alice/.pimon/bin/pimon-agent run --user
Restart=always
RestartSec=2s

[Install]
WantedBy=default.target
`

const wantPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- PiMon agent。日志由守护进程写入 --log-file 指定的文件：超过 5 MiB 时改名为 .1 再重开，只保留一个旧文件。
     不使用 StandardOutPath/StandardErrorPath，因为 launchd 不会轮转它们。 -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.lancelrq.pimon-agent</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/alice/.pimon/bin/pimon-agent</string>
		<string>run</string>
		<string>--log-file</string>
		<string>/Users/alice/.pimon/agent.log</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`

func readConf(t *testing.T, e *testEnv, name string) config.Config {
	t.Helper()
	var c config.Config
	if err := json.Unmarshal([]byte(e.file(t, name).data), &c); err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return c
}

// ---- Linux 系统模式 ----

func TestSystem_拨入全新安装_命令序列与文件(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())

	assertLines(t, "命令序列", e.runner.log, []string{
		"/usr/sbin/useradd --system --user-group --home-dir /var/lib/pimon-agent --no-create-home --shell /usr/sbin/nologin pimon",
		"/usr/bin/systemctl is-active pimon-agent.service",
		"/usr/sbin/sysctl -p /etc/sysctl.d/99-pimon-agent.conf",
		"/usr/bin/systemctl daemon-reload",
		"/usr/bin/systemctl enable --now pimon-agent.service",
	})
	assertLines(t, "文件操作", e.fs.ops, []string{
		"mkdir /var/lib/pimon-agent 0750 990:990",
		"mkdir /etc/pimon-agent 0700 990:990",
		"write /usr/local/bin/pimon-agent 0755 nil",
		"write /etc/sysctl.d/99-pimon-agent.conf 0644 nil",
		"write /etc/pimon-agent/agent.json 0600 990:990",
		"write /etc/systemd/system/pimon-agent.service 0644 nil",
	})
	if got := e.file(t, "/etc/systemd/system/pimon-agent.service").data; got != wantSystemUnit {
		t.Fatalf("单元内容不符:\n%s", got)
	}
	if got := e.file(t, "/etc/sysctl.d/99-pimon-agent.conf").data; got != "net.ipv4.ping_group_range = 0 2147483647\n" {
		t.Fatalf("sysctl 内容不符: %q", got)
	}
	c := readConf(t, e, "/etc/pimon-agent/agent.json")
	if c.Mode != config.ModeDialIn || c.HubURL != testHub || c.HubPin != testFP || string(c.Credential) != e.pairCred || c.HubHostname != "pimon" {
		t.Fatalf("配置不符: %+v（凭据 %q）", c, string(c.Credential))
	}
	if len(e.pairReqs) != 1 {
		t.Fatalf("配对调用 %d 次", len(e.pairReqs))
	}
	r := e.pairReqs[0]
	if r.HubURL != testHub || r.Pin.String() != testFP || r.Code != "ABCD-EFGH" || r.Hello == nil {
		t.Fatalf("配对请求不符: %+v", r)
	}
	if e.statusN == 0 {
		t.Fatal("没有等待守护进程就绪")
	}
	out := e.out.String()
	assertContains(t, out, "[完成] 创建系统用户 pimon", "[完成] 配对", "pimon-agent status")
	assertNotContains(t, out, e.pairCred, "ABCD-EFGH")
}

func TestSystem_拨入重跑幂等_跳过已完成步骤(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.reset()
	e.mustInstall(t, dialIn())

	assertLines(t, "重跑命令", e.runner.log, []string{
		"/usr/bin/systemctl is-active pimon-agent.service",
		"/usr/bin/systemctl is-enabled pimon-agent.service",
	})
	assertLines(t, "重跑文件操作", e.fs.ops, nil)
	if len(e.pairReqs) != 0 {
		t.Fatal("已配对时不应再次配对")
	}
	assertContains(t, e.out.String(),
		"[已存在] 系统用户 pimon", "[已存在] 二进制 /usr/local/bin/pimon-agent（内容相同）",
		"[已存在] net.ipv4.ping_group_range", "[跳过] 配对", "[已存在] systemd 单元", "[已存在] pimon-agent.service（已启用并在运行）")
}

func TestSystem_升级二进制后重启(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.reset()
	e.selfData = "BINARY-v3"
	e.mustInstall(t, Options{})

	assertLines(t, "升级命令", e.runner.log, []string{
		"/usr/bin/systemctl is-active pimon-agent.service",
		"/usr/bin/systemctl enable --now pimon-agent.service",
		"/usr/bin/systemctl restart pimon-agent.service",
	})
	if e.file(t, "/usr/local/bin/pimon-agent").data != "BINARY-v3" {
		t.Fatal("二进制未替换")
	}
	assertContains(t, e.out.String(), "[完成] 升级二进制", "[完成] 重启 pimon-agent.service（二进制已升级）")
}

func TestSystem_当前可执行文件即安装位置_按启动时间判断是否重启(t *testing.T) {
	for _, tc := range []struct {
		name    string
		monoUS  int64
		restart bool
	}{
		{"服务早于二进制", 100e6, true},   // 启动于 t0-900s，二进制 mtime 为 t0
		{"服务晚于二进制", 1000e6, false}, // 启动于 t0
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			e.mustInstall(t, dialIn())
			e.reset()
			e.fs.files["/usr/local/bin/pimon-agent"].data = "BINARY-v3"
			if !tc.restart {
				e.fs.files["/usr/local/bin/pimon-agent"].mtime = t0.Add(-2000e9)
			}
			e.runner.monoUS = tc.monoUS
			d := e.deps()
			d.Self = "/usr/local/bin/pimon-agent"
			if err := Install(context.Background(), d, Options{}); err != nil {
				t.Fatalf("install: %v\n%s", err, e.out.String())
			}
			if e.file(t, "/usr/local/bin/pimon-agent").data != "BINARY-v3" {
				t.Fatal("不应自己读自己再写回")
			}
			assertContains(t, e.out.String(), "当前可执行文件即安装位置")
			if got := hasCmd(e.runner.log, "/usr/bin/systemctl restart"); got != tc.restart {
				t.Fatalf("restart=%v，期望 %v；命令 %v", got, tc.restart, e.runner.log)
			}
		})
	}
}

func TestSystem_拨出全新安装_写配置并提示稍候配对(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialOut())

	assertLines(t, "命令序列", e.runner.log, []string{
		"/usr/sbin/useradd --system --user-group --home-dir /var/lib/pimon-agent --no-create-home --shell /usr/sbin/nologin pimon",
		"/usr/bin/systemctl is-active pimon-agent.service",
		"/usr/sbin/sysctl -p /etc/sysctl.d/99-pimon-agent.conf",
		"/usr/bin/systemctl daemon-reload",
		"/usr/bin/systemctl enable --now pimon-agent.service",
	})
	if len(e.pairReqs) != 0 {
		t.Fatal("拨出模式不应配对")
	}
	if got := e.ports.got; len(got) != 1 || got[0] != 31418 {
		t.Fatalf("端口检查 %v", got)
	}
	c := readConf(t, e, "/etc/pimon-agent/agent.json")
	if c.Mode != config.ModeDialOut || c.Listen != ":31418" || c.Credential != "" {
		t.Fatalf("配置不符: %+v", c)
	}
	f := e.file(t, "/etc/pimon-agent/agent.json")
	if f.mode != 0o600 || f.uid != 990 || f.gid != 990 {
		t.Fatalf("配置属主权限不符: %04o %d:%d", f.mode, f.uid, f.gid)
	}
	assertContains(t, e.out.String(), "稍候", "sudo pimon-agent pair")
}

func TestSystem_拨出重跑保留已配对的凭据哈希(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialOut())
	c := readConf(t, e, "/etc/pimon-agent/agent.json")
	c.CredentialHash = "abcdef0123456789"
	data, _ := config.Marshal(c)
	e.fs.files["/etc/pimon-agent/agent.json"].data = string(data)
	e.reset()
	e.mustInstall(t, Options{Listen: ":31419"})
	c = readConf(t, e, "/etc/pimon-agent/agent.json")
	if c.Listen != ":31419" || c.CredentialHash != "abcdef0123456789" {
		t.Fatalf("配置不符: %+v", c)
	}
	assertContains(t, e.out.String(), "[完成] 重启 pimon-agent.service（配置已变化）")
}

func TestSystem_切换连接模式清除原配对并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.reset()
	e.mustInstall(t, dialOut())
	c := readConf(t, e, "/etc/pimon-agent/agent.json")
	if c.Mode != config.ModeDialOut || c.Credential != "" || c.HubURL != "" {
		t.Fatalf("配置不符: %+v", c)
	}
	assertContains(t, e.out.String(), "[警告] 连接模式")
}

func TestSystem_目录与配置属主不符时修正(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.users.users["pimon"] = sysinstall.User{UID: 990, GID: 990}
	e.fs.dir("/var/lib/pimon-agent", 0o755, 0, 0)
	e.fs.dir("/etc/pimon-agent", 0o755, 0, 0)
	e.mustInstall(t, dialOut())
	for _, d := range []struct {
		name string
		mode fs.FileMode
	}{{"/var/lib/pimon-agent", 0o750}, {"/etc/pimon-agent", 0o700}} {
		f := e.file(t, d.name)
		if f.mode != d.mode || f.uid != 990 || f.gid != 990 {
			t.Fatalf("%s 未修正: %04o %d:%d", d.name, f.mode, f.uid, f.gid)
		}
	}
	// 配置内容相同但属主被改坏：以正确属主原子重写（不对可能被换成链接的路径 chown）
	e.fs.files["/etc/pimon-agent/agent.json"].uid = 0
	e.fs.files["/etc/pimon-agent/agent.json"].mode = 0o644
	e.reset()
	e.mustInstall(t, dialOut())
	assertLines(t, "修正操作", e.fs.ops, []string{"write /etc/pimon-agent/agent.json 0600 990:990"})
}

func TestSystem_已有用户时跳过创建(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.users.users["pimon"] = sysinstall.User{UID: 900, GID: 900}
	e.mustInstall(t, dialOut())
	if hasCmd(e.runner.log, cmdUseradd) {
		t.Fatal("用户已存在时不应 useradd")
	}
	assertContains(t, e.out.String(), "[已存在] 系统用户 pimon")
}

// ---- 端口检查 ----

func TestPorts_Linux被其他进程占用_指出进程(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.ports.ls = []sysinstall.Listener{{PID: 4242, Exe: "/usr/sbin/nginx"}}
	err := e.install(t, dialOut())
	if err == nil {
		t.Fatal("端口被占用时应失败")
	}
	assertContains(t, e.out.String(), "端口 31418 已被占用", "pid 4242（/usr/sbin/nginx）")
	if hasCmd(e.runner.log, "/usr/bin/systemctl enable") {
		t.Fatal("失败时不应启用服务")
	}
}

func TestPorts_Linux被本服务占用_视为已运行(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialOut())
	e.reset()
	e.ports.ls = []sysinstall.Listener{{PID: 77, Exe: "/usr/local/bin/pimon-agent"}}
	e.mustInstall(t, dialOut())
	assertContains(t, e.out.String(), "[已存在] 端口 31418（pimon-agent.service 已在运行）")
}

func TestPorts_Linux本服务进程的二进制已被替换_重启(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialOut())
	e.reset()
	e.ports.ls = []sysinstall.Listener{{PID: 77, Exe: "/usr/local/bin/pimon-agent (deleted)"}}
	e.mustInstall(t, dialOut())
	if !hasCmd(e.runner.log, "/usr/bin/systemctl restart") {
		t.Fatalf("应重启，命令 %v", e.runner.log)
	}
}

func TestPorts_macOS只能说被占用(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.ports.ls = []sysinstall.Listener{{}}
	if err := e.install(t, dialOut()); err == nil {
		t.Fatal("应失败")
	}
	out := e.out.String()
	assertContains(t, out, "端口 31418 已被占用", "macOS 无法查出占用进程")
	assertNotContains(t, out, "pid")
}

func TestPorts_macOS服务已加载时被占用视为本服务(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.mustInstall(t, dialOut())
	e.reset()
	e.ports.ls = []sysinstall.Listener{{}}
	e.mustInstall(t, dialOut())
	assertContains(t, e.out.String(), "[已存在] 端口 31418")
}

func TestPorts_无权限单独提示不说被占用(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.ports.err = fmt.Errorf("试绑定端口 443: %w", fs.ErrPermission)
	if err := e.install(t, Options{Listen: ":443"}); err == nil {
		t.Fatal("应失败")
	}
	out := e.out.String()
	assertContains(t, out, "没有权限", "1024")
	assertNotContains(t, out, "已被占用")
}

// ---- Docker 组 ----

func dockerHost(e *testEnv) {
	e.fs.dir("/var/run", 0o755, 0, 0)
	e.fs.put("/var/run/docker.sock", "", 0o660, 0, 999)
	e.fs.put("/etc/group", "root:x:0:\ndocker:x:999:bob\n", 0o644, 0, 0)
}

func TestDocker_非交互且无参数_跳过并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	dockerHost(e)
	e.mustInstall(t, dialOut())
	if hasCmd(e.runner.log, cmdUsermod) {
		t.Fatal("不应加组")
	}
	assertContains(t, e.out.String(), "[警告] docker 组", "--docker-group")
}

func TestDocker_显式参数_加组(t *testing.T) {
	e := newEnv(t, "linux", 0)
	dockerHost(e)
	o := dialOut()
	o.DockerGroup = true
	e.mustInstall(t, o)
	if !hasCmd(e.runner.log, "/usr/sbin/usermod -aG docker pimon") {
		t.Fatalf("应加组，命令 %v", e.runner.log)
	}
	assertContains(t, e.out.String(), "近似授予 root 权限")
	// 重跑：已在组内
	e.reset()
	e.mustInstall(t, o)
	if hasCmd(e.runner.log, cmdUsermod) {
		t.Fatal("已在组内时不应再加")
	}
	assertContains(t, e.out.String(), "[已存在] pimon 已在 docker 组")
}

func TestDocker_交互询问(t *testing.T) {
	for _, tc := range []struct {
		answer string
		add    bool
	}{{"y\n", true}, {"yes\n", true}, {"n\n", false}, {"\n", false}} {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			dockerHost(e)
			e.tty, e.in = true, tc.answer
			e.mustInstall(t, dialOut())
			if got := hasCmd(e.runner.log, cmdUsermod); got != tc.add {
				t.Fatalf("加组=%v，期望 %v", got, tc.add)
			}
			assertContains(t, e.out.String(), "近似授予 root 权限", "[y/N]")
		})
	}
}

func TestDocker_无Docker时不询问(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.tty, e.in = true, "y\n"
	e.mustInstall(t, dialOut())
	assertNotContains(t, e.out.String(), "docker")
}

// ---- ping sysctl ----

func TestSysctl_与hub取值统一且只扩大不缩小(t *testing.T) {
	// 取值与 hub install 一致（0 2147483647）：两个 sysctl 文件开机按字母序加载时不会互相缩小
	for _, cur := range []string{"1\t0", "100\t200", "1000\t2000"} {
		e := newEnv(t, "linux", 0)
		e.fs.files[pingRangeProc].data = cur + "\n"
		e.mustInstall(t, dialOut())
		if got := e.file(t, sysctlPath).data; got != "net.ipv4.ping_group_range = 0 2147483647\n" {
			t.Fatalf("当前 %s：写入 %q", cur, got)
		}
	}
	for _, cur := range []string{"0\t2147483647", "900\t1000"} { // 已包含 pimon 组（gid 990）
		e := newEnv(t, "linux", 0)
		e.fs.files[pingRangeProc].data = cur + "\n"
		e.mustInstall(t, dialOut())
		if _, ok := e.fs.files[sysctlPath]; ok || hasCmd(e.runner.log, cmdSysctl) {
			t.Fatalf("当前 %s 已包含时不应写入", cur)
		}
	}
}

// ---- 拨入配对失败 ----

func TestPair_拨号失败_不启用服务也不写配置(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.pairErr = &agentlink.AuthFailError{Reason: "bad_code"}
	err := e.install(t, dialIn())
	if err == nil {
		t.Fatal("应失败")
	}
	if hasCmd(e.runner.log, "/usr/bin/systemctl enable") || hasCmd(e.runner.log, "/usr/bin/systemctl daemon-reload") {
		t.Fatalf("不应启用服务: %v", e.runner.log)
	}
	if _, ok := e.fs.files["/etc/pimon-agent/agent.json"]; ok {
		t.Fatal("不应写配置")
	}
	if _, ok := e.fs.files["/etc/systemd/system/pimon-agent.service"]; ok {
		t.Fatal("配对失败时不应留下单元文件")
	}
	assertContains(t, e.out.String(), "[失败] 配对", "配对码")
	assertNotContains(t, e.out.String(), "ABCD-EFGH")
}

func TestPair_hub断开_回滚配置不启用(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.pairAfter = &link.HelloRejectedError{Kind: agentlink.CloseKind("bye"), Bye: "revoked"}
	if err := e.install(t, dialIn()); err == nil {
		t.Fatal("应失败")
	}
	if _, ok := e.fs.files["/etc/pimon-agent/agent.json"]; ok {
		t.Fatal("应回滚刚写入的配置")
	}
	assertContains(t, e.out.String(), "若网页上已出现该设备，请先吊销再重试")
	if hasCmd(e.runner.log, "/usr/bin/systemctl enable") {
		t.Fatal("不应启用服务")
	}
}

func TestPair_hub断开_回滚为原配置(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	old := e.file(t, "/etc/pimon-agent/agent.json").data
	e.reset()
	e.pairAfter = &link.HelloRejectedError{Kind: agentlink.CloseKind("bye"), Bye: "revoked"}
	e.pairCred = "new-cred"
	o := dialIn()
	o.RePair = true
	if err := e.install(t, o); err == nil {
		t.Fatal("应失败")
	}
	if got := e.file(t, "/etc/pimon-agent/agent.json").data; got != old {
		t.Fatalf("未回滚为原配置: %s", got)
	}
}

func TestPair_协议不兼容_保留凭据不启用(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.pairAfter = &link.IncompatibleError{Reason: "too_old", RequiredMin: 2, HubVersion: "2.0"}
	if err := e.install(t, dialIn()); err == nil {
		t.Fatal("应失败")
	}
	if c := readConf(t, e, "/etc/pimon-agent/agent.json"); string(c.Credential) != e.pairCred {
		t.Fatal("应保留凭据")
	}
	if hasCmd(e.runner.log, "/usr/bin/systemctl enable") {
		t.Fatal("不应启用服务")
	}
	assertContains(t, e.out.String(), "升级 agent")
}

func TestPair_hello超时_凭据已落盘照常启用并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.pairAfter = link.ErrHelloTimeout
	e.mustInstall(t, dialIn())
	if !hasCmd(e.runner.log, "/usr/bin/systemctl enable --now") {
		t.Fatal("应启用服务")
	}
	assertContains(t, e.out.String(), "[警告] 配对")
}

func TestPair_重新配对参数(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.reset()
	e.pairCred = "new-cred"
	o := dialIn()
	o.RePair = true
	e.mustInstall(t, o)
	if len(e.pairReqs) != 1 {
		t.Fatal("--re-pair 应重新配对")
	}
	if c := readConf(t, e, "/etc/pimon-agent/agent.json"); string(c.Credential) != "new-cred" {
		t.Fatal("凭据未更新")
	}
	assertContains(t, e.out.String(), "[完成] 重启 pimon-agent.service（配置已变化）")
}

func TestPair_换hub需重新配对(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.reset()
	o := dialIn()
	o.Hub = "wss://192.168.1.11:31418"
	e.mustInstall(t, o)
	if len(e.pairReqs) != 1 {
		t.Fatal("hub 地址变化应重新配对")
	}
}

func TestPair_已配对且未给配对码_可重跑(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.reset()
	o := dialIn()
	o.Code = ""
	e.mustInstall(t, o)
}

func TestPair_未配对且缺配对码_用法错误(t *testing.T) {
	e := newEnv(t, "linux", 0)
	o := dialIn()
	o.Code = ""
	err := e.install(t, o)
	var ue UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("应为用法错误: %v", err)
	}
}

// ---- 等待就绪 ----

func TestWaitReady_超时只警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.statusErr = errBoom
	e.mustInstall(t, dialOut())
	if e.statusN < 2 {
		t.Fatalf("应轮询多次，实际 %d", e.statusN)
	}
	assertContains(t, e.out.String(), "[警告] 等待守护进程就绪", "journalctl -u pimon-agent")
}

// ---- Linux 用户模式 ----

func TestUser_拨出全新安装_命令序列与文件(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	o := dialOut()
	o.User = true
	e.mustInstall(t, o)

	assertLines(t, "命令序列", e.runner.log, []string{
		"/usr/bin/systemctl --user is-active pimon-agent.service",
		"/usr/bin/systemctl --user daemon-reload",
		"/usr/bin/systemctl --user enable --now pimon-agent.service",
	})
	assertLines(t, "文件操作", e.fs.ops, []string{
		"mkdir /home/alice/.pimon 0700 1000:1000",
		"mkdir /home/alice/.pimon/bin 0700 1000:1000",
		"write /home/alice/.pimon/bin/pimon-agent 0755 nil",
		"write /home/alice/.pimon/agent.json 0600 nil",
		"mkdir /home/alice/.config/systemd/user 0755 1000:1000",
		"write /home/alice/.config/systemd/user/pimon-agent.service 0644 nil",
	})
	if got := e.file(t, "/home/alice/.config/systemd/user/pimon-agent.service").data; got != wantUserUnit {
		t.Fatalf("用户单元不符:\n%s", got)
	}
	out := e.out.String()
	assertContains(t, out, "loginctl enable-linger alice", "ping_group_range",
		"/home/alice/.pimon/bin/pimon-agent pair --user", "/home/alice/.pimon/bin/pimon-agent status --user")
	assertNotContains(t, out, "近似授予")
	if hasCmd(e.runner.log, "loginctl") || hasCmd(e.runner.log, cmdSysctl) {
		t.Fatal("用户模式不应自动执行 loginctl 或 sysctl")
	}
}

func TestUser_拨入重跑幂等(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	o := dialIn()
	o.User = true
	e.mustInstall(t, o)
	if f := e.file(t, "/home/alice/.pimon/agent.json"); f.mode != 0o600 {
		t.Fatalf("配置权限 %04o", f.mode)
	}
	e.reset()
	e.mustInstall(t, o)
	assertLines(t, "重跑命令", e.runner.log, []string{
		"/usr/bin/systemctl --user is-active pimon-agent.service",
		"/usr/bin/systemctl --user is-enabled pimon-agent.service",
	})
	assertLines(t, "重跑文件操作", e.fs.ops, nil)
}

func TestUser_家目录含空格与百分号时单元正确转义(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	e.home = "/home/a b%c"
	e.fs.dir(e.home, 0o750, 1000, 1000)
	o := dialOut()
	o.User = true
	e.mustInstall(t, o)
	unit := e.file(t, "/home/a b%c/.config/systemd/user/pimon-agent.service").data
	assertContains(t, unit, `ExecStart="/home/a b%%c/.pimon/bin/pimon-agent" run --user`)
	assertContains(t, e.out.String(), `'/home/a b%c/.pimon/bin/pimon-agent' pair --user`)
}

// ---- macOS ----

func TestDarwin_拨出全新安装_命令序列与plist(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.mustInstall(t, dialOut())

	assertLines(t, "命令序列", e.runner.log, []string{
		"/bin/launchctl print gui/501/io.github.lancelrq.pimon-agent",
		"/bin/launchctl bootstrap gui/501 /Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist",
	})
	assertLines(t, "文件操作", e.fs.ops, []string{
		"mkdir /Users/alice/.pimon 0700 501:20",
		"mkdir /Users/alice/.pimon/bin 0700 501:20",
		"write /Users/alice/.pimon/bin/pimon-agent 0755 nil",
		"write /Users/alice/.pimon/agent.json 0600 nil",
		"mkdir /Users/alice/Library/LaunchAgents 0755 501:20",
		"write /Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist 0644 nil",
	})
	if got := e.file(t, "/Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist").data; got != wantPlist {
		t.Fatalf("plist 不符:\n%s", got)
	}
	assertContains(t, e.out.String(), "/Users/alice/.pimon/bin/pimon-agent pair", "/Users/alice/.pimon/bin/pimon-agent status", "~/.pimon/agent.log")
	assertNotContains(t, e.out.String(), "--user", "loginctl")
}

func TestDarwin_重跑与plist变化(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.mustInstall(t, dialIn())
	e.reset()
	e.mustInstall(t, dialIn())
	// 当前可执行文件不是安装位置、内容相同：无需任何动作
	assertLines(t, "重跑命令", e.runner.log, []string{"/bin/launchctl print gui/501/io.github.lancelrq.pimon-agent"})

	// plist 被改动：先 bootout 再 bootstrap
	e.reset()
	e.fs.files["/Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist"].data = "old"
	e.mustInstall(t, dialIn())
	assertLines(t, "plist 变化", e.runner.log, []string{
		"/bin/launchctl print gui/501/io.github.lancelrq.pimon-agent",
		"/bin/launchctl bootout gui/501/io.github.lancelrq.pimon-agent",
		"/bin/launchctl bootstrap gui/501 /Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist",
	})

	// 二进制升级：SIGTERM 后由 KeepAlive 拉起
	e.reset()
	e.selfData = "BINARY-v3"
	e.mustInstall(t, dialIn())
	assertLines(t, "二进制升级", e.runner.log, []string{
		"/bin/launchctl print gui/501/io.github.lancelrq.pimon-agent",
		"/bin/launchctl kill SIGTERM gui/501/io.github.lancelrq.pimon-agent",
	})
}

func TestDarwin_拨入配对失败不加载(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.pairErr = &agentlink.DialError{Addr: "192.168.1.10:31418", Err: errBoom}
	if err := e.install(t, dialIn()); err == nil {
		t.Fatal("应失败")
	}
	if hasCmd(e.runner.log, "/bin/launchctl bootstrap") {
		t.Fatal("不应加载服务")
	}
	if _, ok := e.fs.files["/Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist"]; ok {
		t.Fatal("不应写 plist")
	}
}

// ---- 模式矩阵与参数 ----

func TestModeMatrix(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		euid int
		user bool
		noSD bool
		want string
	}{
		{"macOS以root运行拒绝", "darwin", 0, false, false, "macOS"},
		{"Linux系统模式要求root", "linux", 1000, false, false, "root"},
		{"Linux用户模式要求非root", "linux", 0, true, false, "非 root"},
		{"Linux无systemd", "linux", 0, false, true, "systemd"},
		{"不支持的系统", "windows", 0, false, false, "windows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.goos, tc.euid)
			if tc.noSD {
				delete(e.fs.files, "/run/systemd/system")
			}
			o := dialOut()
			o.User = tc.user
			err := e.install(t, o)
			if err == nil || !strings.Contains(err.Error()+e.out.String(), tc.want) {
				t.Fatalf("应因 %q 失败: %v\n%s", tc.want, err, e.out.String())
			}
			if len(e.runner.log) != 0 || len(e.fs.ops) != 0 {
				t.Fatalf("不应有任何改动: %v %v", e.runner.log, e.fs.ops)
			}
		})
	}
}

func TestOptions_用法错误(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    Options
	}{
		{"同时拨入拨出", Options{Hub: testHub, Fingerprint: testFP, Code: "X", Listen: ":31418"}},
		{"都没给且无配置", Options{}},
		{"拨入缺指纹", Options{Hub: testHub, Code: "X"}},
		{"指纹格式错", Options{Hub: testHub, Fingerprint: "SHA256:xx", Code: "X"}},
		{"地址不是wss", Options{Hub: "http://1.2.3.4:31418", Fingerprint: testFP, Code: "X"}},
		{"拨出给名称", Options{Listen: ":31418", Name: "vps"}},
		{"监听地址无端口", Options{Listen: "0.0.0.0"}},
		{"监听端口越界", Options{Listen: ":70000"}},
		{"拨出给re-pair", Options{Listen: ":31418", RePair: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			err := e.install(t, tc.o)
			var ue UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("应为用法错误: %v", err)
			}
			if len(e.runner.log) != 0 || len(e.fs.ops) != 0 {
				t.Fatal("用法错误不应有改动")
			}
		})
	}
}

func TestOptions_名称写进配对帧(t *testing.T) {
	e := newEnv(t, "linux", 0)
	o := dialIn()
	o.Name = "nas"
	e.mustInstall(t, o)
	if e.pairReqs[0].Name != "nas" {
		t.Fatalf("名称 %q", e.pairReqs[0].Name)
	}
}

// ---- 配置路径上的符号链接（pimon 被攻破后诱导 root 读写其他文件）----

const shadow = "root:$6$secret:19000:0:99999:7:::\n"

func shadowHost(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t, "linux", 0)
	e.users.users["pimon"] = sysinstall.User{UID: 990, GID: 990}
	e.fs.put("/etc/shadow", shadow, 0o640, 0, 42)
	e.fs.dir("/etc/pimon-agent", 0o700, 990, 990)
	return e
}

func assertShadowIntact(t *testing.T, e *testEnv) {
	t.Helper()
	f := e.file(t, "/etc/shadow")
	if f.data != shadow || f.uid != 0 || f.gid != 42 || f.mode != 0o640 {
		t.Fatalf("/etc/shadow 被改动: %+v", f)
	}
	for _, r := range e.fs.reads {
		if r == "/etc/shadow" {
			t.Fatal("不应读取 /etc/shadow")
		}
	}
}

func TestSymlink_已有配置是符号链接_拒绝读取(t *testing.T) {
	e := shadowHost(t)
	e.fs.symlink("/etc/pimon-agent/agent.json", "/etc/shadow")
	err := e.install(t, dialOut())
	if err == nil {
		t.Fatal("应失败")
	}
	assertContains(t, e.out.String(), "不是普通文件")
	assertShadowIntact(t, e)
	if len(e.runner.log) != 0 || len(e.fs.ops) != 0 {
		t.Fatalf("不应有改动: %v %v", e.runner.log, e.fs.ops)
	}
}

func TestSymlink_配对前配置被换成符号链接_拒绝写入且不启用(t *testing.T) {
	e := shadowHost(t)
	e.beforeSave = func() { e.fs.symlink("/etc/pimon-agent/agent.json", "/etc/shadow") }
	if err := e.install(t, dialIn()); err == nil {
		t.Fatal("应失败")
	}
	assertContains(t, e.out.String(), "不是普通文件")
	assertShadowIntact(t, e)
	if hasCmd(e.runner.log, "/usr/bin/systemctl enable") {
		t.Fatal("不应启用服务")
	}
}

func TestSymlink_回滚时配置被换成符号链接_拒绝回滚(t *testing.T) {
	e := shadowHost(t)
	e.afterSave = func() { e.fs.symlink("/etc/pimon-agent/agent.json", "/etc/shadow") }
	e.pairAfter = &link.HelloRejectedError{Kind: agentlink.CloseKind("bye"), Bye: "revoked"}
	if err := e.install(t, dialIn()); err == nil {
		t.Fatal("应失败")
	}
	assertContains(t, e.out.String(), "撤销刚保存的凭据也失败了", "不是普通文件")
	assertShadowIntact(t, e)
	if f := e.file(t, "/etc/pimon-agent/agent.json"); f.link != "/etc/shadow" {
		t.Fatal("不应动这个符号链接")
	}
}

// noServiceCmds 是 --no-service 时绝不应出现的系统命令。
var noServiceCmds = []string{cmdSystemctl, cmdLaunchctl, cmdUseradd, cmdUsermod, cmdSysctl, "visudo", "loginctl"}

func assertNoSystemCmds(t *testing.T, e *testEnv) {
	t.Helper()
	if len(e.runner.log) != 0 {
		t.Fatalf("--no-service 不应执行任何系统命令: %v", e.runner.log)
	}
	for _, c := range noServiceCmds {
		if hasCmd(e.runner.log, c) {
			t.Fatalf("不应执行 %s", c)
		}
	}
}

func TestNoService_Linux用户模式拨入_只准备目录装二进制配对写配置(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	o := dialIn()
	o.User, o.NoService = true, true
	e.mustInstall(t, o)

	assertNoSystemCmds(t, e)
	assertLines(t, "文件操作", e.fs.ops, []string{
		"mkdir /home/alice/.pimon 0700 1000:1000",
		"mkdir /home/alice/.pimon/bin 0700 1000:1000",
		"write /home/alice/.pimon/bin/pimon-agent 0755 nil",
		"write /home/alice/.pimon/agent.json 0600 nil",
	})
	if len(e.pairReqs) != 1 {
		t.Fatalf("应配对一次: %d", len(e.pairReqs))
	}
	if e.statusN != 0 {
		t.Fatal("不应等待守护进程就绪")
	}
	if _, ok := e.fs.files["/home/alice/.config/systemd/user/pimon-agent.service"]; ok {
		t.Fatal("不应写服务定义")
	}
	out := e.out.String()
	assertContains(t, out, "安装完成", "不安装服务", "/home/alice/.pimon/bin/pimon-agent run --user")
	assertNotContains(t, out, "loginctl", "近似授予")
}

func TestNoService_macOS拨出_写配置且不碰launchctl(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	o := dialOut()
	o.NoService = true
	e.mustInstall(t, o)

	assertNoSystemCmds(t, e)
	if _, ok := e.fs.files["/Users/alice/Library/LaunchAgents/com.pimon.agent.plist"]; ok {
		t.Fatal("不应写 plist")
	}
	e.file(t, "/Users/alice/.pimon/agent.json")
	assertContains(t, e.out.String(), "/Users/alice/.pimon/bin/pimon-agent run")
	assertNotContains(t, e.out.String(), "稍候几秒等服务启动")
}

func TestNoService_重跑幂等(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	o := dialIn()
	o.User, o.NoService = true, true
	e.mustInstall(t, o)
	e.reset()
	e.mustInstall(t, o)
	assertNoSystemCmds(t, e)
	if len(e.fs.ops) != 0 || len(e.pairReqs) != 0 {
		t.Fatalf("重跑不应有改动: %v 配对 %d", e.fs.ops, len(e.pairReqs))
	}
}

func TestNoService_配对失败_不留配置(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	e.pairErr = errors.New("拒绝")
	o := dialIn()
	o.User, o.NoService = true, true
	if err := e.install(t, o); err == nil {
		t.Fatal("应失败")
	}
	assertNoSystemCmds(t, e)
	if _, ok := e.fs.files["/home/alice/.pimon/agent.json"]; ok {
		t.Fatal("不应留下配置")
	}
}

func TestNoService_用法错误(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		euid int
		o    Options
		want string
	}{
		{"Linux缺--user", "linux", 0, Options{NoService: true, Listen: ":31418"}, "--user"},
		{"Linux非root缺--user", "linux", 1000, Options{NoService: true, Listen: ":31418"}, "--user"},
		{"与--sudoers同用", "linux", 1000, Options{NoService: true, User: true, Sudoers: true, Listen: ":31418"}, "--sudoers"},
		{"与--docker-group同用", "linux", 1000, Options{NoService: true, User: true, DockerGroup: true, Listen: ":31418"}, "--docker-group"},
		{"macOS与--sudoers同用", "darwin", 501, Options{NoService: true, Sudoers: true, Listen: ":31418"}, "--sudoers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.goos, tc.euid)
			err := e.install(t, tc.o)
			var ue UsageError
			if !errors.As(err, &ue) || !strings.Contains(ue.Error(), tc.want) {
				t.Fatalf("应为含 %q 的用法错误: %v", tc.want, err)
			}
			if len(e.runner.log) != 0 || len(e.fs.ops) != 0 {
				t.Fatal("用法错误不应有改动")
			}
		})
	}
}
