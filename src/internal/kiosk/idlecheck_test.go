package kiosk

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const raspiSwayidle = "swayidle -w timeout 600 'wlopm --off *' resume 'wlopm --on *' &\n"

type idleFixture struct {
	root    string
	checker *IdleChecker
	clk     *armClock
	changes atomic.Int32
}

func newIdleFixture(t *testing.T) *idleFixture {
	t.Helper()
	f := &idleFixture{root: t.TempDir(), clk: newArmClock(testStart)}
	writeFiles(t, filepath.Join(f.root, "proc"), map[string]string{})
	f.checker = NewIdleChecker(IdleConfig{
		UserPath:    filepath.Join(f.root, "user-autostart"),
		GreeterPath: filepath.Join(f.root, "greeter-autostart"),
		SystemPath:  filepath.Join(f.root, "system-autostart"),
		ProcRoot:    filepath.Join(f.root, "proc"),
		Clock:       f.clk,
		Log:         quietLogger(),
		OnChange:    func() { f.changes.Add(1) },
	})
	return f
}

func (f *idleFixture) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *idleFixture) addProc(t *testing.T, pid, comm string) {
	t.Helper()
	writeFiles(t, filepath.Join(f.root, "proc", pid), map[string]string{"comm": comm + "\n"})
}

func TestIdleCheck_解析三处autostart的swayidle行(t *testing.T) {
	f := newIdleFixture(t)
	f.write(t, "user-autostart", "/home/u/m0/kiosk.sh &\n"+raspiSwayidle)
	f.write(t, "greeter-autostart", "/usr/sbin/pi-greeter &\n# "+raspiSwayidle)
	f.write(t, "system-autostart", "lwrespawn pcmanfm-pi\nlwrespawn swayidle -w\n")
	got := f.checker.Check()
	if !got.User || got.Greeter || !got.System || got.SwayidleRunning {
		t.Fatalf("got %+v", got)
	}
	if !got.CheckedAt.Equal(testStart) {
		t.Fatalf("CheckedAt=%v", got.CheckedAt)
	}
}

func TestIdleCheck_文件缺失按未命中处理(t *testing.T) {
	f := newIdleFixture(t)
	got := f.checker.Check()
	if got.User || got.Greeter || got.System || got.SwayidleRunning {
		t.Fatalf("got %+v", got)
	}
}

func TestIdleCheck_swayidle进程(t *testing.T) {
	f := newIdleFixture(t)
	f.addProc(t, "10", "labwc")
	f.addProc(t, "11", "swayidle-helper")
	if f.checker.Check().SwayidleRunning {
		t.Fatal("没有 swayidle 进程")
	}
	f.addProc(t, "12", "swayidle")
	if !f.checker.Check().SwayidleRunning {
		t.Fatal("应检测到 swayidle 进程")
	}
}

func TestIdleCheck_Run启动即检查并每小时复查(t *testing.T) {
	f := newIdleFixture(t)
	if f.checker.Last() != nil {
		t.Fatal("检查前应为 nil")
	}
	done := runUntilCancel(t, f.checker.Run)
	f.clk.waitArmed(t, time.Hour)
	last := f.checker.Last()
	if last == nil || last.User {
		t.Fatalf("初次结果 %+v", last)
	}
	if f.changes.Load() != 1 {
		t.Fatalf("初次检查应通知 1 次，实际 %d", f.changes.Load())
	}

	f.write(t, "user-autostart", raspiSwayidle)
	f.clk.Advance(time.Hour)
	f.clk.waitArmed(t, time.Hour)
	last = f.checker.Last()
	if last == nil || !last.User {
		t.Fatalf("一小时后应发现 swayidle: %+v", last)
	}
	if !last.CheckedAt.Equal(testStart.Add(time.Hour)) {
		t.Fatalf("CheckedAt=%v", last.CheckedAt)
	}
	done()
}

func TestIdleCheck_Last返回副本(t *testing.T) {
	f := newIdleFixture(t)
	f.write(t, "user-autostart", raspiSwayidle)
	done := runUntilCancel(t, f.checker.Run)
	f.clk.waitArmed(t, time.Hour)
	a := f.checker.Last()
	a.User = false
	if b := f.checker.Last(); !b.User {
		t.Fatal("修改返回值不应影响内部状态")
	}
	done()
}

func TestUserAutostartPath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := UserAutostartPath(env(map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "HOME": "/h"})); got != "/x/cfg/labwc/autostart" {
		t.Fatal(got)
	}
	if got := UserAutostartPath(env(map[string]string{"HOME": "/h"})); got != "/h/.config/labwc/autostart" {
		t.Fatal(got)
	}
	if got := UserAutostartPath(env(nil)); got != "" {
		t.Fatalf("两者都没有应返回空: %q", got)
	}
}
