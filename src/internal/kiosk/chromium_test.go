package kiosk

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestChromiumArgs_缩放为1时不带缩放参数(t *testing.T) {
	got := ChromiumArgs("/p/profile", "http://h/screen/auth?token=t", 1)
	want := []string{
		"--kiosk", "--ozone-platform=wayland", "--noerrdialogs", "--disable-infobars",
		"--no-first-run", "--disable-features=Translate", "--password-store=basic",
		"--hide-crash-restore-bubble", "--check-for-update-interval=31536000",
		"--user-data-dir=/p/profile",
		"http://h/screen/auth?token=t",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestChromiumArgs_缩放参数拼接(t *testing.T) {
	for scale, want := range map[float64]string{1.25: "--force-device-scale-factor=1.25", 1.5: "--force-device-scale-factor=1.5", 2: "--force-device-scale-factor=2"} {
		got := ChromiumArgs("/p", "http://u", scale)
		if got[len(got)-1] != "http://u" {
			t.Fatalf("URL 必须是最后一个参数: %v", got)
		}
		if got[len(got)-2] != want {
			t.Fatalf("scale %v: 期望 %q，实际 %v", scale, want, got)
		}
	}
	// 0 或负数按 1 处理
	for _, s := range []float64{0, -1} {
		for _, a := range ChromiumArgs("/p", "u", s) {
			if len(a) > 24 && a[:24] == "--force-device-scale-fac" {
				t.Fatalf("scale %v 不应带缩放参数", s)
			}
		}
	}
}

func TestBackoff_翻倍封顶并可清零(t *testing.T) {
	var b backoff
	var got []int
	for i := 0; i < 9; i++ {
		got = append(got, int(b.next().Seconds()))
	}
	if !reflect.DeepEqual(got, []int{1, 2, 4, 8, 16, 32, 60, 60, 60}) {
		t.Fatalf("got %v", got)
	}
	b.reset()
	if b.next().Seconds() != 1 {
		t.Fatal("清零后应从 1s 重新开始")
	}
}

func TestExecLauncher_进程组终止与回收(t *testing.T) {
	p, err := ExecLauncher{}.Start("/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
		t.Fatal("进程不应已退出")
	default:
	}
	p.Terminate()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM 后进程未退出")
	}
	if _, err := (ExecLauncher{}).Start("/nonexistent/chromium", nil); err == nil {
		t.Fatal("可执行文件不存在应报错")
	}
}

func TestLaunchAttr_独立进程组(t *testing.T) {
	if a := launchAttr(); a == nil || !a.Setpgid {
		t.Fatalf("应设置 Setpgid: %+v", a)
	}
}

func TestExecLauncher_暴露进程号(t *testing.T) {
	p, err := ExecLauncher{}.Start("/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	pp, ok := p.(interface{ Pid() int })
	if !ok || pp.Pid() <= 0 {
		t.Fatalf("进程应暴露有效的 Pid: %v", p)
	}
}

func TestExecLauncher_组长退出后清理残留子进程(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// 组长 sh 起一个后台 sleep 后立刻退出，sleep 仍留在进程组里。
	p, err := ExecLauncher{}.Start("/bin/sh", []string{"-c", `sleep 60 & echo $! > "$0"; exit 0`, pidFile})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("组长未退出")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("残留子进程 %d 应已被清理", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
