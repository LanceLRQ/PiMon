package lightdm

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"testing"
)

type memFS struct{ files map[string]string } // 以 "/" 结尾的键表示目录

func (m memFS) ReadFile(name string) ([]byte, error) {
	s, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("open %s: %w", name, fs.ErrNotExist)
	}
	return []byte(s), nil
}

func (m memFS) ReadDir(name string) ([]string, error) {
	if _, ok := m.files[name+"/"]; !ok {
		return nil, fmt.Errorf("readdir %s: %w", name, fs.ErrNotExist)
	}
	var out []string
	for p := range m.files {
		if path.Dir(p) == name {
			out = append(out, path.Base(p))
		}
	}
	sort.Strings(out)
	return out, nil
}

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
		got, ok := SeatAutologinUser(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%q,%v) want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestAutologinUserPrecedence(t *testing.T) {
	f := memFS{files: map[string]string{
		"/usr/share/lightdm/lightdm.conf.d/":          "",
		"/usr/share/lightdm/lightdm.conf.d/10-a.conf": "[Seat:*]\nautologin-user=share\n",
		"/etc/lightdm/lightdm.conf.d/":                "",
		"/etc/lightdm/lightdm.conf.d/50-b.conf":       "[Seat:*]\nautologin-user=etcd\n",
		"/etc/lightdm/lightdm.conf.d/readme.txt":      "[Seat:*]\nautologin-user=ignored\n",
	}}
	got, err := AutologinUser(f)
	if err != nil || got != "etcd" {
		t.Fatalf("got %q %v", got, err)
	}
	f.files[MainConf] = "[Seat:*]\nautologin-user=main\n"
	if got, _ := AutologinUser(f); got != "main" {
		t.Fatalf("主配置文件应最后覆盖: %q", got)
	}
}

func TestAutologinUserNoConfig(t *testing.T) {
	if got, err := AutologinUser(memFS{files: map[string]string{}}); err != nil || got != "" {
		t.Fatalf("got %q %v", got, err)
	}
}
