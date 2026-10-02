package kiosk

import (
	"reflect"
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
