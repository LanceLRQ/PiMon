package install

import (
	"context"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
)

func (e *testEnv) uninstall(t *testing.T, o UninstallOptions) {
	t.Helper()
	if err := Uninstall(context.Background(), e.deps(), o); err != nil {
		t.Fatalf("uninstall 失败: %v\n%s", err, e.out.String())
	}
}

func TestUninstall_系统模式(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialIn())
	e.fs.dir("/etc/sudoers.d", 0o750, 0, 0)
	e.fs.put("/etc/sudoers.d/pimon-agent", "pimon ALL=...", 0o440, 0, 0)
	e.reset()
	e.uninstall(t, UninstallOptions{})

	assertLines(t, "命令序列", e.runner.log, []string{
		"/usr/bin/systemctl disable --now pimon-agent.service",
		"/usr/bin/systemctl daemon-reload",
	})
	assertLines(t, "文件操作", e.fs.ops, []string{
		"rm /etc/systemd/system/pimon-agent.service",
		"rm /etc/sudoers.d/pimon-agent",
		"rm /etc/sysctl.d/99-pimon-agent.conf",
		"rm /usr/local/bin/pimon-agent",
	})
	if _, ok := e.fs.files["/etc/pimon-agent/agent.json"]; !ok {
		t.Fatal("不带 --purge 时应保留配置")
	}
	assertContains(t, e.out.String(), "userdel pimon", "--purge")
}

func TestUninstall_系统模式Purge(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.mustInstall(t, dialOut())
	e.fs.put("/var/lib/pimon-agent/agent.db", "db", 0o600, 990, 990)
	e.reset()
	e.uninstall(t, UninstallOptions{Purge: true})
	for _, p := range []string{"/etc/pimon-agent", "/etc/pimon-agent/agent.json", "/var/lib/pimon-agent", "/var/lib/pimon-agent/agent.db"} {
		if _, ok := e.fs.files[p]; ok {
			t.Fatalf("%s 应被删除", p)
		}
	}
	assertContains(t, e.out.String(), "userdel pimon")
	if _, ok := e.users.users["pimon"]; !ok {
		t.Fatal("不应删除 pimon 用户")
	}
}

func TestUninstall_未安装时跳过(t *testing.T) {
	e := newEnv(t, "linux", 0)
	e.uninstall(t, UninstallOptions{})
	assertLines(t, "命令序列", e.runner.log, nil)
	assertLines(t, "文件操作", e.fs.ops, nil)
	assertContains(t, e.out.String(), "[跳过] systemd 单元")
}

func TestUninstall_用户模式Purge(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	o := dialOut()
	o.User = true
	e.mustInstall(t, o)
	e.reset()
	e.uninstall(t, UninstallOptions{User: true, Purge: true})
	assertLines(t, "命令序列", e.runner.log, []string{
		"/usr/bin/systemctl --user disable --now pimon-agent.service",
		"/usr/bin/systemctl --user daemon-reload",
	})
	assertLines(t, "文件操作", e.fs.ops, []string{
		"rm /home/alice/.config/systemd/user/pimon-agent.service",
		"rm /home/alice/.pimon/bin/pimon-agent",
		"rm -r /home/alice/.pimon",
	})
	assertNotContains(t, e.out.String(), "userdel")
}

func TestUninstall_macOS(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.mustInstall(t, dialOut())
	e.reset()
	e.uninstall(t, UninstallOptions{})
	assertLines(t, "命令序列", e.runner.log, []string{
		"/bin/launchctl print gui/501/io.github.lancelrq.pimon-agent",
		"/bin/launchctl bootout gui/501/io.github.lancelrq.pimon-agent",
	})
	assertLines(t, "文件操作", e.fs.ops, []string{
		"rm /Users/alice/Library/LaunchAgents/io.github.lancelrq.pimon-agent.plist",
		"rm /Users/alice/.pimon/bin/pimon-agent",
	})
	if _, ok := e.fs.files["/Users/alice/.pimon/agent.json"]; !ok {
		t.Fatal("应保留配置")
	}
}

func TestUninstall_模式矩阵(t *testing.T) {
	e := newEnv(t, "darwin", 0)
	if err := Uninstall(context.Background(), e.deps(), UninstallOptions{}); err == nil {
		t.Fatal("macOS 以 root 运行应拒绝")
	}
	e = newEnv(t, "linux", 1000)
	if err := Uninstall(context.Background(), e.deps(), UninstallOptions{}); err == nil {
		t.Fatal("系统模式非 root 应拒绝")
	}
}

func TestRestart_三种模式(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		euid int
		user bool
		want string
	}{
		{"系统", "linux", 0, false, "/usr/bin/systemctl restart pimon-agent.service"},
		{"Linux用户", "linux", 1000, true, "/usr/bin/systemctl --user restart pimon-agent.service"},
		{"macOS", "darwin", 501, false, "/bin/launchctl kickstart -k gui/501/io.github.lancelrq.pimon-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.goos, tc.euid)
			p, err := config.Resolve(config.Env{GOOS: tc.goos, EUID: tc.euid, Home: e.home, User: tc.user})
			if err != nil {
				t.Fatal(err)
			}
			if err := Restart(context.Background(), e.deps(), p); err != nil {
				t.Fatal(err)
			}
			assertLines(t, "命令", e.runner.log, []string{tc.want})
		})
	}
}
