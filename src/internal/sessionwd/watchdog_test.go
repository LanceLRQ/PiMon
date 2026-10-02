package sessionwd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const user = "lancelrq"

type memFiles struct {
	mu    sync.Mutex
	files map[string]string
	reads int
}

func (m *memFiles) ReadFile(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	s, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("open %s: %w", name, fs.ErrNotExist)
	}
	return []byte(s), nil
}

func (m *memFiles) ReadDir(name string) ([]string, error) {
	return nil, fmt.Errorf("readdir %s: %w", name, fs.ErrNotExist)
}

func (m *memFiles) set(name, data string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = data
}

type harness struct {
	t        *testing.T
	clk      *clock.Fake
	w        *Watchdog
	files    *memFiles
	log      *bytes.Buffer
	sessions string // list-sessions 输出
	show     map[string]string
	listErr  error
	dialErr  error
	restarts int
	restartE error
	calls    []string
	dialed   []string
}

const goodShow = "Name=lancelrq\nType=wayland\nService=lightdm-autologin\nState=active\n"

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:        t,
		clk:      clock.NewFake(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)),
		files:    &memFiles{files: map[string]string{"/etc/lightdm/lightdm.conf": "[Seat:*]\nautologin-user=lancelrq\n"}},
		log:      &bytes.Buffer{},
		sessions: "  c1 1000 lancelrq seat0 -\n",
		show:     map[string]string{"c1": goodShow},
	}
	h.w = New(Config{User: user}, Deps{
		Clock: h.clk,
		Log:   slog.New(slog.NewTextHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Files: h.files,
		Loginctl: func(_ context.Context, args ...string) (string, error) {
			h.calls = append(h.calls, strings.Join(args, " "))
			switch args[0] {
			case "list-sessions":
				return h.sessions, h.listErr
			case "show-session":
				return h.show[args[1]], nil
			}
			return "", errors.New("unexpected")
		},
		Restart: func(context.Context) error {
			h.restarts++
			return h.restartE
		},
		Dial: func(p string) error {
			h.dialed = append(h.dialed, p)
			return h.dialErr
		},
		LookupUID: func(string) (int, error) { return 1000, nil },
	})
	return h
}

// tick 推进 d 后执行一次检查。
func (h *harness) tick(d time.Duration) {
	h.t.Helper()
	h.clk.Advance(d)
	h.w.Step(context.Background())
}

// pastGrace 越过启动宽限（用一次健康检查）。
func (h *harness) pastGrace() { h.tick(Grace + time.Second) }

func (h *harness) kill() { h.dialErr = errors.New("connection refused") }

func TestGraceNoJudgement(t *testing.T) {
	h := newHarness(t)
	h.kill()
	h.sessions = ""
	for i := 0; i < 5; i++ {
		h.tick(10 * time.Second) // 共 50s，仍在宽限内
	}
	if h.restarts != 0 || len(h.calls) != 0 {
		t.Fatalf("宽限期内不应检查或重启: restarts=%d calls=%v", h.restarts, h.calls)
	}
}

func TestHealthyNeverRestarts(t *testing.T) {
	h := newHarness(t)
	h.pastGrace()
	for i := 0; i < 20; i++ {
		h.tick(Interval)
	}
	if h.restarts != 0 {
		t.Fatalf("健康时不应重启")
	}
	if len(h.dialed) == 0 || h.dialed[0] != "/run/user/1000/wayland-0" {
		t.Fatalf("socket 路径 = %v", h.dialed)
	}
	if h.calls[1] != "show-session c1 -p Name -p Type -p Service -p State" {
		t.Fatalf("show-session 参数 = %q", h.calls[1])
	}
	if h.calls[0] != "list-sessions --no-legend" {
		t.Fatalf("list-sessions 参数 = %q", h.calls[0])
	}
}

func TestAliveVariants(t *testing.T) {
	cases := []struct {
		name  string
		show  string
		alive bool
	}{
		{"active", goodShow, true},
		{"online", strings.Replace(goodShow, "active", "online", 1), true},
		{"closing", strings.Replace(goodShow, "active", "closing", 1), false},
		{"x11", strings.Replace(goodShow, "wayland", "x11", 1), false},
		{"greeter 服务", strings.Replace(goodShow, "lightdm-autologin", "lightdm-greeter", 1), false},
		{"用户名不符", strings.Replace(goodShow, "lancelrq", "other", 1), false},
	}
	for _, c := range cases {
		h := newHarness(t)
		h.show["c1"] = c.show
		h.pastGrace()
		h.tick(Interval)
		h.tick(Interval)
		got := h.restarts == 0
		if got != c.alive {
			t.Errorf("%s: 存活判定 = %v, 想要 %v", c.name, got, c.alive)
		}
	}
}

func TestNoSessionOrSocketDeadIsFailure(t *testing.T) {
	h := newHarness(t)
	h.sessions = "  c2 1001 other seat0 -\n" // 只有别的用户
	h.pastGrace()
	h.tick(Interval)
	if h.restarts != 1 {
		t.Fatalf("无会话应判失败: restarts=%d", h.restarts)
	}
	h2 := newHarness(t)
	h2.kill()
	h2.pastGrace()
	h2.tick(Interval)
	if h2.restarts != 1 {
		t.Fatalf("socket 连不上应判失败: restarts=%d", h2.restarts)
	}
}

func TestSecondSessionOfSameUserCounts(t *testing.T) {
	h := newHarness(t)
	h.sessions = "  c1 1000 lancelrq seat0 -\n  c2 1000 lancelrq - pts/0\n"
	h.show["c1"] = "Name=lancelrq\nType=tty\nService=sshd\nState=active\n"
	h.show["c2"] = goodShow
	h.pastGrace()
	h.tick(Interval)
	h.tick(Interval)
	if h.restarts != 0 {
		t.Fatalf("任一会话符合即存活")
	}
}

func TestTwoConsecutiveFailuresRequired(t *testing.T) {
	h := newHarness(t)
	h.pastGrace() // 健康
	h.kill()
	h.tick(Interval) // 失败 1
	if h.restarts != 0 {
		t.Fatalf("1 次失败不应重启")
	}
	h.dialErr = nil
	h.tick(Interval) // 恢复，计数清零
	h.kill()
	h.tick(Interval) // 又是失败 1
	if h.restarts != 0 {
		t.Fatalf("恢复后计数应清零")
	}
	h.tick(Interval) // 失败 2
	if h.restarts != 1 {
		t.Fatalf("连续 2 次失败应重启, got %d", h.restarts)
	}
}

func TestCooldownSkipsJudgement(t *testing.T) {
	h := newHarness(t)
	h.kill()
	h.pastGrace() // 失败 1
	h.tick(Interval)
	if h.restarts != 1 {
		t.Fatalf("first restart")
	}
	callsBefore := len(h.calls)
	for elapsed := time.Duration(0); elapsed+Interval < Cooldown; elapsed += Interval {
		h.tick(Interval)
	}
	if h.restarts != 1 || len(h.calls) != callsBefore {
		t.Fatalf("冷却期内不应判定: restarts=%d", h.restarts)
	}
	// 冷却结束后重新累计 2 次
	h.tick(Interval) // 到达冷却终点之后的第一次，失败 1
	h.tick(Interval) // 失败 2
	if h.restarts != 2 {
		t.Fatalf("冷却结束后应能再次重启, got %d", h.restarts)
	}
}

func TestHourlyCapAndSlidingWindow(t *testing.T) {
	h := newHarness(t)
	h.kill()
	h.pastGrace()
	h.tick(Interval) // 第 1 次重启
	// 以 6 分钟为步长制造第 2、3 次重启（每次 = 冷却过后 2 次失败）
	cycle := func() {
		h.tick(Cooldown) // 冷却刚好结束，失败 1
		h.tick(Interval) // 失败 2 → 重启
	}
	cycle()
	cycle()
	if h.restarts != 3 {
		t.Fatalf("应已重启 3 次, got %d", h.restarts)
	}
	cycle() // 第 4 次：1 小时内已满 3 次
	if h.restarts != 3 {
		t.Fatalf("1 小时内最多 3 次, got %d", h.restarts)
	}
	if !strings.Contains(h.log.String(), "上限") {
		t.Fatalf("超限应记日志:\n%s", h.log)
	}
	// 窗口滑出：最早一次重启距今超过 1 小时后又允许重启
	h.tick(Window)
	h.tick(Interval)
	if h.restarts != 4 {
		t.Fatalf("窗口滑出后应恢复重启, got %d", h.restarts)
	}
}

func TestRestartFailureStillCoolsDown(t *testing.T) {
	h := newHarness(t)
	h.kill()
	h.restartE = errors.New("boom")
	h.pastGrace()
	h.tick(Interval)
	h.tick(Interval)
	h.tick(Interval)
	if h.restarts != 1 {
		t.Fatalf("重启失败也应冷却，避免连环重试: %d", h.restarts)
	}
	if !strings.Contains(h.log.String(), "boom") {
		t.Fatalf("应记录重启错误")
	}
}

func TestLoginctlErrorIsInconclusive(t *testing.T) {
	h := newHarness(t)
	h.pastGrace()
	h.kill()
	h.tick(Interval) // 失败 1
	h.listErr = errors.New("logind 不可用")
	for i := 0; i < 5; i++ {
		h.tick(Interval)
	}
	if h.restarts != 0 {
		t.Fatalf("loginctl 自身出错不算失败")
	}
	h.listErr = nil
	h.tick(Interval) // 失败 2，计数不应被无结论的检查清零
	if h.restarts != 1 {
		t.Fatalf("无结论不清零计数, got %d", h.restarts)
	}
}

func TestConfigMismatchIdlesAndRereads(t *testing.T) {
	h := newHarness(t)
	h.files.set("/etc/lightdm/lightdm.conf", "[Seat:*]\nautologin-user=someoneelse\n")
	h.kill()
	h.pastGrace()
	for i := 0; i < 10; i++ {
		h.tick(Interval)
	}
	if h.restarts != 0 || len(h.calls) != 0 {
		t.Fatalf("配置不匹配时应空转: restarts=%d calls=%v", h.restarts, h.calls)
	}
	if !strings.Contains(h.log.String(), "autologin-user") {
		t.Fatalf("应记一条日志:\n%s", h.log)
	}
	// 10 分钟内不重读：改回配置也不立刻生效
	readsBefore := h.files.reads
	h.files.set("/etc/lightdm/lightdm.conf", "[Seat:*]\nautologin-user=lancelrq\n")
	h.tick(Interval)
	if h.files.reads != readsBefore {
		t.Fatalf("10 分钟内不应重读配置")
	}
	if h.restarts != 0 {
		t.Fatalf("尚未重读，不应生效")
	}
	// 越过重读间隔后生效并开始判定
	h.tick(ConfigRecheck)
	h.tick(Interval)
	if h.restarts != 1 {
		t.Fatalf("重读后应生效并重启, got %d", h.restarts)
	}
}

func TestConfigBecomesMismatchedResetsFailures(t *testing.T) {
	h := newHarness(t)
	h.pastGrace()
	h.kill()
	h.tick(Interval) // 失败 1
	h.files.set("/etc/lightdm/lightdm.conf", "[Seat:*]\nautologin-user=other\n")
	h.tick(ConfigRecheck) // 重读，停用
	h.files.set("/etc/lightdm/lightdm.conf", "[Seat:*]\nautologin-user=lancelrq\n")
	h.tick(ConfigRecheck) // 重读，重新生效，计数已清零
	if h.restarts != 0 {
		t.Fatalf("停用期间应清零计数")
	}
}

func TestConfigUnreadableTreatedAsDisabled(t *testing.T) {
	h := newHarness(t)
	delete(h.files.files, "/etc/lightdm/lightdm.conf")
	h.kill()
	h.pastGrace()
	h.tick(Interval)
	h.tick(Interval)
	if h.restarts != 0 {
		t.Fatalf("没有 autologin 配置不应重启 lightdm")
	}
}

func TestRunLoopStopsOnCancel(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.w.Run(ctx) }()
	for h.clk.Waiters() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}
