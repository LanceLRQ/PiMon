package kiosk

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const (
	// pollInterval 是令牌哈希与会话存活的轮询周期。
	pollInterval = 10 * time.Second
	// sessionMaxFails 是会话探测连续失败多少次后退出。
	sessionMaxFails = 2
	// defaultGrace 是 SIGTERM 之后等多久再 SIGKILL。
	defaultGrace = 5 * time.Second
)

// Paths 是 kiosk 的本地状态路径。
type Paths struct {
	// LockPath 是单实例锁文件。
	LockPath string
	// ProfileDir 是 Chromium 的 user-data-dir。
	ProfileDir string
}

// DefaultPaths 按 ${XDG_STATE_HOME:-~/.local/state}/pimon/kiosk 推算路径。
func DefaultPaths(getenv func(string) string) (Paths, error) {
	state := getenv("XDG_STATE_HOME")
	if state == "" {
		home := getenv("HOME")
		if home == "" {
			return Paths{}, errors.New("无法确定状态目录：XDG_STATE_HOME 与 HOME 均未设置")
		}
		state = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(state, "pimon", "kiosk")
	return Paths{LockPath: filepath.Join(dir, "kiosk.lock"), ProfileDir: filepath.Join(dir, "chrome-profile")}, nil
}

// Config 是守护进程的全部依赖；时钟、Chromium 启动器、会话探测、exec、与 hub 的连接都可注入。
type Config struct {
	// HubURL 是 hub 的网页地址，如 http://127.0.0.1:31415。
	HubURL string
	// TokenPath 是屏幕令牌文件。
	TokenPath string
	// ChromiumPath 是 Chromium 可执行文件。
	ChromiumPath string
	Paths        Paths

	Clock    clock.Clock
	Launcher Launcher
	Link     HubLink
	// SessionProbe 探测图形会话是否存活；nil 表示不探测。
	SessionProbe func() error
	// Exec 用新进程映像替换当前进程，成功时不返回。默认 syscall.Exec。
	Exec func(path string, argv, env []string) error
	// Executable 返回当前可执行文件路径，默认 os.Executable。
	Executable func() (string, error)
	// Args、Env 是 exec 自身时传递的参数与环境，默认 os.Args、os.Environ()。
	Args []string
	Env  []string
	// Version 是自身构建版本，默认 version.Version；与 hub snapshot 的 build 不同即视为 hub 已升级。
	Version string
	Log     *slog.Logger
	// Grace 是 SIGTERM 之后等待多久强杀，默认 5s。
	Grace time.Duration
	// SettingsWait 是首次启动 Chromium 前最多等多久首份设置（用于一次就带上缩放参数）；0 表示不等。
	SettingsWait time.Duration
	// Services 是随守护进程启停的后台服务（触摸监听、息屏检查等）：持锁后启动，
	// ctx 在守护进程退出时取消，Run 等它们全部返回后才返回。
	Services []func(ctx context.Context)
	// ReportInterval 大于 0 时按此周期重复上报当前状态。
	ReportInterval time.Duration
	// FillReport 在每次上报前被调用，用于合并守护核心之外的字段
	// （touchscreen、chromium_rss_bytes、idle_check）；可为 nil。会被多个 goroutine 并发调用，必须并发安全。
	FillReport func(*model.KioskReport)
}

// New 校验配置并补默认值。
func New(cfg Config) (*Daemon, error) {
	switch {
	case cfg.HubURL == "":
		return nil, errors.New("缺少 HubURL")
	case cfg.TokenPath == "":
		return nil, errors.New("缺少 TokenPath")
	case cfg.ChromiumPath == "":
		return nil, errors.New("缺少 ChromiumPath")
	case cfg.Paths.LockPath == "" || cfg.Paths.ProfileDir == "":
		return nil, errors.New("缺少 Paths")
	case cfg.Launcher == nil:
		return nil, errors.New("缺少 Launcher")
	case cfg.Link == nil:
		return nil, errors.New("缺少 Link")
	}
	cfg.HubURL = strings.TrimRight(cfg.HubURL, "/")
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Exec == nil {
		cfg.Exec = syscallExec
	}
	if cfg.Executable == nil {
		cfg.Executable = os.Executable
	}
	if cfg.Args == nil {
		cfg.Args = os.Args
	}
	envDefaulted := cfg.Env == nil
	if envDefaulted {
		cfg.Env = os.Environ()
	}
	execedFor, env := splitExecForBuild(cfg.Env)
	cfg.Env = env
	if envDefaulted && execedFor != "" {
		_ = os.Unsetenv(ExecForBuildEnv)
	}
	if cfg.Version == "" {
		cfg.Version = version.Version
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Grace <= 0 {
		cfg.Grace = defaultGrace
	}
	return &Daemon{
		cfg:        cfg,
		log:        cfg.Log,
		clk:        cfg.Clock,
		settingsCh: make(chan settingsEvent),
		buildCh:    make(chan string),
		quit:       make(chan struct{}),
		scale:      1,
		execedFor:  execedFor,
	}, nil
}
