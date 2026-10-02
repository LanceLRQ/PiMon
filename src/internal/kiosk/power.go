package kiosk

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// DefaultWlopmPath 是 wlopm 的默认路径。
const DefaultWlopmPath = "/usr/bin/wlopm"

// powerTimeout 是单次 wlopm 调用的上限。
const powerTimeout = 10 * time.Second

// CommandRunner 执行外部命令；测试用替身，生产用 ExecRunner。
type CommandRunner func(ctx context.Context, name string, args ...string) error

// ExecRunner 用 os/exec 直接执行命令（参数不经 shell），继承当前环境变量
// （wlopm 需要 WAYLAND_DISPLAY 与 XDG_RUNTIME_DIR）。
func ExecRunner(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// Power 按屏幕状态用 wlopm 开关显示器输出。
type Power struct {
	path string
	run  CommandRunner
	log  *slog.Logger

	mu   sync.Mutex
	last string // 上一次成功应用的状态（on/off）；空表示尚未应用
}

// NewPower 创建电源控制；path 为空时用 DefaultWlopmPath。
func NewPower(path string, run CommandRunner, log *slog.Logger) *Power {
	if path == "" {
		path = DefaultWlopmPath
	}
	return &Power{path: path, run: run, log: log}
}

// Apply 按屏幕状态 mode 执行 `wlopm --off '*'`（mode 为 off）或 `wlopm --on '*'`（其他）。
// 状态与上次成功应用的相同则不重复执行；命令失败只记日志，下次 Apply 会重试。
func (p *Power) Apply(mode string) {
	state, flag := "on", "--on"
	if mode == "off" {
		state, flag = "off", "--off"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == state {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), powerTimeout)
	defer cancel()
	if err := p.run(ctx, p.path, flag, "*"); err != nil {
		p.log.Error("wlopm 执行失败", "state", state, "err", err)
		return
	}
	p.last = state
	p.log.Info("显示器电源已切换", "state", state)
}

// Invalidate 让下一次 Apply 无条件执行一次（重新连上 hub 后调用）。
func (p *Power) Invalidate() {
	p.mu.Lock()
	p.last = ""
	p.mu.Unlock()
}
