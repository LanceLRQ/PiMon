package install

import (
	"slices"
	"strings"
	"testing"
)

const (
	wdUnitFile = "/etc/systemd/system/pimon-session-watchdog.service"
	wdUnitWant = `[Unit]
Description=PiMon session watchdog
After=lightdm.service

[Service]
ExecStart=/usr/local/bin/pimon-hub session-watchdog --user lancelrq
User=root
Restart=always
RestartSec=5s

[Install]
WantedBy=graphical.target
`
)

func (e *testEnv) cmdCount(prefix string) int {
	n := 0
	for _, l := range e.run.log {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestWatchdogFreshInstall(t *testing.T) {
	e := kioskEnv(t)
	if err := e.install(t); err != nil {
		t.Fatalf("install: %v\n%s", err, e.out)
	}
	f := e.fs.files[wdUnitFile]
	if f == nil || f.data != wdUnitWant || f.mode != 0o644 || f.uid != 0 {
		t.Fatalf("unit = %#v", f)
	}
	en := slices.Index(e.run.log, cmdSystemctl+" enable --now "+watchdogService)
	if en < 0 {
		t.Fatalf("没有 enable --now:\n%v", e.run.log)
	}
	reload := -1
	for i := en - 1; i >= 0; i-- {
		if e.run.log[i] == cmdSystemctl+" daemon-reload" {
			reload = i
			break
		}
	}
	if reload < 0 {
		t.Error("enable 前应有 daemon-reload")
	}
	if e.cmdCount(cmdSystemctl+" restart "+watchdogService) != 0 {
		t.Error("全新安装不应 restart 看门狗")
	}
}

func TestWatchdogIdempotent(t *testing.T) {
	e := kioskEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	n := len(e.fs.writes)
	e.out.Reset()
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.wroteAfter(n, wdUnitFile) {
		t.Error("内容相同应跳过写入")
	}
	if e.cmdCount(cmdSystemctl+" restart "+watchdogService) != 0 {
		t.Error("无变化不应重启看门狗")
	}
}

func (e *testEnv) wroteAfter(n int, name string) bool {
	for _, w := range e.fs.writes[n:] {
		if w.name == name {
			return true
		}
	}
	return false
}

func TestWatchdogRestartedWhenUnitChanged(t *testing.T) {
	e := kioskEnv(t)
	e.seed(wdUnitFile, "[Unit]\nDescription=old\n", 0o644, 0, 0)
	e.run.wdActive = true
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.fs.files[wdUnitFile].data != wdUnitWant {
		t.Error("unit 应被更新")
	}
	if e.cmdCount(cmdSystemctl+" restart "+watchdogService) != 1 {
		t.Errorf("unit 变化且服务在运行时应 restart 一次:\n%v", e.run.log)
	}
}

func TestWatchdogRestartedWhenBinaryUpgraded(t *testing.T) {
	e := kioskEnv(t)
	e.seed(wdUnitFile, wdUnitWant, 0o644, 0, 0)
	e.seed(binPath, "BIN-OLD", 0o755, 0, 0)
	e.run.wdActive = true
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.cmdCount(cmdSystemctl+" restart "+watchdogService) != 1 {
		t.Errorf("二进制升级且服务在运行时应 restart:\n%v", e.run.log)
	}
}

func TestWatchdogUpgradeWhileInactiveDoesNotRestart(t *testing.T) {
	e := kioskEnv(t)
	e.seed(binPath, "BIN-OLD", 0o755, 0, 0)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.cmdCount(cmdSystemctl+" restart "+watchdogService) != 0 {
		t.Error("原本没运行时 enable --now 已用新二进制启动，不应再 restart")
	}
}

func TestWatchdogNotInstalledWithoutKiosk(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.wrote(wdUnitFile) != nil || e.cmdCount(cmdSystemctl+" enable --now "+watchdogService) != 0 {
		t.Error("未带 --kiosk 不应安装看门狗")
	}
}

func TestWatchdogRejectsUnsafeUserName(t *testing.T) {
	e := kioskEnv(t)
	e.opts.DesktopUser = "bad user\nExecStartPre=/bin/evil"
	e.users.users[e.opts.DesktopUser] = User{UID: 1001, GID: 1001}
	err := e.install(t)
	if err == nil || !strings.Contains(err.Error(), "桌面用户") {
		t.Fatalf("err = %v", err)
	}
	if e.wrote(wdUnitFile) != nil {
		t.Error("不得写入 unit")
	}
}
