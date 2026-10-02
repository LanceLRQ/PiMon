package kiosk

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/labwc"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	// GreeterAutostartPath 与 SystemAutostartPath 是系统级 labwc autostart。
	GreeterAutostartPath = "/etc/xdg/labwc-greeter/autostart"
	SystemAutostartPath  = "/etc/xdg/labwc/autostart"
	idleCheckInterval    = time.Hour
)

// UserAutostartPath 返回用户 labwc autostart 路径：$XDG_CONFIG_HOME/labwc/autostart，
// 缺省 ~/.config/labwc/autostart；两者都没有时返回空串。
func UserAutostartPath(getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "labwc", "autostart")
}

// IdleConfig 是息屏检查的依赖；路径为空的一项视为未命中。
type IdleConfig struct {
	UserPath    string
	GreeterPath string
	SystemPath  string
	// ProcRoot 是 /proc 目录，默认 /proc。
	ProcRoot string
	Clock    clock.Clock
	Log      *slog.Logger
	// OnChange 在每次检查完成后调用，用于立即上报。
	OnChange func()
}

// IdleChecker 检查系统是否可能自行息屏：三处 labwc autostart 里的 swayidle 行与 swayidle 进程。
type IdleChecker struct {
	cfg IdleConfig

	mu   sync.Mutex
	last *model.KioskIdleCheck
}

// NewIdleChecker 创建息屏检查并补默认值。
func NewIdleChecker(cfg IdleConfig) *IdleChecker {
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = "/proc"
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &IdleChecker{cfg: cfg}
}

// Check 执行一次检查并记为最近结果。
func (c *IdleChecker) Check() model.KioskIdleCheck {
	res := model.KioskIdleCheck{
		User:            c.fileHasSwayidle(c.cfg.UserPath),
		Greeter:         c.fileHasSwayidle(c.cfg.GreeterPath),
		System:          c.fileHasSwayidle(c.cfg.SystemPath),
		SwayidleRunning: swayidleRunning(c.cfg.ProcRoot),
		CheckedAt:       c.cfg.Clock.Now(),
	}
	c.mu.Lock()
	cp := res
	c.last = &cp
	c.mu.Unlock()
	return res
}

// Last 返回最近一次检查结果的副本；尚未检查为 nil。
func (c *IdleChecker) Last() *model.KioskIdleCheck {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		return nil
	}
	cp := *c.last
	return &cp
}

// Run 启动时检查一次，之后每小时一次，直到 ctx 结束。
func (c *IdleChecker) Run(ctx context.Context) {
	for {
		r := c.Check()
		if r.User || r.Greeter || r.System || r.SwayidleRunning {
			c.cfg.Log.Warn("检测到系统息屏配置", "user", r.User, "greeter", r.Greeter, "system", r.System, "running", r.SwayidleRunning)
		}
		if c.cfg.OnChange != nil {
			c.cfg.OnChange()
		}
		select {
		case <-ctx.Done():
			return
		case <-c.cfg.Clock.After(idleCheckInterval):
		}
	}
}

func (c *IdleChecker) fileHasSwayidle(path string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return labwc.HasSwayidle(string(data))
}

// swayidleRunning 扫描 <proc>/<pid>/comm，有 swayidle 进程即为 true。
func swayidleRunning(procRoot string) bool {
	matches, err := filepath.Glob(filepath.Join(procRoot, "[0-9]*", "comm"))
	if err != nil {
		return false
	}
	for _, m := range matches {
		if data, err := os.ReadFile(m); err == nil && strings.TrimSpace(string(data)) == "swayidle" {
			return true
		}
	}
	return false
}
