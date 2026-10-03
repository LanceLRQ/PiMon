package sudocmd

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

type fakeInfo struct {
	name string
	mode fs.FileMode
	uid  uint32
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

// fakeFS 用路径表模拟文件系统：files 记录每个真实路径的信息，links 记录符号链接到真实路径的映射。
type fakeFS struct {
	files map[string]fakeInfo
	links map[string]string
}

func newFakeFS() *fakeFS {
	f := &fakeFS{files: map[string]fakeInfo{}, links: map[string]string{}}
	for _, d := range []string{"/", "/usr", "/usr/sbin", "/usr/bin", "/usr/local", "/usr/local/sbin", "/usr/local/bin"} {
		f.files[d] = fakeInfo{name: filepath.Base(d), mode: fs.ModeDir | 0o755}
	}
	return f
}

func (f *fakeFS) real(p string) (string, bool) {
	if r, ok := f.links[p]; ok {
		return r, true
	}
	_, ok := f.files[p]
	return p, ok
}

func (f *fakeFS) toFS() FS {
	return FS{
		Stat: func(p string) (fs.FileInfo, error) {
			r, ok := f.real(p)
			if !ok {
				return nil, fs.ErrNotExist
			}
			fi, ok := f.files[r]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return fi, nil
		},
		EvalSymlinks: func(p string) (string, error) {
			r, ok := f.real(p)
			if !ok {
				return "", fs.ErrNotExist
			}
			return r, nil
		},
		Owner: func(fi fs.FileInfo) (uint32, bool) {
			ff, ok := fi.(fakeInfo)
			return ff.uid, ok
		},
	}
}

func TestResolve找到属主为root的命令(t *testing.T) {
	f := newFakeFS()
	f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
	got, err := Resolve(f.toFS(), Smartctl)
	if err != nil || got != "/usr/sbin/smartctl" {
		t.Fatalf("Resolve = %q %v", got, err)
	}
}

func TestResolve返回符号链接的真实路径(t *testing.T) {
	f := newFakeFS()
	f.files["/usr/bin/btrfs"] = fakeInfo{name: "btrfs", mode: 0o755}
	f.links["/usr/sbin/btrfs"] = "/usr/bin/btrfs"
	got, err := Resolve(f.toFS(), Btrfs)
	if err != nil || got != "/usr/bin/btrfs" {
		t.Fatalf("Resolve = %q %v", got, err)
	}
}

func TestResolve不存在(t *testing.T) {
	if _, err := Resolve(newFakeFS().toFS(), Mdadm); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound: %v", err)
	}
}

func TestResolve拒绝不安全的属主与权限(t *testing.T) {
	cases := map[string]func(f *fakeFS){
		"属主非 root": func(f *fakeFS) {
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755, uid: 1000}
		},
		"组可写": func(f *fakeFS) {
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o775}
		},
		"他人可写": func(f *fakeFS) {
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o757}
		},
		"不可执行": func(f *fakeFS) {
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o644}
		},
		"不是普通文件": func(f *fakeFS) {
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: fs.ModeDir | 0o755}
		},
		"所在目录他人可写": func(f *fakeFS) {
			f.files["/usr/sbin"] = fakeInfo{name: "sbin", mode: fs.ModeDir | 0o777}
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
		},
		"上级目录属主非 root": func(f *fakeFS) {
			f.files["/usr"] = fakeInfo{name: "usr", mode: fs.ModeDir | 0o755, uid: 1000}
			f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
		},
		"链接指向用户可写目录": func(f *fakeFS) {
			f.files["/home"] = fakeInfo{name: "home", mode: fs.ModeDir | 0o755}
			f.files["/home/u"] = fakeInfo{name: "u", mode: fs.ModeDir | 0o755, uid: 1000}
			f.files["/home/u/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
			f.links["/usr/sbin/smartctl"] = "/home/u/smartctl"
		},
	}
	for name, setup := range cases {
		f := newFakeFS()
		setup(f)
		if got, err := Resolve(f.toFS(), Smartctl); !errors.Is(err, ErrUnsafe) {
			t.Errorf("%s：应返回 ErrUnsafe，得到 %q %v", name, got, err)
		}
	}
}

func TestResolve不安全的候选之后仍可选用安全的候选(t *testing.T) {
	f := newFakeFS()
	f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o777}
	f.files["/usr/local/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
	got, err := Resolve(f.toFS(), Smartctl)
	if err != nil || got != "/usr/local/sbin/smartctl" {
		t.Fatalf("Resolve = %q %v", got, err)
	}
}

func TestResolve拒绝未知命令与无法取得属主(t *testing.T) {
	f := newFakeFS()
	f.files["/usr/bin/rm"] = fakeInfo{name: "rm", mode: 0o755}
	if _, err := Resolve(f.toFS(), "rm"); err == nil {
		t.Fatal("未知命令应被拒绝")
	}
	f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
	fsys := f.toFS()
	fsys.Owner = func(fs.FileInfo) (uint32, bool) { return 0, false }
	if _, err := Resolve(fsys, Smartctl); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("无法取得属主应视为不安全: %v", err)
	}
}

func TestResolveAll只含找到的命令(t *testing.T) {
	f := newFakeFS()
	f.files["/usr/sbin/smartctl"] = fakeInfo{name: "smartctl", mode: 0o755}
	f.files["/usr/bin/btrfs"] = fakeInfo{name: "btrfs", mode: 0o755}
	paths, errs := ResolveAll(f.toFS())
	if paths[Smartctl] != "/usr/sbin/smartctl" || paths[Btrfs] != "/usr/bin/btrfs" || len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	if !errors.Is(errs[Mdadm], ErrNotFound) || len(errs) != 1 {
		t.Fatalf("errs = %v", errs)
	}
}

func TestSystemFS真实路径可用(t *testing.T) {
	fsys := SystemFS()
	if fsys.Stat == nil || fsys.EvalSymlinks == nil || fsys.Owner == nil {
		t.Fatal("SystemFS 字段不应为空")
	}
	fi, err := fsys.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	if uid, ok := fsys.Owner(fi); !ok || uid != 0 {
		t.Fatalf("/ 的属主应为 root: %d %v", uid, ok)
	}
}
