package sdnotify

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

func listen(t *testing.T) (*net.UnixConn, string) {
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := filepath.Join(dir, "n.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: p, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, p
}

func recv(t *testing.T, c *net.UnixConn) string {
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, _, err := c.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func pid42() int { return 42 }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestNotifyReady(t *testing.T) {
	conn, p := listen(t)
	n := New(env(map[string]string{"NOTIFY_SOCKET": p}), pid42)
	if err := n.Notify("READY=1"); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, conn); got != "READY=1" {
		t.Fatalf("收到 %q", got)
	}
}

func TestNotifyWithoutSocketIsSilent(t *testing.T) {
	n := New(env(nil), pid42)
	if err := n.Notify("READY=1"); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyBadSocketErrors(t *testing.T) {
	n := New(env(map[string]string{"NOTIFY_SOCKET": "/nonexistent-dir/x.sock"}), pid42)
	if err := n.Notify("READY=1"); err == nil {
		t.Fatal("无法连接时应返回错误")
	}
}

func TestSocketAddrAbstract(t *testing.T) {
	a := socketAddr("@abc")
	if a.Name != "\x00abc" || a.Net != "unixgram" {
		t.Fatalf("抽象命名空间转换错误: %q", a.Name)
	}
	if socketAddr("/run/x").Name != "/run/x" {
		t.Fatal("普通路径不应改动")
	}
}

func TestWatchdogInterval(t *testing.T) {
	cases := []struct {
		v    string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"abc", 0, false},
		{"0", 0, false},
		{"-5", 0, false},
		{"60000000", 30 * time.Second, true},
		{"1", time.Second, true},
		{"1000000", time.Second, true},
		{"1999999", time.Second, true},
		{"2000000", time.Second, true},
		{"3000000", 1500 * time.Millisecond, true},
	}
	for _, c := range cases {
		got, ok := New(env(map[string]string{"WATCHDOG_USEC": c.v}), pid42).WatchdogInterval()
		if got != c.want || ok != c.ok {
			t.Errorf("WATCHDOG_USEC=%q: got (%v,%v), want (%v,%v)", c.v, got, ok, c.want, c.ok)
		}
	}
}

func TestRunWatchdogTicksWithFakeClock(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var sent = make(chan string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunWatchdog(ctx, fake, 30*time.Second, func(s string) error { sent <- s; return nil })
		close(done)
	}()
	waitWaiters := func() {
		for i := 0; i < 500 && fake.Waiters() < 1; i++ {
			time.Sleep(time.Millisecond)
		}
		if fake.Waiters() < 1 {
			t.Fatal("未等待假时钟")
		}
	}
	for i := 0; i < 2; i++ {
		waitWaiters()
		fake.Advance(30 * time.Second)
		select {
		case s := <-sent:
			if s != "WATCHDOG=1" {
				t.Fatalf("收到 %q", s)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("未发送心跳")
		}
	}
	waitWaiters()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 结束后应返回")
	}
}

func TestWatchdogPID(t *testing.T) {
	cases := []struct {
		name string
		pid  string
		ok   bool
	}{
		{"未设置", "", true},
		{"等于本进程", "42", true},
		{"不等于本进程", "43", false},
		{"非数字", "abc", false},
		{"带空白但相等", " 42 ", false},
	}
	for _, c := range cases {
		m := map[string]string{"WATCHDOG_USEC": "60000000"}
		if c.pid != "" {
			m["WATCHDOG_PID"] = c.pid
		}
		got, ok := New(env(m), pid42).WatchdogInterval()
		if ok != c.ok {
			t.Errorf("%s: ok = %v, 期望 %v", c.name, ok, c.ok)
		}
		if ok && got != 30*time.Second {
			t.Errorf("%s: 间隔 = %v", c.name, got)
		}
	}
}
