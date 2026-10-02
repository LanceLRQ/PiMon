package install

import (
	"io/fs"
	"strings"
	"testing"
)

const (
	userAutostart  = "/home/lancelrq/.config/labwc/autostart"
	userEnv        = "/home/lancelrq/.config/labwc/environment"
	greeterAuto    = "/etc/xdg/labwc-greeter/autostart"
	systemAuto     = "/etc/xdg/labwc/autostart"
	kioskLine      = "/usr/bin/systemd-cat -t pimon-kiosk /usr/local/bin/pimon-hub kiosk --hub http://127.0.0.1:31415 &"
	themeDir       = "/home/lancelrq/.icons/pimon-hidden"
	m0Line         = "/home/lancelrq/m0/kiosk.sh >/dev/null 2>&1 &"
	swayLine       = "/usr/bin/swayidle -w timeout 300 'wlopm --off *' &"
	backupTimeSufx = "20261002T120000Z"
)

func kioskEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	e.opts.Kiosk = true
	return e
}

func (e *testEnv) seed(name, data string, mode uint32, uid, gid int) {
	e.fs.files[name] = &fakeFile{data: data, mode: fs.FileMode(mode), uid: uid, gid: gid}
}

func (e *testEnv) backups(prefix string) []string {
	var out []string
	for n := range e.fs.files {
		if strings.HasPrefix(n, prefix+".pimon-bak-") {
			out = append(out, n)
		}
	}
	return out
}

func TestKioskFreshInstall(t *testing.T) {
	e := kioskEnv(t)
	if err := e.install(t); err != nil {
		t.Fatalf("install: %v\n%s", err, e.out)
	}
	a := e.fs.files[userAutostart]
	if a == nil || a.data != kioskLine+"\n" {
		t.Fatalf("autostart = %#v", a)
	}
	if a.mode != 0o644 || a.uid != 1000 || a.gid != 1000 {
		t.Errorf("autostart 权限属主 = %o %d:%d", a.mode, a.uid, a.gid)
	}
	for _, d := range []string{"/home/lancelrq/.config", "/home/lancelrq/.config/labwc", "/home/lancelrq/.icons", themeDir, themeDir + "/cursors"} {
		f := e.fs.files[d]
		if f == nil || !f.dir || f.mode != 0o755 || f.uid != 1000 || f.gid != 1000 {
			t.Errorf("目录 %s = %#v", d, f)
		}
	}
	env := e.fs.files[userEnv]
	if env == nil || env.data != "XCURSOR_THEME=pimon-hidden\n" || env.mode != 0o644 || env.uid != 1000 {
		t.Errorf("environment = %#v", env)
	}
	idx := e.fs.files[themeDir+"/index.theme"]
	if idx == nil || idx.data != "[Icon Theme]\nName=pimon-hidden\n" || idx.uid != 1000 || idx.mode != 0o644 {
		t.Errorf("index.theme = %#v", idx)
	}
	want := string(xcursorBytes())
	for _, n := range cursorNames {
		f := e.fs.files[themeDir+"/cursors/"+n]
		if f == nil || f.data != want || len(f.data) != 15776 || f.uid != 1000 || f.gid != 1000 || f.mode != 0o644 {
			t.Fatalf("光标 %s 不符合预期", n)
		}
	}
	if !strings.Contains(e.out.String(), "下次登录") || !strings.Contains(e.out.String(), "systemctl restart lightdm") {
		t.Errorf("缺少生效提示:\n%s", e.out)
	}
}

func TestKioskIdempotent(t *testing.T) {
	e := kioskEnv(t)
	e.seed(userAutostart, m0Line+"\n"+swayLine+"\n", 0o664, 1000, 1000)
	e.seed(userEnv, "XCURSOR_THEME=m0-hidden\nXCURSOR_SIZE=24\n", 0o644, 1000, 1000)
	e.fs.files[greeterAuto] = &fakeFile{data: "/usr/sbin/pi-greeter &\n" + swayLine + "\n", mode: 0o644}
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]string{}
	for n, f := range e.fs.files {
		snapshot[n] = f.data
	}
	nWrites := len(e.fs.writes)
	e.out.Reset()
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	for _, w := range e.fs.writes[nWrites:] {
		if strings.HasPrefix(w.name, "/home/") || strings.HasPrefix(w.name, "/etc/xdg") {
			t.Errorf("第二次运行不应再写 %s", w.name)
		}
	}
	for n, f := range e.fs.files {
		if old, ok := snapshot[n]; !ok {
			t.Errorf("第二次运行新增了文件 %s", n)
		} else if old != f.data {
			t.Errorf("第二次运行改了 %s", n)
		}
	}
	if strings.Contains(e.out.String(), "[完成] kiosk") || strings.Contains(e.out.String(), "已删除") {
		t.Errorf("第二次运行不应再有完成项:\n%s", e.out)
	}
}

func TestKioskKeepsOtherLinesAndBacksUp(t *testing.T) {
	e := kioskEnv(t)
	orig := m0Line + "\n" + swayLine + "\n"
	e.seed(userAutostart, orig, 0o664, 1000, 1000)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	a := e.fs.files[userAutostart]
	if a.data != m0Line+"\n"+kioskLine+"\n" {
		t.Errorf("autostart = %q", a.data)
	}
	if a.mode != 0o664 || a.uid != 1000 || a.gid != 1000 {
		t.Errorf("应保留原权限属主: %o %d:%d", a.mode, a.uid, a.gid)
	}
	bs := e.backups(userAutostart)
	if len(bs) != 1 || !strings.HasSuffix(bs[0], ".pimon-bak-"+backupTimeSufx) {
		t.Fatalf("备份 = %v", bs)
	}
	if b := e.fs.files[bs[0]]; b.data != orig || b.mode != 0o664 || b.uid != 1000 {
		t.Errorf("备份内容或权限不对: %#v", b)
	}
	out := e.out.String()
	if !strings.Contains(out, swayLine) {
		t.Errorf("应打印被删原行:\n%s", out)
	}
	if !strings.Contains(out, m0Line) {
		t.Errorf("应提醒人工清理 M0 启动行:\n%s", out)
	}
}

func TestKioskDoesNotDuplicateBackup(t *testing.T) {
	e := kioskEnv(t)
	orig := m0Line + "\n" + swayLine + "\n"
	e.seed(userAutostart, orig, 0o664, 1000, 1000)
	for _, d := range []string{"/home/lancelrq/.config", "/home/lancelrq/.config/labwc"} {
		e.fs.files[d] = &fakeFile{dir: true, mode: 0o755, uid: 1000, gid: 1000}
	}
	e.seed(userAutostart+".pimon-bak-20200101T000000Z", orig, 0o664, 1000, 1000)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if bs := e.backups(userAutostart); len(bs) != 1 {
		t.Errorf("同内容已有备份时不应再建: %v", bs)
	}
}

func TestKioskGreeterSwayidleRemoved(t *testing.T) {
	e := kioskEnv(t)
	orig := "/usr/sbin/pi-greeter &\n" + swayLine + "\n/usr/bin/sbtest &\n"
	e.fs.files[greeterAuto] = &fakeFile{data: orig, mode: 0o644, uid: 0, gid: 0}
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	g := e.fs.files[greeterAuto]
	if g.data != "/usr/sbin/pi-greeter &\n/usr/bin/sbtest &\n" || g.mode != 0o644 || g.uid != 0 {
		t.Errorf("greeter autostart = %#v", g)
	}
	bs := e.backups(greeterAuto)
	if len(bs) != 1 || e.fs.files[bs[0]].data != orig || e.fs.files[bs[0]].uid != 0 {
		t.Errorf("greeter 备份 = %v", bs)
	}
}

func TestKioskSystemAutostartOnlyWarns(t *testing.T) {
	e := kioskEnv(t)
	orig := "lwrespawn wf-panel-pi\n" + swayLine + "\n"
	e.fs.files[systemAuto] = &fakeFile{data: orig, mode: 0o644}
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	if e.fs.files[systemAuto].data != orig {
		t.Error("不得改 /etc/xdg/labwc/autostart")
	}
	if len(e.backups(systemAuto)) != 0 {
		t.Error("不得为系统 autostart 建备份")
	}
	if !strings.Contains(e.out.String(), "[警告]") || !strings.Contains(e.out.String(), systemAuto) {
		t.Errorf("应警告:\n%s", e.out)
	}
}

func TestKioskEnvironmentReplaced(t *testing.T) {
	e := kioskEnv(t)
	orig := "XCURSOR_THEME=m0-hidden\nXCURSOR_SIZE=24\n"
	e.seed(userEnv, orig, 0o644, 1000, 1000)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	env := e.fs.files[userEnv]
	if env.data != "XCURSOR_THEME=pimon-hidden\nXCURSOR_SIZE=24\n" || env.mode != 0o644 || env.uid != 1000 {
		t.Errorf("environment = %#v", env)
	}
	bs := e.backups(userEnv)
	if len(bs) != 1 || e.fs.files[bs[0]].data != orig {
		t.Errorf("environment 备份 = %v", bs)
	}
}

func TestKioskRequiresDesktopUser(t *testing.T) {
	e := kioskEnv(t)
	e.fs.files[lightdmMainConf] = &fakeFile{data: "[Seat:*]\n"}
	err := e.install(t)
	if err == nil || !strings.Contains(err.Error(), "桌面用户") {
		t.Fatalf("err = %v", err)
	}
	for _, w := range e.fs.writes {
		if strings.HasPrefix(w.name, "/home/") {
			t.Errorf("无桌面用户时不应写 %s", w.name)
		}
	}
}

func TestKioskNotRequestedLeavesHomeAlone(t *testing.T) {
	e := newTestEnv(t)
	if err := e.install(t); err != nil {
		t.Fatal(err)
	}
	for _, w := range e.fs.writes {
		if strings.HasPrefix(w.name, "/home/") {
			t.Errorf("未带 --kiosk 不应写 %s", w.name)
		}
	}
}
