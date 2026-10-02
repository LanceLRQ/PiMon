package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// autoClock 的 After 立即返回并把时间推进 d，让轮询不真正等待。
type autoClock struct{ now time.Time }

func (c *autoClock) Now() time.Time { return c.now }
func (c *autoClock) After(d time.Duration) <-chan time.Time {
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

type fakeFile struct {
	data     string
	mode     fs.FileMode
	uid, gid int
	dir      bool
}

type writeRec struct {
	name string
	mode fs.FileMode
	own  *Owner
}

type fakeFS struct {
	files  map[string]*fakeFile
	writes []writeRec
	fixes  []string // SetOwnerMode / Mkdir 的记录
}

func (f *fakeFS) ReadFile(name string) ([]byte, error) {
	e, ok := f.files[name]
	if !ok || e.dir {
		return nil, fmt.Errorf("open %s: %w", name, fs.ErrNotExist)
	}
	return []byte(e.data), nil
}

func (f *fakeFS) ReadDir(name string) ([]string, error) {
	e, ok := f.files[name]
	if !ok || !e.dir {
		return nil, fmt.Errorf("readdir %s: %w", name, fs.ErrNotExist)
	}
	var out []string
	for p := range f.files {
		if path.Dir(p) == name {
			out = append(out, path.Base(p))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeFS) Stat(name string) (FileInfo, error) {
	e, ok := f.files[name]
	if !ok {
		return FileInfo{}, fmt.Errorf("stat %s: %w", name, fs.ErrNotExist)
	}
	return FileInfo{IsDir: e.dir, Mode: e.mode, UID: e.uid, GID: e.gid}, nil
}

func (f *fakeFS) WriteFile(name string, data []byte, perm fs.FileMode, own *Owner) error {
	f.writes = append(f.writes, writeRec{name, perm, own})
	f.files[name] = &fakeFile{data: string(data), mode: perm}
	return nil
}

func (f *fakeFS) Mkdir(name string, perm fs.FileMode, own Owner) error {
	f.fixes = append(f.fixes, "mkdir "+name)
	f.files[name] = &fakeFile{dir: true, mode: perm, uid: own.UID, gid: own.GID}
	return nil
}

func (f *fakeFS) SetOwnerMode(name string, perm fs.FileMode, own Owner) error {
	f.fixes = append(f.fixes, "fix "+name)
	e := f.files[name]
	e.mode, e.uid, e.gid = perm, own.UID, own.GID
	return nil
}

type fakeUsers struct {
	users  map[string]User
	groups map[string][]string
}

func (u *fakeUsers) Lookup(name string) (User, error) {
	if x, ok := u.users[name]; ok {
		return x, nil
	}
	return User{}, ErrNotFound
}

func (u *fakeUsers) InGroup(name, group string) (bool, error) {
	return slices.Contains(u.groups[name], group), nil
}

type fakePorts struct {
	ls  []Listener
	err error
}

func (p *fakePorts) Listeners(int) ([]Listener, error) { return p.ls, p.err }

// fakeRunner 记录命令序列，并对会改变系统状态的命令更新其他假对象。
type fakeRunner struct {
	env          *testEnv
	log          []string
	failOn       map[string]error // 以命令前缀（空格拼接的 argv）匹配
	active       bool
	setupOut     string // setup-code 的标准输出
	setupExit    int    // 非 0 时以该退出码失败
	setupCodeRun int
}

func (r *fakeRunner) Run(_ context.Context, argv ...string) (string, error) {
	line := strings.Join(argv, " ")
	r.log = append(r.log, line)
	for prefix, err := range r.failOn {
		if strings.HasPrefix(line, prefix) {
			return "", err
		}
	}
	switch {
	case strings.HasPrefix(line, cmdUseradd):
		r.env.users.users["pimon"] = User{UID: 998, GID: 998}
	case strings.HasPrefix(line, cmdUsermod):
		r.env.users.groups[argv[3]] = append(r.env.users.groups[argv[3]], argv[2])
	case line == cmdSystemctl+" is-active "+serviceName:
		if r.active {
			return "active\n", nil
		}
		return "inactive\n", &ExitError{Argv: argv, Code: 3}
	case line == cmdSystemctl+" enable --now "+serviceName:
		r.active = true
		r.env.fs.files[tokenPath] = &fakeFile{data: "tok", mode: 0o640}
	case strings.Contains(line, "setup-code"):
		r.setupCodeRun++
		if r.setupExit != 0 {
			return "已设置管理员，不需要设置码\n", &ExitError{Argv: argv, Code: r.setupExit}
		}
		return r.setupOut, nil
	}
	return "", nil
}

type testEnv struct {
	deps   Deps
	fs     *fakeFS
	users  *fakeUsers
	run    *fakeRunner
	ports  *fakePorts
	out    *bytes.Buffer
	health *int // 失败次数计数器：前 n 次失败
	opts   Options
}

const setupOutput = "首次设置码: ABCD-2345\n有效期至: 2026-10-03 10:00:00\n"

// newTestEnv 构造一台「全新的树莓派」：有 systemd、有桌面用户 lancelrq 与 lightdm 自动登录配置。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{out: &bytes.Buffer{}}
	e.fs = &fakeFS{files: map[string]*fakeFile{
		"/run/systemd/system": {dir: true},
		"/etc/lightdm":        {dir: true},
		lightdmMainConf:       {data: "[LightDM]\n[Seat:*]\n#autologin-user=nobody\nautologin-user=lancelrq\nautologin-session=rpd-labwc\n"},
		"/tmp/new/pimon-hub":  {data: "BIN-V1", mode: 0o755},
		pingRange:             {data: "0\t2147483647\n"},
	}}
	e.users = &fakeUsers{users: map[string]User{"lancelrq": {UID: 1000, GID: 1000}}, groups: map[string][]string{"lancelrq": {"lancelrq", "sudo"}}}
	e.run = &fakeRunner{env: e, setupOut: setupOutput}
	e.ports = &fakePorts{}
	failures := 0
	e.health = &failures
	e.deps = Deps{
		Out:    e.out,
		Clock:  &autoClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)},
		Runner: e.run,
		FS:     e.fs,
		Users:  e.users,
		Ports:  e.ports,
		Health: func(context.Context, string) error {
			if failures > 0 {
				failures--
				return errors.New("连接被拒绝")
			}
			return nil
		},
		EUID:     0,
		GOOS:     "linux",
		Self:     "/tmp/new/pimon-hub",
		LocalIPs: func() []net.IP { return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("10.22.33.198")} },
	}
	return e
}

func (e *testEnv) install(t *testing.T) error {
	t.Helper()
	return Run(context.Background(), e.deps, e.opts)
}

func (e *testEnv) wrote(name string) *writeRec {
	for i := range e.fs.writes {
		if e.fs.writes[i].name == name {
			return &e.fs.writes[i]
		}
	}
	return nil
}
