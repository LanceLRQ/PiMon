package install

import "testing"

func TestSeatAutologinUser(t *testing.T) {
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"正常", "[Seat:*]\nautologin-user=lancelrq\n", "lancelrq", true},
		{"注释行不算", "[Seat:*]\n#autologin-user=x\n; autologin-user=y\n", "", false},
		{"其他段不算", "[LightDM]\nautologin-user=x\n[Seat:*]\n", "", false},
		{"空格与后值覆盖", "[Seat:*]\n autologin-user = a \nautologin-user=b\n", "b", true},
		{"空值表示取消", "[Seat:*]\nautologin-user=\n", "", true},
		{"其他 Seat 段不算", "[Seat:seat1]\nautologin-user=x\n", "", false},
	}
	for _, c := range cases {
		got, ok := seatAutologinUser(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%q,%v) want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestDetectAutologinUserPrecedence(t *testing.T) {
	f := &fakeFS{files: map[string]*fakeFile{
		"/usr/share/lightdm/lightdm.conf.d":           {dir: true},
		"/usr/share/lightdm/lightdm.conf.d/10-a.conf": {data: "[Seat:*]\nautologin-user=share\n"},
		"/etc/lightdm/lightdm.conf.d":                 {dir: true},
		"/etc/lightdm/lightdm.conf.d/50-b.conf":       {data: "[Seat:*]\nautologin-user=etcd\n"},
		"/etc/lightdm/lightdm.conf.d/readme.txt":      {data: "[Seat:*]\nautologin-user=ignored\n"},
	}}
	got, err := detectAutologinUser(f)
	if err != nil || got != "etcd" {
		t.Fatalf("got %q %v", got, err)
	}
	f.files[lightdmMainConf] = &fakeFile{data: "[Seat:*]\nautologin-user=main\n"}
	if got, _ := detectAutologinUser(f); got != "main" {
		t.Fatalf("主配置文件应最后覆盖: %q", got)
	}
}

func TestDetectAutologinUserNoConfig(t *testing.T) {
	if got, err := detectAutologinUser(&fakeFS{files: map[string]*fakeFile{}}); err != nil || got != "" {
		t.Fatalf("got %q %v", got, err)
	}
}
