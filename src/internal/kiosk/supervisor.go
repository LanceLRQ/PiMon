package kiosk

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

type settingsEvent struct {
	settings ui.ScreenSettings
}

// Daemon 是 kiosk 守护进程。所有状态变更都在 Run 的事件循环里串行完成。
type Daemon struct {
	cfg Config
	log *slog.Logger
	clk clock.Clock

	settingsCh chan settingsEvent
	buildCh    chan string
	quit       chan struct{}

	// mu 保护上报用的字段（循环 goroutine 写，任意 goroutine 读）。
	mu           sync.Mutex
	startedAt    time.Time // 零值表示 Chromium 未在运行
	pid          int       // 运行中的 Chromium 组长 pid；未运行为 0
	restarts     int
	backoffUntil time.Time // 零值表示不在退避中

	// 以下字段只在循环 goroutine 访问。
	proc           Process
	procDone       <-chan struct{}
	launchedBefore bool
	crash          backoff
	tokenWait      backoff
	tokenSum       string // 当前 Chromium 启动时所用令牌的哈希
	retryCh        <-chan time.Time
	pollCh         <-chan time.Time
	firstWaitCh    <-chan time.Time
	dailyCh        <-chan time.Time
	dailyTarget    time.Time
	pendingFirst   bool
	scale          float64
	daily          model.DailyRestartSettings
	tz             string
	sessionFails   int
	upgradeFailed  string
}

// Run 取锁、清理 Singleton、启动 Chromium 并看护，直到 ctx 结束、图形会话失效或 hub 升级触发 exec。
// 拿不到锁返回 ErrLocked。
func (d *Daemon) Run(ctx context.Context) error {
	lk, err := AcquireLock(d.cfg.Paths.LockPath)
	if err != nil {
		return err
	}
	defer func() { _ = lk.Release() }()
	if err := CleanSingleton(lk, d.cfg.Paths.ProfileDir); err != nil {
		return err
	}

	linkCtx, cancelLink := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.cfg.Link.Run(linkCtx, linkSink{d})
	}()
	for _, svc := range d.cfg.Services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc(linkCtx)
		}()
	}
	if d.cfg.ReportInterval > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.reportLoop(linkCtx)
		}()
	}
	defer func() {
		close(d.quit) // 先放开可能阻塞在回调里的链路 goroutine
		cancelLink()
		wg.Wait()
	}()

	return d.loop(ctx)
}

func (d *Daemon) loop(ctx context.Context) error {
	d.pollCh = d.clk.After(pollInterval)
	if d.cfg.SettingsWait > 0 {
		d.pendingFirst = true
		d.firstWaitCh = d.clk.After(d.cfg.SettingsWait)
	} else {
		d.launch()
	}
	for {
		select {
		case <-ctx.Done():
			d.stop()
			return nil
		case <-d.procDone:
			d.onExit()
		case <-d.retryCh:
			d.retryCh = nil
			d.launch()
		case <-d.firstWaitCh:
			d.firstWaitCh = nil
			d.pendingFirst = false
			d.launch()
		case <-d.pollCh:
			d.pollCh = d.clk.After(pollInterval)
			if d.poll() {
				return nil
			}
		case <-d.dailyCh:
			d.onDaily()
		case ev := <-d.settingsCh:
			d.applySettings(ev.settings)
		case b := <-d.buildCh:
			if d.onBuild(b) {
				return nil
			}
		}
	}
}

// launch 重读令牌并启动 Chromium；令牌读失败或启动失败都进入退避等待，稍后重试。
func (d *Daemon) launch() {
	tok, err := readToken(d.cfg.TokenPath)
	if err != nil {
		// 令牌缺失多半是 hub 尚未就绪，不是 Chromium 崩溃：单独退避，不计重启。
		delay := d.tokenWait.next()
		d.log.Warn("读取屏幕令牌失败，稍后重试", "err", err, "delay", delay)
		d.waitRetry(delay)
		return
	}
	d.tokenWait.reset()

	target := d.cfg.HubURL + "/screen/auth?token=" + url.QueryEscape(tok)
	args := ChromiumArgs(d.cfg.Paths.ProfileDir, target, d.scale)
	p, err := d.cfg.Launcher.Start(d.cfg.ChromiumPath, args)
	if err != nil {
		delay := d.crash.next()
		d.log.Error("启动 Chromium 失败，稍后重试", "err", err, "delay", delay)
		d.waitRetry(delay)
		return
	}
	d.proc, d.procDone = p, p.Done()
	d.tokenSum = tokenHash(tok)
	d.mu.Lock()
	d.startedAt = d.clk.Now()
	d.pid = 0
	if pp, ok := p.(interface{ Pid() int }); ok {
		d.pid = pp.Pid()
	}
	d.backoffUntil = time.Time{}
	if d.launchedBefore {
		d.restarts++
	}
	d.mu.Unlock()
	d.launchedBefore = true
	d.log.Info("Chromium 已启动", "scale", d.scale)
	d.report()
}

// waitRetry 进入等待：delay 之后重试启动，并把退避截止时刻上报给 hub。
func (d *Daemon) waitRetry(delay time.Duration) {
	d.retryCh = d.clk.After(delay)
	d.mu.Lock()
	d.startedAt, d.pid = time.Time{}, 0
	d.backoffUntil = d.clk.Now().Add(delay)
	d.mu.Unlock()
	d.report()
}

// onExit 处理 Chromium 非预期退出：按退避重新拉起。
func (d *Daemon) onExit() {
	d.mu.Lock()
	ran := d.clk.Now().Sub(d.startedAt)
	d.mu.Unlock()
	d.proc, d.procDone = nil, nil
	if ran >= stableRun {
		d.crash.reset()
	}
	delay := d.crash.next()
	d.log.Warn("Chromium 退出，稍后重新拉起", "ran", ran, "delay", delay)
	d.waitRetry(delay)
}

// stop 终止 Chromium 进程组：SIGTERM，宽限期后 SIGKILL，并等它退出。
func (d *Daemon) stop() {
	if d.proc == nil {
		return
	}
	p, done := d.proc, d.procDone
	p.Terminate()
	select {
	case <-done:
	case <-d.clk.After(d.cfg.Grace):
		d.log.Warn("Chromium 未在宽限期内退出，强制结束")
		p.Kill()
		<-done
	}
	d.proc, d.procDone = nil, nil
	d.mu.Lock()
	d.startedAt, d.pid = time.Time{}, 0
	d.mu.Unlock()
}

// restart 主动重启运行中的 Chromium（不进入退避）；未在运行时无操作，下次启动自然带上新状态。
func (d *Daemon) restart(reason string) {
	if d.proc == nil {
		return
	}
	d.log.Info("重启 Chromium", "reason", reason)
	d.stop()
	d.launch()
}

// poll 做会话存活与令牌变化检查；返回 true 表示守护进程应退出。
func (d *Daemon) poll() bool {
	if d.cfg.SessionProbe != nil {
		if err := d.cfg.SessionProbe(); err != nil {
			d.sessionFails++
			d.log.Warn("图形会话探测失败", "err", err, "fails", d.sessionFails)
			if d.sessionFails >= sessionMaxFails {
				d.log.Info("图形会话已结束，守护进程退出")
				d.stop()
				return true
			}
		} else {
			d.sessionFails = 0
		}
	}
	if d.proc != nil {
		tok, err := readToken(d.cfg.TokenPath)
		switch {
		case err != nil:
			d.log.Debug("轮询令牌失败", "err", err)
		case tokenHash(tok) != d.tokenSum:
			d.restart("屏幕令牌变化")
		}
	}
	return false
}

func (d *Daemon) applySettings(s ui.ScreenSettings) {
	scale := s.Screen.UIScale
	if scale <= 0 {
		scale = 1
	}
	scaleChanged := scale != d.scale
	dailyChanged := s.Screen.DailyRestart != d.daily || s.Timezone != d.tz
	d.scale, d.daily, d.tz = scale, s.Screen.DailyRestart, s.Timezone
	if dailyChanged {
		d.scheduleDaily()
	}
	if d.pendingFirst {
		d.pendingFirst = false
		d.firstWaitCh = nil
		d.launch()
		return
	}
	if scaleChanged {
		d.restart("界面缩放变化")
	}
}

// scheduleDaily 按当前设置重新挂每日重启定时器；未开启则取消。
func (d *Daemon) scheduleDaily() {
	d.dailyCh = nil
	if !d.daily.Enabled {
		return
	}
	loc, err := loadLocation(d.tz)
	if err != nil {
		d.log.Warn("设置时区无法解析，改用本地时区", "tz", d.tz, "err", err)
	}
	now := d.clk.Now()
	next, err := nextDailyRestart(now, loc, d.daily.At)
	if err != nil {
		d.log.Warn("每日重启时刻无效，不调度", "at", d.daily.At, "err", err)
		return
	}
	d.dailyTarget = next
	d.dailyCh = d.clk.After(next.Sub(now))
}

func (d *Daemon) onDaily() {
	// 计时器到期后重新核对墙钟：不足则补等，避免时钟校正后提前重启。
	if now := d.clk.Now(); now.Before(d.dailyTarget) {
		d.dailyCh = d.clk.After(d.dailyTarget.Sub(now))
		return
	}
	d.scheduleDaily()
	d.restart("每日定时重启")
}

// onBuild 处理 hub 的构建版本；与自身不同即停 Chromium 并 exec 换新二进制。
// 返回 true 表示 exec 已完成（真实环境不会返回）。
func (d *Daemon) onBuild(build string) bool {
	if build == "" || build == d.cfg.Version || build == d.upgradeFailed {
		return false
	}
	d.log.Info("hub 已升级，换用新二进制", "hub", build, "self", d.cfg.Version)
	hadProc := d.proc != nil
	d.stop()
	path, err := resolveExecutable(d.cfg.Executable)
	if err == nil {
		err = d.cfg.Exec(path, d.cfg.Args, d.cfg.Env)
	}
	if err != nil {
		d.upgradeFailed = build
		d.log.Error("exec 新二进制失败，继续运行当前版本", "err", err)
		if hadProc {
			d.launch()
		}
		return false
	}
	return true
}

// ChromiumPID 返回运行中的 Chromium 组长进程号（同时是进程组号）；未运行为 0。
func (d *Daemon) ChromiumPID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pid
}

// ReportNow 立即上报一次当前状态；可从任意 goroutine 调用，不阻塞。
func (d *Daemon) ReportNow() { d.report() }

// reportLoop 按 ReportInterval 周期上报，直到 ctx 结束。
func (d *Daemon) reportLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.clk.After(d.cfg.ReportInterval):
			d.report()
		}
	}
}

// report 组装并上报当前状态；可从任意 goroutine 调用，Link.Report 不得阻塞。
func (d *Daemon) report() {
	d.mu.Lock()
	r := model.KioskReport{Version: d.cfg.Version, Restarts: d.restarts}
	if !d.startedAt.IsZero() {
		t := d.startedAt
		r.ChromiumStartedAt = &t
	}
	if !d.backoffUntil.IsZero() {
		t := d.backoffUntil
		r.BackoffUntil = &t
	}
	d.mu.Unlock()
	if d.cfg.FillReport != nil {
		d.cfg.FillReport(&r)
	}
	d.cfg.Link.Report(r)
}

// linkSink 把链路事件送进守护进程的事件循环。
type linkSink struct{ d *Daemon }

func (s linkSink) OnConnected() { s.d.report() }

func (s linkSink) OnSettings(v ui.ScreenSettings) {
	select {
	case s.d.settingsCh <- settingsEvent{v}:
	case <-s.d.quit:
	}
}

func (s linkSink) OnBuild(b string) {
	select {
	case s.d.buildCh <- b:
	case <-s.d.quit:
	}
}

func (s linkSink) ReadToken() (string, error) { return readToken(s.d.cfg.TokenPath) }
