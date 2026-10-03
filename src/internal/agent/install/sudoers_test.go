package install

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

// fakeFileInfo 把 fakeFile 包装成 fs.FileInfo，供 sudocmd.Resolve 做属主与权限校验。
type fakeFileInfo struct {
	name string
	f    *fakeFile
}

func (i fakeFileInfo) Name() string { return path.Base(i.name) }
func (i fakeFileInfo) Size() int64  { return int64(len(i.f.data)) }
func (i fakeFileInfo) Mode() fs.FileMode {
	m := i.f.mode
	if i.f.dir {
		m |= fs.ModeDir
	}
	return m
}
func (i fakeFileInfo) ModTime() time.Time { return i.f.mtime }
func (i fakeFileInfo) IsDir() bool        { return i.f.dir }
func (i fakeFileInfo) Sys() any           { return i.f }

// sudoFS 让 sudocmd 在 fakeFS 上解析命令：Stat 跟随符号链接，属主取 fakeFile.uid。
func (f *fakeFS) sudoFS() sudocmd.FS {
	follow := func(p string) (string, error) {
		for range 8 {
			e, ok := f.files[p]
			if !ok {
				return "", fmt.Errorf("stat %s: %w", p, fs.ErrNotExist)
			}
			if e.link == "" {
				return p, nil
			}
			p = e.link
		}
		return "", fmt.Errorf("%s: 符号链接层数过多", p)
	}
	return sudocmd.FS{
		Stat: func(p string) (fs.FileInfo, error) {
			real, err := follow(p)
			if err != nil {
				return nil, err
			}
			return fakeFileInfo{real, f.files[real]}, nil
		},
		EvalSymlinks: follow,
		Owner: func(fi fs.FileInfo) (uint32, bool) {
			return uint32(fi.Sys().(*fakeFile).uid), true
		},
	}
}

const (
	sudoersCheck = "/etc/sudoers.d/.pimon-agent.check"
	includeDir   = "#\n# This file MUST be edited with the 'visudo' command as root.\nroot ALL=(ALL:ALL) ALL\n@includedir /etc/sudoers.d\n"
)

// storageHost 准备 sudo 环境（/etc/sudoers 含 includedir、/etc/sudoers.d、visudo），并在 /usr/sbin 放入给定命令。
func storageHost(e *testEnv, cmds ...string) {
	e.fs.dir("/usr", 0o755, 0, 0)
	e.fs.dir("/usr/sbin", 0o755, 0, 0)
	e.fs.put("/etc/sudoers", includeDir, 0o440, 0, 0)
	e.fs.dir("/etc/sudoers.d", 0o750, 0, 0)
	e.fs.put(cmdVisudo, "", 0o755, 0, 0)
	for _, c := range cmds {
		e.fs.put("/usr/sbin/"+c, "", 0o755, 0, 0)
	}
}

func allStorageCmds(e *testEnv) { storageHost(e, "smartctl", "mdadm", "btrfs") }

func withSudoers(o Options) Options {
	o.Sudoers = true
	return o
}

func wantSudoers(t *testing.T, paths map[string]string) string {
	t.Helper()
	s, err := sudocmd.SudoersFile("pimon", paths)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var allPaths = map[string]string{"smartctl": "/usr/sbin/smartctl", "mdadm": "/usr/sbin/mdadm", "btrfs": "/usr/sbin/btrfs"}

// assertSudoersWritten 断言 sudoers 已按「暂存 → visudo → 0440 root:root 原子写入 → 删暂存」落地。
func assertSudoersWritten(t *testing.T, e *testEnv, want string) {
	t.Helper()
	f := e.file(t, sudoersPath)
	if f.data != want {
		t.Fatalf("sudoers 内容不符\n得到:\n%s\n期望:\n%s", f.data, want)
	}
	if f.mode != 0o440 || f.uid != 0 || f.gid != 0 {
		t.Fatalf("sudoers 权限 %04o %d:%d，期望 0440 0:0", f.mode, f.uid, f.gid)
	}
	if _, ok := e.fs.files[sudoersCheck]; ok {
		t.Fatal("暂存文件应已删除")
	}
	var got []string
	for _, op := range e.fs.ops {
		if strings.Contains(op, "/etc/sudoers.d/") {
			got = append(got, op)
		}
	}
	assertLines(t, "sudoers 文件操作", got, []string{
		"write " + sudoersCheck + " 0440 0:0",
		"write " + sudoersPath + " 0440 0:0",
		"rm " + sudoersCheck,
	})
	if !hasCmd(e.runner.log, cmdVisudo+" -cf "+sudoersCheck) {
		t.Fatalf("没有用 visudo 校验暂存文件，命令 %v", e.runner.log)
	}
}

func assertNoSudoers(t *testing.T, e *testEnv) {
	t.Helper()
	for _, p := range []string{sudoersPath, sudoersCheck} {
		if _, ok := e.fs.files[p]; ok {
			t.Fatalf("%s 不应存在", p)
		}
	}
	if hasCmd(e.runner.log, cmdVisudo) {
		t.Fatalf("不应调用 visudo，命令 %v", e.runner.log)
	}
}

func TestSudoers_交互确认写入(t *testing.T) {
	for _, ans := range []string{"y\n", "yes\n", "Y\n"} {
		t.Run(strings.TrimSpace(ans), func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			allStorageCmds(e)
			e.tty, e.in = true, ans
			e.mustInstall(t, dialOut())
			assertSudoersWritten(t, e, wantSudoers(t, allPaths))
			out := e.out.String()
			assertContains(t, out, "[y/N]", "/usr/sbin/smartctl -j -a -n standby /dev/sd[a-z]",
				"/usr/sbin/mdadm --detail /dev/md[0-9]", "/usr/sbin/btrfs scrub status /dev/dm-[0-9]",
				"[完成] 写入 sudoers 白名单")
		})
	}
}

func TestSudoers_交互拒绝(t *testing.T) {
	for _, ans := range []string{"n\n", "\n", ""} {
		t.Run(fmt.Sprintf("%q", ans), func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			allStorageCmds(e)
			e.tty, e.in = true, ans
			e.mustInstall(t, dialOut())
			assertNoSudoers(t, e)
			assertContains(t, e.out.String(), "[y/N]", "[跳过] sudoers", "无权限")
		})
	}
}

func TestSudoers_非交互无参数_跳过并提示(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.mustInstall(t, dialOut())
	assertNoSudoers(t, e)
	out := e.out.String()
	assertContains(t, out, "[警告] sudoers", "--sudoers", "无权限")
	assertNotContains(t, out, "[y/N]")
}

func TestSudoers_非交互显式参数_写入(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.mustInstall(t, withSudoers(dialOut()))
	assertSudoersWritten(t, e, wantSudoers(t, allPaths))
	assertNotContains(t, e.out.String(), "[y/N]")
}

func TestSudoers_交互且显式参数_不再询问(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.tty = true
	e.mustInstall(t, withSudoers(dialOut()))
	assertSudoersWritten(t, e, wantSudoers(t, allPaths))
	assertNotContains(t, e.out.String(), "[y/N]")
}

func TestSudoers_内容快照(t *testing.T) {
	e := newEnv(t, "linux", 0)
	storageHost(e, "smartctl")
	e.mustInstall(t, withSudoers(dialOut()))
	got := strings.Split(e.file(t, sudoersPath).data, "\n")
	const p = "pimon ALL=(root) NOPASSWD: /usr/sbin/smartctl "
	assertLines(t, "sudoers 逐行", got, []string{
		"# 由 pimon-agent install 生成，请勿手工修改；pimon-agent uninstall 会删除本文件。",
		"# 只放行 storage-health 插件以 sudo -n 执行的只读命令，参数逐字精确匹配，不含通配符。",
		"# 保留 sudo 的 COMMAND 审计日志，只关闭每次执行产生的 PAM 会话记录。",
		"Defaults:pimon !pam_session",
		p + "--scan-open",
		p + "-j -a -n standby /dev/sd[a-z]",
		p + "-j -a -n standby /dev/sd[a-z][a-z]",
		p + "-j -a -n standby /dev/nvme[0-9]",
		p + "-j -a -n standby /dev/nvme[0-9][0-9]",
		p + "-j -a -n standby /dev/nvme[0-9]n[0-9]",
		p + "-j -a -n standby /dev/nvme[0-9][0-9]n[0-9]",
		p + "-j -a -n standby -d sat /dev/sd[a-z]",
		p + "-j -a -n standby -d sat /dev/sd[a-z][a-z]",
		"",
	})
}

func TestSudoers_全部命令的每一行都受限(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.mustInstall(t, withSudoers(dialOut()))
	data := e.file(t, sudoersPath).data
	if strings.Contains(data, "*") || strings.Contains(data, "!syslog") {
		t.Fatalf("不应含 * 或 !syslog:\n%s", data)
	}
	rules := 0
	for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# "), line == "Defaults:pimon !pam_session":
		case strings.HasPrefix(line, "pimon ALL=(root) NOPASSWD: /usr/sbin/smartctl "),
			strings.HasPrefix(line, "pimon ALL=(root) NOPASSWD: /usr/sbin/mdadm --detail /dev/md"),
			strings.HasPrefix(line, "pimon ALL=(root) NOPASSWD: /usr/sbin/btrfs device stats /dev/"),
			strings.HasPrefix(line, "pimon ALL=(root) NOPASSWD: /usr/sbin/btrfs scrub status /dev/"):
			rules++
		default:
			t.Fatalf("意外的行 %q", line)
		}
	}
	specs, _ := sudocmd.Specs(allPaths)
	if rules != len(specs) || rules == 0 {
		t.Fatalf("规则 %d 条，规格 %d 条", rules, len(specs))
	}
}

func TestSudoers_命令不存在时不列入(t *testing.T) {
	e := newEnv(t, "linux", 0)
	storageHost(e, "mdadm")
	e.tty, e.in = true, "y\n"
	e.mustInstall(t, dialOut())
	assertSudoersWritten(t, e, wantSudoers(t, map[string]string{"mdadm": "/usr/sbin/mdadm"}))
	data := e.file(t, sudoersPath).data
	assertNotContains(t, data, "smartctl", "btrfs")
	assertNotContains(t, e.out.String(), "/usr/sbin/smartctl", "/usr/sbin/btrfs")
}

func TestSudoers_命令属主不安全时不列入并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	storageHost(e, "mdadm")
	e.fs.put("/usr/sbin/smartctl", "", 0o755, 1000, 1000)
	e.mustInstall(t, withSudoers(dialOut()))
	assertSudoersWritten(t, e, wantSudoers(t, map[string]string{"mdadm": "/usr/sbin/mdadm"}))
	assertContains(t, e.out.String(), "[警告] sudoers（不放行 smartctl", "属主不是 root")
}

func TestSudoers_三个命令都不存在_跳过并说明(t *testing.T) {
	e := newEnv(t, "linux", 0)
	storageHost(e)
	e.tty, e.in = true, "y\n"
	e.mustInstall(t, withSudoers(dialOut()))
	assertNoSudoers(t, e)
	out := e.out.String()
	assertContains(t, out, "[跳过] sudoers", "未检测到 smartctl、mdadm、btrfs")
	assertNotContains(t, out, "[y/N]")
}

func TestSudoers_visudo校验失败_不落地并删除暂存(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.runner.failOn = map[string]error{cmdVisudo: &sysinstall.ExitError{Argv: []string{cmdVisudo}, Code: 1, Stderr: "syntax error near line 5"}}
	e.mustInstall(t, withSudoers(dialOut()))
	for _, p := range []string{sudoersPath, sudoersCheck} {
		if _, ok := e.fs.files[p]; ok {
			t.Fatalf("%s 不应存在", p)
		}
	}
	if !hasCmd(e.runner.log, cmdVisudo+" -cf "+sudoersCheck) {
		t.Fatal("应先用 visudo 校验")
	}
	for _, op := range e.fs.ops {
		if strings.HasPrefix(op, "write "+sudoersPath) {
			t.Fatalf("校验失败后不应写入目标文件: %v", e.fs.ops)
		}
	}
	assertContains(t, e.out.String(), "[警告] sudoers", "visudo", "syntax error near line 5", "未写入")
	if !e.runner.active {
		t.Fatal("sudoers 失败不应阻止服务启动")
	}
}

func TestSudoers_visudo校验失败_保留原有文件(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.fs.put(sudoersPath, "旧内容\n", 0o440, 0, 0)
	e.runner.failOn = map[string]error{cmdVisudo: &sysinstall.ExitError{Argv: []string{cmdVisudo}, Code: 1}}
	e.mustInstall(t, withSudoers(dialOut()))
	if got := e.file(t, sudoersPath).data; got != "旧内容\n" {
		t.Fatalf("原有文件被改动: %q", got)
	}
	if _, ok := e.fs.files[sudoersCheck]; ok {
		t.Fatal("暂存文件应已删除")
	}
}

func TestSudoers_暂存写入失败_install失败(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.fs.failOn = map[string]error{"write " + sudoersCheck: errBoom}
	err := e.install(t, withSudoers(dialOut()))
	if err == nil || !strings.Contains(err.Error(), "sudoers") {
		t.Fatalf("应在 sudoers 步骤失败，得到 %v", err)
	}
	assertNoSudoers(t, e)
}

func TestSudoers_目标写入失败_删除暂存并失败(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.fs.failOn = map[string]error{"write " + sudoersPath: errBoom}
	if err := e.install(t, withSudoers(dialOut())); err == nil {
		t.Fatal("应失败")
	}
	for _, p := range []string{sudoersPath, sudoersCheck} {
		if _, ok := e.fs.files[p]; ok {
			t.Fatalf("%s 不应存在", p)
		}
	}
}

func TestSudoers_visudo缺失_不写并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	delete(e.fs.files, cmdVisudo)
	e.mustInstall(t, withSudoers(dialOut()))
	assertNoSudoers(t, e)
	assertContains(t, e.out.String(), "[警告] sudoers", cmdVisudo, "无权限")
}

func TestSudoers_未安装sudo_不写并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	delete(e.fs.files, "/etc/sudoers")
	e.mustInstall(t, withSudoers(dialOut()))
	assertNoSudoers(t, e)
	assertContains(t, e.out.String(), "[警告] sudoers", "/etc/sudoers")
}

func TestSudoers_includedir缺失_不写并警告(t *testing.T) {
	for _, conf := range []string{
		"root ALL=(ALL:ALL) ALL\n",
		"# includedir /etc/sudoers.d\n",
		"@includedir /etc/other.d\n",
		"@includedir /etc/sudoers.dx\n",
	} {
		t.Run(conf, func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			allStorageCmds(e)
			e.fs.put("/etc/sudoers", conf, 0o440, 0, 0)
			e.mustInstall(t, withSudoers(dialOut()))
			assertNoSudoers(t, e)
			assertContains(t, e.out.String(), "[警告] sudoers", "includedir")
		})
	}
}

func TestSudoers_includedir两种写法都认(t *testing.T) {
	for _, conf := range []string{
		"@includedir /etc/sudoers.d\n",
		"#includedir /etc/sudoers.d\n",
		"root ALL=(ALL) ALL\n  @includedir   /etc/sudoers.d/  \n",
	} {
		t.Run(conf, func(t *testing.T) {
			e := newEnv(t, "linux", 0)
			allStorageCmds(e)
			e.fs.put("/etc/sudoers", conf, 0o440, 0, 0)
			e.mustInstall(t, withSudoers(dialOut()))
			assertSudoersWritten(t, e, wantSudoers(t, allPaths))
		})
	}
}

func TestSudoers_sudoers目录缺失_不写并警告(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	delete(e.fs.files, "/etc/sudoers.d")
	e.mustInstall(t, withSudoers(dialOut()))
	assertNoSudoers(t, e)
	assertContains(t, e.out.String(), "[警告] sudoers", "/etc/sudoers.d")
}

func TestSudoers_内容相同则跳过(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.mustInstall(t, withSudoers(dialOut()))
	e.reset()
	e.tty, e.in = true, "" // 内容相同不应再询问
	e.mustInstall(t, dialOut())
	if hasCmd(e.runner.log, cmdVisudo) {
		t.Fatal("内容相同不应再校验")
	}
	for _, op := range e.fs.ops {
		if strings.Contains(op, "/etc/sudoers.d/") {
			t.Fatalf("内容相同不应写文件: %v", e.fs.ops)
		}
	}
	out := e.out.String()
	assertContains(t, out, "[已存在] sudoers 白名单")
	assertNotContains(t, out, "[y/N]")
}

func TestSudoers_内容变化时重写(t *testing.T) {
	e := newEnv(t, "linux", 0)
	storageHost(e, "mdadm")
	e.mustInstall(t, withSudoers(dialOut()))
	allStorageCmds(e) // 之后装上了 smartctl 与 btrfs
	e.reset()
	e.mustInstall(t, withSudoers(dialOut()))
	assertSudoersWritten(t, e, wantSudoers(t, allPaths))
}

func TestSudoers_共用reader_两次询问都读到回答(t *testing.T) {
	e := newEnv(t, "linux", 0)
	dockerHost(e)
	allStorageCmds(e)
	e.tty, e.in = true, "y\nyes\n"
	e.mustInstall(t, dialOut())
	if !hasCmd(e.runner.log, "/usr/sbin/usermod -aG docker pimon") {
		t.Fatal("第一次回答应加入 docker 组")
	}
	assertSudoersWritten(t, e, wantSudoers(t, allPaths))
}

func TestSudoers_用户模式只提示(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	allStorageCmds(e)
	e.tty, e.in = true, "y\n"
	o := dialOut()
	o.User = true
	e.mustInstall(t, o)
	assertNoSudoers(t, e)
	out := e.out.String()
	assertContains(t, out, "sudoers", "用户模式不写 sudoers", "SMART", "无权限")
	assertNotContains(t, out, "[y/N]")
}

func TestSudoers_用户模式未检测到命令_不提示(t *testing.T) {
	e := newEnv(t, "linux", 1000)
	o := dialOut()
	o.User = true
	e.mustInstall(t, o)
	assertNotContains(t, e.out.String(), "sudoers")
}

func TestSudoers_macOS只提示(t *testing.T) {
	e := newEnv(t, "darwin", 501)
	e.tty, e.in = true, "y\n"
	o := dialOut()
	o.Sudoers = true
	e.mustInstall(t, o)
	assertNoSudoers(t, e)
	out := e.out.String()
	assertContains(t, out, "[跳过] sudoers", "macOS 不写 sudoers")
	assertNotContains(t, out, "[y/N]")
}

func TestUninstall_删除install写入的sudoers(t *testing.T) {
	e := newEnv(t, "linux", 0)
	allStorageCmds(e)
	e.mustInstall(t, withSudoers(dialOut()))
	e.file(t, sudoersPath)
	e.reset()
	e.uninstall(t, UninstallOptions{})
	if _, ok := e.fs.files[sudoersPath]; ok {
		t.Fatal("uninstall 应删除 sudoers 文件")
	}
	assertContains(t, e.out.String(), "[完成] 删除 sudoers 文件 "+sudoersPath)
}
