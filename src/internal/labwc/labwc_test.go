package labwc

import "testing"

// 共用夹具：覆盖真机上见过的 autostart 写法。
const (
	rpiSwayidle   = "swayidle -w timeout 600 'wlopm --off *' resume 'wlopm --on *' &"
	fixtureM0     = "/home/lancelrq/m0/kiosk.sh >/dev/null 2>&1 &\n"
	fixtureSystem = "lwrespawn pcmanfm-pi\nlwrespawn wf-panel-pi\nkanshi\nlxsession-xdg-autostart\n"
)

func TestHasSwayidle(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"空内容", "", false},
		{"系统 autostart 无 swayidle", fixtureSystem, false},
		{"M0 行无 swayidle", fixtureM0, false},
		{"raspi-config 写法", rpiSwayidle + "\n", true},
		{"绝对路径", "/usr/bin/swayidle -w &\n", true},
		{"lwrespawn 包装", "lwrespawn swayidle -w\n", true},
		{"夹在其他行之间", fixtureM0 + rpiSwayidle + "\n" + fixtureSystem, true},
		{"注释行不算", "# " + rpiSwayidle + "\n", false},
		{"缩进后的注释行不算", "   #swayidle -w\n", false},
		{"行内尾注释不算", "kanshi & # swayidle later\n", false},
		{"引号内出现不算", "echo 'swayidle is disabled'\n", false},
		{"相似命令不算", "swayidle-helper --x\nmyswayidle\n", false},
		{"分号后的命令", "sleep 1; swayidle -w\n", true},
		{"CRLF 行尾", "kanshi\r\nswayidle -w\r\n", true},
		{"无末尾换行", "swayidle", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasSwayidle(c.content); got != c.want {
				t.Fatalf("HasSwayidle=%v 期望 %v\n%q", got, c.want, c.content)
			}
		})
	}
}

func TestRemoveSwayidle(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		want        string
		wantChanged bool
	}{
		{"没有则不动", fixtureSystem, fixtureSystem, false},
		{"空内容", "", "", false},
		{"删掉启用行保留其他", fixtureM0 + rpiSwayidle + "\n" + fixtureSystem, fixtureM0 + fixtureSystem, true},
		{"注释行保留", "# " + rpiSwayidle + "\n" + rpiSwayidle + "\n", "# " + rpiSwayidle + "\n", true},
		{"多行 swayidle 全删", "swayidle -w &\nkanshi\n/usr/bin/swayidle &\n", "kanshi\n", true},
		{"无末尾换行的最后一行", "kanshi\nswayidle -w", "kanshi\n", true},
		{"只有 swayidle 一行", rpiSwayidle + "\n", "", true},
		{"CRLF 保持", "kanshi\r\nswayidle -w\r\nfoo\r\n", "kanshi\r\nfoo\r\n", true},
		{"空行保留", "a\n\nswayidle &\n\nb\n", "a\n\n\nb\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := RemoveSwayidle(c.in)
			if got != c.want || changed != c.wantChanged {
				t.Fatalf("got (%q,%v) 期望 (%q,%v)", got, changed, c.want, c.wantChanged)
			}
			if HasSwayidle(got) {
				t.Fatal("删除后不应再有 swayidle")
			}
		})
	}
}

func TestEnsureLine(t *testing.T) {
	const line = "/usr/bin/systemd-cat -t pimon-kiosk /usr/local/bin/pimon-hub kiosk &"
	cases := []struct {
		name        string
		in          string
		want        string
		wantChanged bool
	}{
		{"空内容追加", "", line + "\n", true},
		{"末尾有换行追加", fixtureM0, fixtureM0 + line + "\n", true},
		{"末尾无换行先补换行", "kanshi", "kanshi\n" + line + "\n", true},
		{"已存在不动", fixtureM0 + line + "\n", fixtureM0 + line + "\n", false},
		{"已存在但带首尾空白也不动", "  " + line + "  \n", "  " + line + "  \n", false},
		{"已存在 CRLF 不动", line + "\r\n", line + "\r\n", false},
		{"被注释的不算存在", "# " + line + "\n", "# " + line + "\n" + line + "\n", true},
		{"CRLF 文件追加沿用 CRLF", "kanshi\r\n", "kanshi\r\n" + line + "\r\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := EnsureLine(c.in, line)
			if got != c.want || changed != c.wantChanged {
				t.Fatalf("got (%q,%v) 期望 (%q,%v)", got, changed, c.want, c.wantChanged)
			}
			if again, ch := EnsureLine(got, line); ch || again != got {
				t.Fatal("EnsureLine 必须幂等")
			}
		})
	}
}

func TestSetEnv(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		want        string
		wantChanged bool
	}{
		{"空内容追加", "", "XCURSOR_THEME=pimon-hidden\n", true},
		{"整行替换同名键", "XCURSOR_THEME=m0-hidden\nXCURSOR_SIZE=24\n", "XCURSOR_THEME=pimon-hidden\nXCURSOR_SIZE=24\n", true},
		{"保留其他行与注释", "# 注释\nXKB_DEFAULT_LAYOUT=gb\nXCURSOR_THEME=PiXtrix\n", "# 注释\nXKB_DEFAULT_LAYOUT=gb\nXCURSOR_THEME=pimon-hidden\n", true},
		{"值已相同不改", "XCURSOR_THEME=pimon-hidden\nXCURSOR_SIZE=24\n", "XCURSOR_THEME=pimon-hidden\nXCURSOR_SIZE=24\n", false},
		{"键缺失追加", "XCURSOR_SIZE=24\n", "XCURSOR_SIZE=24\nXCURSOR_THEME=pimon-hidden\n", true},
		{"无末尾换行追加", "XCURSOR_SIZE=24", "XCURSOR_SIZE=24\nXCURSOR_THEME=pimon-hidden\n", true},
		{"键名前缀相同不误伤", "XCURSOR_THEME_EXTRA=1\n", "XCURSOR_THEME_EXTRA=1\nXCURSOR_THEME=pimon-hidden\n", true},
		{"等号前后有空白", "XCURSOR_THEME = old\n", "XCURSOR_THEME=pimon-hidden\n", true},
		{"被注释的键不算", "#XCURSOR_THEME=old\n", "#XCURSOR_THEME=old\nXCURSOR_THEME=pimon-hidden\n", true},
		{"CRLF 保持", "XCURSOR_THEME=old\r\nXCURSOR_SIZE=24\r\n", "XCURSOR_THEME=pimon-hidden\r\nXCURSOR_SIZE=24\r\n", true},
		{"同名键多行全部替换", "XCURSOR_THEME=a\nX=1\nXCURSOR_THEME=b\n", "XCURSOR_THEME=pimon-hidden\nX=1\nXCURSOR_THEME=pimon-hidden\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := SetEnv(c.in, "XCURSOR_THEME", "pimon-hidden")
			if got != c.want || changed != c.wantChanged {
				t.Fatalf("got (%q,%v) 期望 (%q,%v)", got, changed, c.want, c.wantChanged)
			}
		})
	}
}
