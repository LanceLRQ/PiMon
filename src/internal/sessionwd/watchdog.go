// Package sessionwd 实现会话级看门狗：周期检查桌面用户的 labwc 图形会话是否存活，
// 连续失败时重启 lightdm 让系统重新自动登录（M0 F6：会话死掉后屏幕会停在 greeter）。
// 所有外部依赖（loginctl、systemctl、socket、文件、时钟、uid 查询）都经 Deps 注入。
package sessionwd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/lightdm"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	// Interval 是两次检查之间的间隔。
	Interval = 30 * time.Second
	// Grace 是进程启动后不判失败的宽限：开机自动登录要 30–40 秒，留足余量。
	Grace = 90 * time.Second
	// ConfigRecheck 是重读 lightdm 配置的间隔。
	ConfigRecheck = 10 * time.Minute
	// FailThreshold 是触发重启所需的连续失败次数。
	FailThreshold = 2
	// Cooldown 是重启之后不判定的时长：M0 实测 restart lightdm 约 35 秒回到页面。
	Cooldown = 5 * time.Minute
	// MaxRestarts 是 Window 内最多执行的重启次数。
	MaxRestarts = 3
	// Window 是重启次数限制的滑动窗口。
	Window = time.Hour

	wantType    = "wayland"
	wantService = "lightdm-autologin"
)

// Config 是看门狗的参数。
type Config struct {
	User string // 桌面用户
}

// Deps 汇集外部依赖，测试里整体替换。
type Deps struct {
	Clock clock.Clock
	Log   *slog.Logger
	Files lightdm.Files
	// Loginctl 执行 loginctl 并返回标准输出。
	Loginctl func(ctx context.Context, args ...string) (string, error)
	// Restart 执行 systemctl restart lightdm。
	Restart func(ctx context.Context) error
	// Dial 尝试 unix connect 到给定 socket 路径，成功返回 nil。
	Dial func(path string) error
	// LookupUID 返回用户的 uid。
	LookupUID func(user string) (int, error)
}

// Watchdog 保存状态机；Step 与 Run 不可并发调用。
type Watchdog struct {
	cfg Config
	d   Deps

	start         time.Time
	configChecked bool
	nextConfig    time.Time
	enabled       bool
	failures      int
	cooldownUntil time.Time
	restarts      []time.Time // 窗口内的重启时刻
	capLogged     bool
}

// New 创建看门狗；启动宽限从此刻开始计。
func New(cfg Config, d Deps) *Watchdog {
	return &Watchdog{cfg: cfg, d: d, start: d.Clock.Now()}
}

// Run 每个间隔执行一次 Step，直到 ctx 结束。
func (w *Watchdog) Run(ctx context.Context) error {
	w.d.Log.Info("会话看门狗启动", "user", w.cfg.User, "interval", Interval, "grace", Grace)
	for {
		w.Step(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.d.Clock.After(Interval):
		}
	}
}

// Step 执行一次检查周期。
func (w *Watchdog) Step(ctx context.Context) {
	now := w.d.Clock.Now()
	w.refreshConfig(now)
	if !w.enabled {
		return
	}
	if now.Before(w.start.Add(Grace)) || now.Before(w.cooldownUntil) {
		return
	}
	alive, ok := w.check(ctx)
	switch {
	case !ok:
		return // 无结论：既不计失败也不清零
	case alive:
		if w.failures > 0 {
			w.d.Log.Info("图形会话恢复正常", "failures", w.failures)
		}
		w.failures = 0
		w.capLogged = false
		return
	}
	w.failures++
	w.d.Log.Warn("图形会话检查失败", "failures", w.failures, "threshold", FailThreshold)
	if w.failures < FailThreshold {
		return
	}
	w.maybeRestart(ctx, now)
}

// refreshConfig 按需重读 lightdm 配置；autologin-user 不等于目标用户时停用并清零计数。
func (w *Watchdog) refreshConfig(now time.Time) {
	if w.configChecked && now.Before(w.nextConfig) {
		return
	}
	w.configChecked = true
	w.nextConfig = now.Add(ConfigRecheck)
	got, err := lightdm.AutologinUser(w.d.Files)
	enabled := err == nil && got == w.cfg.User
	switch {
	case err != nil:
		w.d.Log.Warn("读取 lightdm 配置失败，看门狗空转", "err", err)
	case !enabled:
		w.d.Log.Info("lightdm 的 autologin-user 不是目标用户，看门狗空转", "autologin-user", got, "user", w.cfg.User, "recheck", ConfigRecheck)
	case !w.enabled:
		w.d.Log.Info("lightdm 自动登录用户与目标一致，看门狗生效", "user", w.cfg.User)
	}
	if !enabled {
		w.failures = 0
		w.capLogged = false
	}
	w.enabled = enabled
}

func (w *Watchdog) maybeRestart(ctx context.Context, now time.Time) {
	kept := w.restarts[:0]
	for _, t := range w.restarts {
		if now.Sub(t) < Window {
			kept = append(kept, t)
		}
	}
	w.restarts = kept
	if len(w.restarts) >= MaxRestarts {
		if !w.capLogged {
			w.d.Log.Error("已达重启次数上限，只记日志不再重启 lightdm，直到窗口滑出", "max", MaxRestarts, "window", Window)
			w.capLogged = true
		}
		return
	}
	w.d.Log.Error("图形会话已失效，重启 lightdm 重新自动登录", "user", w.cfg.User)
	w.restarts = append(w.restarts, now)
	w.cooldownUntil = now.Add(Cooldown)
	w.failures = 0
	w.capLogged = false
	if err := w.d.Restart(ctx); err != nil {
		w.d.Log.Error("重启 lightdm 失败", "err", err)
	}
}

// check 返回会话是否存活；ok=false 表示检查本身出错、没有结论。
func (w *Watchdog) check(ctx context.Context) (alive, ok bool) {
	uid, err := w.d.LookupUID(w.cfg.User)
	if err != nil {
		w.d.Log.Warn("查询桌面用户 uid 失败", "err", err)
		return false, false
	}
	out, err := w.d.Loginctl(ctx, "list-sessions", "--no-legend")
	if err != nil {
		w.d.Log.Warn("loginctl list-sessions 失败，本次检查无结论", "err", err)
		return false, false
	}
	found, showFailed := false, false
	for _, id := range sessionIDsOf(out, w.cfg.User) {
		show, err := w.d.Loginctl(ctx, "show-session", id, "-p", "Name", "-p", "Type", "-p", "Service", "-p", "State")
		if err != nil {
			w.d.Log.Warn("loginctl show-session 失败", "session", id, "err", err)
			showFailed = true
			continue
		}
		if matchSession(show, w.cfg.User) {
			found = true
			break
		}
	}
	if !found && showFailed {
		w.d.Log.Warn("部分会话 show-session 失败且无匹配会话，本次检查无结论", "user", w.cfg.User)
		return false, false
	}
	if !found {
		w.d.Log.Warn("没有找到符合条件的图形会话", "user", w.cfg.User)
		return false, true
	}
	sock := fmt.Sprintf("/run/user/%d/wayland-0", uid)
	if err := w.d.Dial(sock); err != nil {
		w.d.Log.Warn("Wayland socket 连接失败", "socket", sock, "err", err)
		return false, true
	}
	return true, true
}

// sessionIDsOf 从 `loginctl list-sessions --no-legend` 输出（SESSION UID USER SEAT TTY）里取出属于 user 的会话 ID。
func sessionIDsOf(out, user string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[2] == user {
			ids = append(ids, f[0])
		}
	}
	return ids
}

// matchSession 判断 show-session 的 KEY=VALUE 输出是否是目标图形会话。
func matchSession(show, user string) bool {
	kv := map[string]string{}
	for _, line := range strings.Split(show, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	state := kv["State"]
	return kv["Name"] == user && kv["Type"] == wantType && kv["Service"] == wantService && (state == "active" || state == "online")
}
