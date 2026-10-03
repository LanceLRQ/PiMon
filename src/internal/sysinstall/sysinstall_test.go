package sysinstall

import (
	"bytes"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOSFS原子写与属主权限(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bin")
	var fsys OSFS
	if err := fsys.WriteFile(target, []byte("v1"), 0o755, nil); err != nil {
		t.Fatal(err)
	}
	own := &Owner{UID: os.Getuid(), GID: os.Getgid()}
	if err := fsys.WriteFile(target, []byte("v2-longer"), 0o640, own); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	fi, err := fsys.Stat(target)
	if string(b) != "v2-longer" || err != nil || fi.Mode != 0o640 || fi.UID != own.UID || fi.GID != own.GID {
		t.Fatalf("content=%q fi=%+v err=%v", b, fi, err)
	}
	names, _ := fsys.ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("不应残留临时文件: %v", names)
	}
}

func TestOSFS写入失败不留临时文件(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "d")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (OSFS{}).WriteFile(target, []byte("x"), 0o644, nil); err == nil {
		t.Fatal("期望失败")
	}
	names, _ := (OSFS{}).ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("失败后应清理临时文件: %v", names)
	}
}

func TestOSFSMkdir与Lstat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	own := Owner{UID: os.Getuid(), GID: os.Getgid()}
	var fsys OSFS
	if err := fsys.Mkdir(dir, 0o750, own); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(dir), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if fi, err := fsys.Lstat(link); err != nil || !fi.IsSymlink {
		t.Fatalf("%+v %v", fi, err)
	}
	if fi, err := fsys.Stat(link); err != nil || !fi.IsDir || fi.Mode != 0o750 {
		t.Fatalf("%+v %v", fi, err)
	}
}

func TestExecRunner成功与退出码及环境(t *testing.T) {
	var r ExecRunner
	out, err := r.Run(t.Context(), "sh", "-c", "echo hi")
	if err != nil || out != "hi\n" {
		t.Fatalf("%q %v", out, err)
	}
	_, err = r.Run(t.Context(), "sh", "-c", "echo bad >&2; exit 3")
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 3 || !strings.Contains(ee.Error(), "bad") {
		t.Fatalf("%#v", err)
	}
	t.Setenv("PIMON_X", "1")
	out, err = r.RunEnv(t.Context(), []string{"PIMON_Y=2"}, "sh", "-c", "echo $PIMON_X-$PIMON_Y")
	if err != nil || out != "-2\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestBuildEnv(t *testing.T) {
	got := BuildEnv([]string{"PATH=/usr/bin", "PIMON_A=1", "HOME=/r"}, []string{"PIMON_B=2"})
	if strings.Join(got, ",") != "PATH=/usr/bin,HOME=/r,PIMON_B=2" {
		t.Fatalf("%v", got)
	}
}

func TestOSUsers查询不存在的用户(t *testing.T) {
	var u OSUsers
	if _, err := u.Lookup("pimon-no-such-user-xyz"); err != ErrNotFound {
		t.Fatalf("err=%v", err)
	}
	if _, err := u.Home("pimon-no-such-user-xyz"); err != ErrNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestBindPorts占用判断(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skip("无法监听: ", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	got, err := BindPorts{}.Listeners(port)
	if err != nil || len(got) != 1 || got[0].PID != 0 {
		t.Fatalf("占用端口: %+v %v", got, err)
	}
	_ = ln.Close()
	got, err = BindPorts{}.Listeners(port)
	if err != nil || len(got) != 0 {
		t.Fatalf("空闲端口: %+v %v", got, err)
	}
}

func TestNewPorts按平台选择(t *testing.T) {
	if _, ok := NewPorts("darwin").(BindPorts); !ok {
		t.Fatal("darwin 应为 BindPorts")
	}
	if p, ok := NewPorts("linux").(ProcPorts); !ok || p.Root != "/proc" {
		t.Fatal("linux 应为 ProcPorts")
	}
}

func TestReport输出格式(t *testing.T) {
	var buf bytes.Buffer
	r := NewReport(&buf)
	r.Failure("第一步", os.ErrPermission)
	r.Done("a", "d")
	r.Skipped("b", "")
	r.Existed("c", "")
	r.Warn("d", "w")
	r.Failure("末步", os.ErrPermission)
	s := buf.String()
	for _, want := range []string{"尚未完成任何步骤", "[完成] a（d）\n", "[跳过] b\n", "[已存在] c\n", "[警告] d（w）\n", "[失败] 末步：", "  - 完成：a\n", "排除问题后重新运行 install"} {
		if !strings.Contains(s, want) {
			t.Fatalf("缺少 %q:\n%s", want, s)
		}
	}
}

func TestBindPorts只监听回环也判占用(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1"} {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			t.Skipf("无法监听 %s: %v", host, err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		got, err := BindPorts{}.Listeners(port)
		_ = ln.Close()
		if err != nil || len(got) != 1 {
			t.Fatalf("%s 占用应判占用: %+v %v", host, got, err)
		}
	}
}

func TestBindPorts无权限错误可判断(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "darwin" {
		t.Skip("仅非 root 的 Linux 上 <1024 端口无权限")
	}
	_, err := BindPorts{}.Listeners(1)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("应为无权限错误: %v", err)
	}
}
