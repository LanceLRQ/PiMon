package kiosk

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

const waitLimit = 5 * time.Second

// armClock 在假时钟之上记录每次 After 的时长，测试据此确认守护进程已挂好某个定时器再推进时间，
// 不依赖 sleep，也不受已过期的残留定时器干扰。
type armClock struct {
	*clock.Fake
	calls chan time.Duration
}

func newArmClock(start time.Time) *armClock {
	return &armClock{Fake: clock.NewFake(start), calls: make(chan time.Duration, 1024)}
}

func (c *armClock) After(d time.Duration) <-chan time.Time {
	ch := c.Fake.After(d)
	select {
	case c.calls <- d:
	default:
	}
	return ch
}

// waitArmed 阻塞到出现一次时长为 d 的 After 调用（此前未消费的其他调用被丢弃）。
func (c *armClock) waitArmed(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.After(waitLimit)
	for {
		select {
		case got := <-c.calls:
			if got == d {
				return
			}
		case <-deadline:
			t.Fatalf("等待定时器 %v 超时", d)
		}
	}
}

type fakeProc struct {
	args       []string
	done       chan struct{}
	once       sync.Once
	ignoreTerm bool
	terminated atomic.Bool
	killed     atomic.Bool
}

func (p *fakeProc) Done() <-chan struct{} { return p.done }
func (p *fakeProc) Terminate() {
	p.terminated.Store(true)
	if !p.ignoreTerm {
		p.exit()
	}
}
func (p *fakeProc) Kill() { p.killed.Store(true); p.exit() }
func (p *fakeProc) exit() { p.once.Do(func() { close(p.done) }) }

type fakeLauncher struct {
	started    chan *fakeProc
	ignoreTerm atomic.Bool
	failStart  atomic.Int32 // >0 时接下来的 Start 返回错误
	onStart    func()
	path       atomic.Value
}

func newFakeLauncher() *fakeLauncher { return &fakeLauncher{started: make(chan *fakeProc, 64)} }

func (l *fakeLauncher) Start(path string, args []string) (Process, error) {
	l.path.Store(path)
	if l.onStart != nil {
		l.onStart()
	}
	if l.failStart.Load() > 0 {
		l.failStart.Add(-1)
		return nil, errors.New("启动失败")
	}
	p := &fakeProc{args: slices.Clone(args), done: make(chan struct{}), ignoreTerm: l.ignoreTerm.Load()}
	l.started <- p
	return p, nil
}

func (l *fakeLauncher) next(t *testing.T) *fakeProc {
	t.Helper()
	select {
	case p := <-l.started:
		return p
	case <-time.After(waitLimit):
		t.Fatal("等待 Chromium 启动超时")
		return nil
	}
}

type fakeLink struct {
	sink    chan LinkSink
	reports chan model.KioskReport
}

func newFakeLink() *fakeLink {
	return &fakeLink{sink: make(chan LinkSink, 1), reports: make(chan model.KioskReport, 256)}
}

func (l *fakeLink) Run(ctx context.Context, sink LinkSink) {
	l.sink <- sink
	<-ctx.Done()
}

func (l *fakeLink) Report(r model.KioskReport) {
	select {
	case l.reports <- r:
	default:
	}
}

func (l *fakeLink) waitReport(t *testing.T, pred func(model.KioskReport) bool) model.KioskReport {
	t.Helper()
	deadline := time.After(waitLimit)
	for {
		select {
		case r := <-l.reports:
			if pred(r) {
				return r
			}
		case <-deadline:
			t.Fatal("等待上报超时")
			return model.KioskReport{}
		}
	}
}

type harness struct {
	t        *testing.T
	d        *Daemon
	clk      *armClock
	launcher *fakeLauncher
	link     *fakeLink
	cancel   context.CancelFunc
	done     chan error
	sink     LinkSink
	token    string
	dir      string
	probeErr atomic.Value // error 或 nil
	probes   atomic.Int32
	exec     chan execCall
	execErr  error
	cfg      Config
}

type execCall struct {
	path string
	argv []string
	env  []string
}

var testStart = time.Date(2026, 10, 2, 3, 0, 0, 0, time.FixedZone("CST", 8*3600))

func newHarness(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{t: t, clk: newArmClock(testStart), launcher: newFakeLauncher(), link: newFakeLink(),
		dir: dir, token: "tok-1", exec: make(chan execCall, 4)}
	h.probeErr.Store(errNil)
	h.cfg = Config{
		HubURL:       "http://127.0.0.1:31415",
		TokenPath:    filepath.Join(dir, "screen.token"),
		ChromiumPath: "/usr/bin/chromium",
		Paths:        Paths{LockPath: filepath.Join(dir, "state", "kiosk.lock"), ProfileDir: filepath.Join(dir, "state", "chrome-profile")},
		Clock:        h.clk,
		Launcher:     h.launcher,
		Link:         h.link,
		SessionProbe: func() error {
			h.probes.Add(1)
			if e := h.probeErr.Load().(error); e != errNil {
				return e
			}
			return nil
		},
		Exec: func(path string, argv, env []string) error {
			h.exec <- execCall{path, argv, env}
			return h.execErr
		},
		Executable: func() (string, error) { return filepath.Join(dir, "nonexistent-bin"), nil },
		Args:       []string{"pimon-hub", "kiosk"},
		Env:        []string{"A=1"},
		Version:    "v1",
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Grace:      5 * time.Second,
	}
	if mutate != nil {
		mutate(&h.cfg)
	}
	h.writeToken("tok-1")
	return h
}

var errNil = errors.New("nil")

func (h *harness) writeToken(v string) {
	h.t.Helper()
	if err := os.WriteFile(h.cfg.TokenPath, []byte(v+"\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) start() {
	h.t.Helper()
	d, err := New(h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.d = d
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- d.Run(ctx) }()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(waitLimit):
		}
	})
	select {
	case h.sink = <-h.link.sink:
	case <-time.After(waitLimit):
		h.t.Fatal("守护进程未启动链路")
	}
}

func (h *harness) waitExit() error {
	h.t.Helper()
	select {
	case err := <-h.done:
		h.done <- err // 放回，供 Cleanup 判断已退出
		return err
	case <-time.After(waitLimit):
		h.t.Fatal("等待守护进程退出超时")
		return nil
	}
}

func scaleSettings(scale float64) ui.ScreenSettings {
	return ui.ScreenSettings{Timezone: "Asia/Shanghai", Screen: model.ScreenDisplaySettings{UIScale: scale, DailyRestart: model.DailyRestartSettings{At: "04:00"}}}
}

func hasArg(args []string, want string) bool { return slices.Contains(args, want) }

func urlArg(p *fakeProc) string { return p.args[len(p.args)-1] }

func TestRun_启动参数与令牌URL(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p := h.launcher.next(t)
	if got := h.launcher.path.Load(); got != "/usr/bin/chromium" {
		t.Fatalf("path %v", got)
	}
	if urlArg(p) != "http://127.0.0.1:31415/screen/auth?token=tok-1" {
		t.Fatalf("url %q", urlArg(p))
	}
	if !hasArg(p.args, "--user-data-dir="+h.cfg.Paths.ProfileDir) || !hasArg(p.args, "--kiosk") {
		t.Fatalf("args %v", p.args)
	}
}

func TestRun_第二个实例拿不到锁且不清理Singleton(t *testing.T) {
	h := newHarness(t, nil)
	if err := os.MkdirAll(h.cfg.Paths.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	singleton := filepath.Join(h.cfg.Paths.ProfileDir, "SingletonCookie")
	if err := os.WriteFile(singleton, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(h.cfg.Paths.LockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := AcquireLock(h.cfg.Paths.LockPath)
	if err != nil {
		t.Fatal(err)
	}

	d, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Run(context.Background()); !errors.Is(err, ErrLocked) {
		t.Fatalf("应返回 ErrLocked，实际 %v", err)
	}
	if _, err := os.Stat(singleton); err != nil {
		t.Fatalf("拿不到锁时不得清理 Singleton: %v", err)
	}
	select {
	case <-h.launcher.started:
		t.Fatal("拿不到锁不得启动 Chromium")
	default:
	}

	// 释放后重新启动：先清理 Singleton 再启动 Chromium
	_ = held.Release()
	h.launcher.onStart = func() {
		if _, err := os.Stat(singleton); err == nil {
			t.Error("启动 Chromium 前应已清理 Singleton")
		}
	}
	h.start()
	h.launcher.next(t)
}

func TestRun_崩溃退避翻倍并在稳定运行后清零(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p := h.launcher.next(t)
	h.clk.waitArmed(t, 10*time.Second) // 轮询定时器在启动时挂好；之后 waitArmed 会丢弃它

	crash := func(p *fakeProc, wantDelay time.Duration) *fakeProc {
		t.Helper()
		before := h.clk.Now()
		p.exit()
		r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.BackoffUntil != nil })
		if got := r.BackoffUntil.Sub(before); got != wantDelay {
			t.Fatalf("退避期望 %v，实际 %v", wantDelay, got)
		}
		h.clk.waitArmed(t, wantDelay)
		h.clk.Advance(wantDelay)
		return h.launcher.next(t)
	}
	p = crash(p, 1*time.Second)
	p = crash(p, 2*time.Second)
	p = crash(p, 4*time.Second)

	// 运行满 5 分钟后再崩溃：退避清零，重新从 1s 开始
	h.link.waitReport(t, func(r model.KioskReport) bool {
		return r.Restarts == 3 && r.ChromiumStartedAt != nil && r.BackoffUntil == nil
	})
	h.clk.Advance(5 * time.Minute)
	p = crash(p, 1*time.Second)

	r := h.link.waitReport(t, func(r model.KioskReport) bool {
		return r.ChromiumStartedAt != nil && r.BackoffUntil == nil && r.Restarts == 4
	})
	if r.Version != "v1" {
		t.Fatalf("version %q", r.Version)
	}
	_ = p
}

func TestRun_退避封顶60秒(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p := h.launcher.next(t)
	var last time.Duration
	for i := 0; i < 9; i++ {
		before := h.clk.Now()
		p.exit()
		r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.BackoffUntil != nil })
		last = r.BackoffUntil.Sub(before)
		h.clk.waitArmed(t, last)
		h.clk.Advance(last)
		p = h.launcher.next(t)
	}
	if last != 60*time.Second {
		t.Fatalf("第 9 次退避应封顶 60s，实际 %v", last)
	}
}

func TestRun_启动失败按崩溃退避(t *testing.T) {
	h := newHarness(t, nil)
	h.launcher.failStart.Store(1)
	h.start()
	r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.BackoffUntil != nil })
	if r.ChromiumStartedAt != nil {
		t.Fatal("启动失败不应有启动时刻")
	}
	h.clk.waitArmed(t, time.Second)
	h.clk.Advance(time.Second)
	h.launcher.next(t)
}

func TestRun_令牌读失败不算崩溃且退避重试(t *testing.T) {
	h := newHarness(t, nil)
	if err := os.Remove(h.cfg.TokenPath); err != nil {
		t.Fatal(err)
	}
	h.start()
	r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.BackoffUntil != nil })
	if r.Restarts != 0 {
		t.Fatalf("令牌读失败不计重启: %d", r.Restarts)
	}
	select {
	case <-h.launcher.started:
		t.Fatal("没有令牌不得启动 Chromium")
	default:
	}
	// 空文件同样算失败：第二次退避翻倍
	h.clk.waitArmed(t, time.Second)
	if err := os.WriteFile(h.cfg.TokenPath, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	h.clk.waitArmed(t, 2*time.Second)
	h.writeToken("tok-2")
	h.clk.Advance(2 * time.Second)
	p := h.launcher.next(t)
	if !strings.HasSuffix(urlArg(p), "token=tok-2") {
		t.Fatalf("url %q", urlArg(p))
	}
	// 首次成功启动不算「重新拉起」
	r = h.link.waitReport(t, func(r model.KioskReport) bool { return r.ChromiumStartedAt != nil })
	if r.Restarts != 0 || r.BackoffUntil != nil {
		t.Fatalf("report %+v", r)
	}
}

func TestRun_令牌变化触发重启(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p1 := h.launcher.next(t)

	// 令牌未变：轮询不重启
	h.clk.waitArmed(t, 10*time.Second)
	h.clk.Advance(10 * time.Second)
	h.clk.waitArmed(t, 10*time.Second)
	if p1.terminated.Load() {
		t.Fatal("令牌未变不应重启")
	}

	h.writeToken("tok-2")
	h.clk.Advance(10 * time.Second)
	p2 := h.launcher.next(t)
	if !p1.terminated.Load() {
		t.Fatal("旧 Chromium 应被终止")
	}
	if !strings.HasSuffix(urlArg(p2), "token=tok-2") {
		t.Fatalf("url %q", urlArg(p2))
	}
	r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.Restarts == 1 && r.ChromiumStartedAt != nil })
	if r.BackoffUntil != nil {
		t.Fatal("主动重启不进入退避")
	}
}

func TestRun_令牌轮询读失败时保持运行(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p1 := h.launcher.next(t)
	h.clk.waitArmed(t, 10*time.Second)
	_ = os.Remove(h.cfg.TokenPath)
	h.clk.Advance(10 * time.Second)
	h.clk.waitArmed(t, 10*time.Second)
	if p1.terminated.Load() {
		t.Fatal("读令牌失败不应重启 Chromium")
	}
}

func TestRun_缩放变化触发重启(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	h.launcher.next(t)

	h.sink.OnSettings(scaleSettings(1.25))
	p2 := h.launcher.next(t)
	if !hasArg(p2.args, "--force-device-scale-factor=1.25") {
		t.Fatalf("args %v", p2.args)
	}
	// 同样的缩放不重启；再变化才重启，中间不应有多余的启动
	h.sink.OnSettings(scaleSettings(1.25))
	h.sink.OnSettings(scaleSettings(1.5))
	p3 := h.launcher.next(t)
	if !hasArg(p3.args, "--force-device-scale-factor=1.5") {
		t.Fatalf("args %v", p3.args)
	}
	if !p2.terminated.Load() {
		t.Fatal("旧 Chromium 应被终止")
	}
	// 缩放为 0 视为 1
	h.sink.OnSettings(scaleSettings(0))
	p4 := h.launcher.next(t)
	for _, a := range p4.args {
		if strings.HasPrefix(a, "--force-device-scale-factor") {
			t.Fatalf("缩放 1 不应带参数: %v", p4.args)
		}
	}
}

func TestRun_首次启动等待设置到达(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.SettingsWait = 3 * time.Second })
	h.start()
	h.clk.waitArmed(t, 3*time.Second)
	select {
	case <-h.launcher.started:
		t.Fatal("等待设置期间不应启动")
	default:
	}
	h.sink.OnSettings(scaleSettings(1.5))
	p := h.launcher.next(t)
	if !hasArg(p.args, "--force-device-scale-factor=1.5") {
		t.Fatalf("首次启动应已带缩放: %v", p.args)
	}
}

func TestRun_等待设置超时后照常启动(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.SettingsWait = 3 * time.Second })
	h.start()
	h.clk.waitArmed(t, 3*time.Second)
	h.clk.Advance(3 * time.Second)
	p := h.launcher.next(t)
	if hasArg(p.args, "--force-device-scale-factor=1.5") {
		t.Fatal("没有设置时按缩放 1")
	}
}

func dailySettings(enabled bool, at string) ui.ScreenSettings {
	s := scaleSettings(1)
	s.Screen.DailyRestart = model.DailyRestartSettings{Enabled: enabled, At: at}
	return s
}

func TestRun_每日重启按设置时区调度(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p1 := h.launcher.next(t)

	// 03:00 +08，每日 04:00（Asia/Shanghai）→ 1 小时后
	h.sink.OnSettings(dailySettings(true, "04:00"))
	h.clk.waitArmed(t, time.Hour)
	h.clk.Advance(time.Hour)
	p2 := h.launcher.next(t)
	if !p1.terminated.Load() {
		t.Fatal("每日重启应终止旧 Chromium")
	}
	// 重启后重新调度到次日同一时刻
	h.clk.waitArmed(t, 24*time.Hour)
	h.clk.Advance(24 * time.Hour)
	p3 := h.launcher.next(t)
	if !p2.terminated.Load() {
		t.Fatal("次日应再次重启")
	}
	_ = p3
	r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.Restarts == 2 && r.ChromiumStartedAt != nil })
	_ = r
}

func TestRun_每日重启改时刻后重新调度且关闭后不调度(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	h.launcher.next(t)

	h.sink.OnSettings(dailySettings(true, "04:00"))
	h.clk.waitArmed(t, time.Hour)
	h.sink.OnSettings(dailySettings(true, "05:00"))
	h.clk.waitArmed(t, 2*time.Hour)
	// 04:00 的旧定时器到期不应触发重启
	h.clk.Advance(time.Hour)
	h.clk.Advance(time.Hour)
	p2 := h.launcher.next(t) // 只有 05:00 那次
	// 关闭后不再调度：之后的下一个启动只能来自缩放变化
	h.sink.OnSettings(dailySettings(false, "05:00"))
	h.sink.OnSettings(scaleSettings(1.25))
	p3 := h.launcher.next(t)
	if !hasArg(p3.args, "--force-device-scale-factor=1.25") || !p2.terminated.Load() {
		t.Fatalf("args %v", p3.args)
	}
	h.clk.Advance(48 * time.Hour)
	h.sink.OnSettings(scaleSettings(1.5))
	p4 := h.launcher.next(t)
	if !hasArg(p4.args, "--force-device-scale-factor=1.5") {
		t.Fatalf("关闭每日重启后 48 小时内不应有重启，下一个启动应来自缩放: %v", p4.args)
	}
}

func TestRun_Build不同时停Chromium并exec自身(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p := h.launcher.next(t)

	h.sink.OnBuild("v1") // 相同：不 exec
	h.sink.OnBuild("")   // 空：不 exec
	h.sink.OnBuild("v2")
	select {
	case c := <-h.exec:
		if c.path != filepath.Join(h.dir, "nonexistent-bin") || !slices.Equal(c.argv, []string{"pimon-hub", "kiosk"}) || !slices.Equal(c.env, []string{"A=1"}) {
			t.Fatalf("exec %+v", c)
		}
	case <-time.After(waitLimit):
		t.Fatal("应调用 exec")
	}
	if !p.terminated.Load() {
		t.Fatal("exec 前应先停止 Chromium")
	}
	if err := h.waitExit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.exec:
		t.Fatal("exec 只应被调用一次")
	default:
	}
}

func TestRun_exec失败后继续看护且同一Build不重试(t *testing.T) {
	h := newHarness(t, nil)
	h.execErr = errors.New("exec 失败")
	h.start()
	h.launcher.next(t)

	h.sink.OnBuild("v2")
	<-h.exec
	h.launcher.next(t) // 停止后重新拉起
	h.sink.OnBuild("v2")
	h.sink.OnSettings(scaleSettings(1.25)) // 顺序哨兵
	p := h.launcher.next(t)
	if !hasArg(p.args, "--force-device-scale-factor=1.25") {
		t.Fatalf("args %v", p.args)
	}
	select {
	case <-h.exec:
		t.Fatal("同一 Build 不应重复 exec")
	default:
	}
	h.sink.OnBuild("v3")
	select {
	case <-h.exec:
	case <-time.After(waitLimit):
		t.Fatal("新的 Build 应再次尝试")
	}
}

func TestRun_会话连续两次探测失败后退出(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p := h.launcher.next(t)

	step := func() {
		h.clk.waitArmed(t, 10*time.Second)
		n := h.probes.Load()
		h.clk.Advance(10 * time.Second)
		deadline := time.Now().Add(waitLimit)
		for h.probes.Load() == n { // 等这一轮探测实际执行完再改下一轮的结果
			if time.Now().After(deadline) {
				t.Fatal("等待探测超时")
			}
			runtime.Gosched()
		}
	}
	// 失败、成功、失败：计数被成功清零，仍在运行
	h.probeErr.Store(errors.New("断开"))
	step()
	h.probeErr.Store(errNil)
	step()
	h.probeErr.Store(errors.New("断开"))
	step()
	h.clk.waitArmed(t, 10*time.Second)
	if p.terminated.Load() {
		t.Fatal("非连续失败不应退出")
	}
	// 再失败一次：连续 2 次
	h.clk.Advance(10 * time.Second)
	if err := h.waitExit(); err != nil {
		t.Fatalf("会话结束应正常退出: %v", err)
	}
	if !p.terminated.Load() {
		t.Fatal("退出前应停止 Chromium")
	}
}

func TestRun_SIGTERM无响应时5秒后强杀(t *testing.T) {
	h := newHarness(t, nil)
	h.launcher.ignoreTerm.Store(true)
	h.start()
	p := h.launcher.next(t)
	h.cancel()
	h.clk.waitArmed(t, 5*time.Second)
	if p.killed.Load() {
		t.Fatal("宽限期内不应强杀")
	}
	h.clk.Advance(5 * time.Second)
	if err := h.waitExit(); err != nil {
		t.Fatal(err)
	}
	if !p.terminated.Load() || !p.killed.Load() {
		t.Fatalf("terminated=%v killed=%v", p.terminated.Load(), p.killed.Load())
	}
}

func TestRun_取消上下文时停止Chromium并释放锁(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	p := h.launcher.next(t)
	h.cancel()
	if err := h.waitExit(); err != nil {
		t.Fatal(err)
	}
	if !p.terminated.Load() {
		t.Fatal("退出时应停止 Chromium")
	}
	lk, err := AcquireLock(h.cfg.Paths.LockPath)
	if err != nil {
		t.Fatalf("退出后锁应已释放: %v", err)
	}
	_ = lk.Release()
}

func TestRun_连上链路时上报当前状态并合并扩展字段(t *testing.T) {
	touch := true
	h := newHarness(t, func(c *Config) {
		c.FillReport = func(r *model.KioskReport) { r.Touchscreen = &touch }
	})
	h.start()
	h.launcher.next(t)
	h.sink.OnConnected()
	r := h.link.waitReport(t, func(r model.KioskReport) bool { return r.ChromiumStartedAt != nil && r.Touchscreen != nil })
	if !*r.Touchscreen || r.Version != "v1" || r.Restarts != 0 {
		t.Fatalf("report %+v", r)
	}
}

func TestRun_链路读令牌使用同一令牌文件(t *testing.T) {
	h := newHarness(t, nil)
	h.start()
	tok, err := h.sink.ReadToken()
	if err != nil || tok != "tok-1" {
		t.Fatalf("tok %q err %v", tok, err)
	}
	_ = os.Remove(h.cfg.TokenPath)
	if _, err := h.sink.ReadToken(); err == nil {
		t.Fatal("令牌缺失应返回错误")
	}
}

func TestNew_缺少必要依赖报错(t *testing.T) {
	cfg := newHarness(t, nil).cfg
	cfg.Launcher = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("缺少 Launcher 应报错")
	}
}
