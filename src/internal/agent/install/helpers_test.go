package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

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
	mtime    time.Time
	link     string // 非空表示符号链接，指向该路径
}

type fakeFS struct {
	clk     *autoClock
	files   map[string]*fakeFile
	ops     []string // 写入、建目录、改属主、删除的记录
	failOn  map[string]error
	uptimeS float64
	reads   []string // ReadFile 读过的路径（跟随符号链接后的实际路径）
	// creator 是 own 为 nil 时新文件的属主（真实系统里为当前进程的 euid/egid）。
	creator sysinstall.Owner
}

func (f *fakeFS) fail(op, name string) error {
	if err, ok := f.failOn[op+" "+name]; ok {
		return err
	}
	return nil
}

func (f *fakeFS) ReadFile(name string) ([]byte, error) {
	if name == "/proc/uptime" {
		return []byte(fmt.Sprintf("%.2f 0.00\n", f.uptimeS)), nil
	}
	e, ok := f.files[name]
	if ok && e.link != "" { // 与真实系统一样跟随符号链接
		name = e.link
		e, ok = f.files[name]
	}
	if !ok || e.dir {
		return nil, fmt.Errorf("open %s: %w", name, fs.ErrNotExist)
	}
	f.reads = append(f.reads, name)
	return []byte(e.data), nil
}

func (f *fakeFS) ReadDir(name string) ([]string, error) {
	var out []string
	for p := range f.files {
		if path.Dir(p) == name {
			out = append(out, path.Base(p))
		}
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeFS) Stat(name string) (sysinstall.FileInfo, error) {
	if e, ok := f.files[name]; ok && e.link != "" {
		name = e.link
	}
	return f.Lstat(name)
}

func (f *fakeFS) Lstat(name string) (sysinstall.FileInfo, error) {
	e, ok := f.files[name]
	if !ok {
		return sysinstall.FileInfo{}, fmt.Errorf("stat %s: %w", name, fs.ErrNotExist)
	}
	return sysinstall.FileInfo{IsDir: e.dir, IsSymlink: e.link != "", IsRegular: !e.dir && e.link == "",
		Mode: e.mode, ModTime: e.mtime, UID: e.uid, GID: e.gid}, nil
}

// symlink 在 name 处放一个指向 target 的符号链接。
func (f *fakeFS) symlink(name, target string) {
	f.files[name] = &fakeFile{link: target, mode: 0o777}
}

func (f *fakeFS) WriteFile(name string, data []byte, perm fs.FileMode, own *sysinstall.Owner) error {
	if err := f.fail("write", name); err != nil {
		return err
	}
	if d, ok := f.files[path.Dir(name)]; !ok || !d.dir {
		return fmt.Errorf("write %s: 父目录不存在: %w", name, fs.ErrNotExist)
	}
	o := "nil"
	nf := &fakeFile{data: string(data), mode: perm, uid: f.creator.UID, gid: f.creator.GID, mtime: f.clk.now}
	if own != nil {
		o = fmt.Sprintf("%d:%d", own.UID, own.GID)
		nf.uid, nf.gid = own.UID, own.GID
	}
	f.ops = append(f.ops, fmt.Sprintf("write %s %04o %s", name, perm, o))
	f.files[name] = nf
	return nil
}

func (f *fakeFS) Mkdir(name string, perm fs.FileMode, own sysinstall.Owner) error {
	f.ops = append(f.ops, fmt.Sprintf("mkdir %s %04o %d:%d", name, perm, own.UID, own.GID))
	for p := path.Dir(name); p != "/" && p != "."; p = path.Dir(p) {
		if _, ok := f.files[p]; !ok {
			f.files[p] = &fakeFile{dir: true, mode: perm}
		}
	}
	f.files[name] = &fakeFile{dir: true, mode: perm, uid: own.UID, gid: own.GID}
	return nil
}

func (f *fakeFS) SetOwnerMode(name string, perm fs.FileMode, own sysinstall.Owner) error {
	f.ops = append(f.ops, fmt.Sprintf("chown %s %04o %d:%d", name, perm, own.UID, own.GID))
	e := f.files[name]
	e.mode, e.uid, e.gid = perm, own.UID, own.GID
	return nil
}

func (f *fakeFS) Remove(name string) error {
	if err := f.fail("rm", name); err != nil {
		return err
	}
	if _, ok := f.files[name]; !ok {
		return fmt.Errorf("remove %s: %w", name, fs.ErrNotExist)
	}
	f.ops = append(f.ops, "rm "+name)
	delete(f.files, name)
	return nil
}

func (f *fakeFS) RemoveAll(name string) error {
	f.ops = append(f.ops, "rm -r "+name)
	for p := range f.files {
		if p == name || strings.HasPrefix(p, name+"/") {
			delete(f.files, p)
		}
	}
	return nil
}

func (f *fakeFS) put(name, data string, mode fs.FileMode, uid, gid int) {
	f.files[name] = &fakeFile{data: data, mode: mode, uid: uid, gid: gid, mtime: f.clk.now}
}

func (f *fakeFS) dir(name string, mode fs.FileMode, uid, gid int) {
	f.files[name] = &fakeFile{dir: true, mode: mode, uid: uid, gid: gid}
}

type fakeUsers struct {
	users  map[string]sysinstall.User
	groups map[string][]string // 用户 -> 附加组
}

func (u *fakeUsers) Lookup(name string) (sysinstall.User, error) {
	if x, ok := u.users[name]; ok {
		return x, nil
	}
	return sysinstall.User{}, sysinstall.ErrNotFound
}

func (u *fakeUsers) Home(name string) (string, error) { return "/home/" + name, nil }

func (u *fakeUsers) InGroup(name, group string) (bool, error) {
	return slices.Contains(u.groups[name], group), nil
}

type fakePorts struct {
	ls  []sysinstall.Listener
	err error
	got []int
}

func (p *fakePorts) Listeners(port int) ([]sysinstall.Listener, error) {
	p.got = append(p.got, port)
	return p.ls, p.err
}

// fakeRunner 记录命令序列，并模拟 systemd/launchd 与用户管理的状态变化。
type fakeRunner struct {
	env     *testEnv
	log     []string
	failOn  map[string]error // 以命令前缀匹配
	active  bool             // systemd 服务运行中
	enabled bool
	loaded  bool  // launchd 已加载
	monoUS  int64 // ActiveEnterTimestampMonotonic（微秒）
}

func (r *fakeRunner) Run(ctx context.Context, argv ...string) (string, error) {
	return r.RunEnv(ctx, nil, argv...)
}

func (r *fakeRunner) RunEnv(_ context.Context, _ []string, argv ...string) (string, error) {
	line := strings.Join(argv, " ")
	r.log = append(r.log, line)
	for prefix, err := range r.failOn {
		if strings.HasPrefix(line, prefix) {
			return "", err
		}
	}
	exit := func(code int) error { return &sysinstall.ExitError{Argv: argv, Code: code} }
	sc := strings.TrimPrefix(strings.TrimPrefix(line, cmdSystemctl+" "), "--user ")
	switch {
	case strings.HasPrefix(line, cmdUseradd):
		r.env.users.users["pimon"] = sysinstall.User{UID: 990, GID: 990}
	case strings.HasPrefix(line, cmdUsermod):
		r.env.users.groups[argv[3]] = append(r.env.users.groups[argv[3]], argv[2])
	case strings.HasPrefix(line, cmdSysctl+" -p "):
		// 应用后内核值即文件里的范围
		v := strings.TrimSpace(strings.TrimPrefix(r.env.fs.files[argv[2]].data, "net.ipv4.ping_group_range = "))
		r.env.fs.files[pingRangeProc].data = strings.Replace(v, " ", "\t", 1) + "\n"
	case !strings.HasPrefix(line, cmdSystemctl):
	case sc == "is-active "+ServiceName:
		if r.active {
			return "active\n", nil
		}
		return "inactive\n", exit(3)
	case sc == "is-enabled "+ServiceName:
		if r.enabled {
			return "enabled\n", nil
		}
		return "disabled\n", exit(1)
	case sc == "enable --now "+ServiceName:
		r.enabled, r.active = true, true
	case sc == "disable --now "+ServiceName:
		r.enabled, r.active = false, false
	case strings.HasPrefix(sc, "show -p ActiveEnterTimestampMonotonic"):
		return fmt.Sprintf("%d\n", r.monoUS), nil
	}
	if strings.HasPrefix(line, cmdLaunchctl) {
		switch argv[1] {
		case "print":
			if !r.loaded {
				return "", exit(113)
			}
		case "bootstrap":
			r.loaded = true
		case "bootout":
			if !r.loaded {
				return "", exit(3)
			}
			r.loaded = false
		}
	}
	return "", nil
}

type testEnv struct {
	clk    *autoClock
	fs     *fakeFS
	users  *fakeUsers
	ports  *fakePorts
	runner *fakeRunner
	out    bytes.Buffer
	in     string
	tty    bool

	goos       string
	euid, egid int
	home       string

	pairReqs   []link.PairRequest
	pairErr    error  // 拨号阶段失败（不调用 Save）
	pairAfter  error  // Save 之后返回的错误
	pairCred   string // pair_ok 里的凭据
	statusErr  error  // Status 恒返回的错误
	statusN    int    // Status 调用次数
	selfData   string // 当前可执行文件内容
	afterSave  func() // 非空时在配对的 Save 成功之后调用
	beforeSave func() // 非空时在配对的 Save 之前调用
	ctx        context.Context
}

const selfPath = "/tmp/dl/pimon-agent"

func newEnv(t *testing.T, goos string, euid int) *testEnv {
	t.Helper()
	clk := &autoClock{now: t0}
	e := &testEnv{
		clk:   clk,
		fs:    &fakeFS{clk: clk, files: map[string]*fakeFile{}, uptimeS: 1000},
		users: &fakeUsers{users: map[string]sysinstall.User{}, groups: map[string][]string{}},
		ports: &fakePorts{},
		goos:  goos, euid: euid, egid: 20,
		selfData: "BINARY-v2", pairCred: "cred-原文-secret",
		ctx: context.Background(),
	}
	if euid != 0 && goos == "linux" {
		e.egid = 1000
	}
	e.fs.creator = sysinstall.Owner{UID: euid, GID: e.egid}
	e.runner = &fakeRunner{env: e}
	switch goos {
	case "darwin":
		e.home = "/Users/alice"
	default:
		e.home = "/home/alice"
		e.fs.dir("/run/systemd/system", 0o755, 0, 0)
		e.fs.put("/proc/sys/net/ipv4/ping_group_range", "1\t0\n", 0o644, 0, 0)
	}
	e.fs.dir("/", 0o755, 0, 0)
	e.fs.dir("/tmp/dl", 0o755, 0, 0)
	e.fs.dir("/usr/local/bin", 0o755, 0, 0)
	e.fs.dir("/etc", 0o755, 0, 0)
	e.fs.dir("/etc/systemd/system", 0o755, 0, 0)
	e.fs.dir("/etc/sysctl.d", 0o755, 0, 0)
	e.fs.dir("/var/lib", 0o755, 0, 0)
	e.fs.dir(e.home, 0o750, euid, e.egid)
	e.fs.put(selfPath, e.selfData, 0o755, euid, e.egid)
	return e
}

func (e *testEnv) deps() Deps {
	e.fs.files[selfPath].data = e.selfData
	return Deps{
		Out: &e.out, In: strings.NewReader(e.in), Interactive: e.tty,
		Clock: e.clk, Runner: e.runner, FS: e.fs, Users: e.users, Ports: e.ports,
		SudoFS: e.fs.sudoFS(),
		GOOS:   e.goos, EUID: e.euid, EGID: e.egid, Home: e.home, Username: "alice",
		Self: selfPath,
		Pair: func(ctx context.Context, req link.PairRequest) (link.PairResult, error) {
			e.pairReqs = append(e.pairReqs, req)
			if e.pairErr != nil {
				return link.PairResult{}, e.pairErr
			}
			if e.beforeSave != nil {
				e.beforeSave()
			}
			ok := agent.PairOK{Credential: e.pairCred, DeviceID: "dev123", ListenerInfo: agent.ListenerInfo{Hostname: "pimon", Version: "1.0"}}
			if err := req.Save(ok); err != nil {
				return link.PairResult{}, err
			}
			if e.afterSave != nil {
				e.afterSave()
			}
			return link.PairResult{DeviceID: "dev123", HubHostname: "pimon", HubVersion: "1.0"}, e.pairAfter
		},
		Hello: func() agent.Hello { return link.NewHello("box", nil) },
		Status: func(context.Context, string) error {
			e.statusN++
			return e.statusErr
		},
	}
}

const (
	testFP  = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"
	testHub = "wss://192.168.1.10:31418"
)

func dialIn() Options {
	return Options{Hub: testHub, Fingerprint: testFP, Code: "ABCD-EFGH"}
}

func dialOut() Options { return Options{Listen: ":31418"} }

func (e *testEnv) install(t *testing.T, o Options) error {
	t.Helper()
	return Install(e.ctx, e.deps(), o)
}

func (e *testEnv) mustInstall(t *testing.T, o Options) {
	t.Helper()
	if err := e.install(t, o); err != nil {
		t.Fatalf("install 失败: %v\n输出:\n%s", err, e.out.String())
	}
}

// reset 清空记录，模拟下一次运行。
func (e *testEnv) reset() {
	e.runner.log = nil
	e.fs.ops = nil
	e.out.Reset()
	e.pairReqs = nil
	e.ports.got = nil
}

func (e *testEnv) file(t *testing.T, name string) *fakeFile {
	t.Helper()
	f, ok := e.fs.files[name]
	if !ok {
		t.Fatalf("文件 %s 不存在；现有操作: %v", name, e.fs.ops)
	}
	return f
}

func assertLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s 不符\n得到:\n  %s\n期望:\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func assertContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Fatalf("输出缺少 %q:\n%s", sub, s)
		}
	}
}

func assertNotContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Fatalf("输出不应包含 %q:\n%s", sub, s)
		}
	}
}

func hasCmd(log []string, prefix string) bool {
	return slices.ContainsFunc(log, func(l string) bool { return strings.HasPrefix(l, prefix) })
}

var errBoom = errors.New("boom")
