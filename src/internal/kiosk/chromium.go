package kiosk

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

// ChromiumArgs 拼出 Chromium 启动参数：M0 验证过的参数全集 + profile 目录，
// 缩放不为 1 时追加 --force-device-scale-factor，目标地址放在最后。
func ChromiumArgs(profileDir, url string, scale float64) []string {
	args := []string{
		"--kiosk",
		"--ozone-platform=wayland",
		"--noerrdialogs",
		"--disable-infobars",
		"--no-first-run",
		"--disable-features=Translate",
		"--password-store=basic",
		"--hide-crash-restore-bubble",
		"--check-for-update-interval=31536000",
		"--user-data-dir=" + profileDir,
	}
	if scale > 0 && scale != 1 {
		args = append(args, "--force-device-scale-factor="+strconv.FormatFloat(scale, 'f', -1, 64))
	}
	return append(args, url)
}

// Process 是一个被看护的 Chromium 进程（含其进程组）。
type Process interface {
	// Done 在进程退出后关闭。
	Done() <-chan struct{}
	// Terminate 向进程组发 SIGTERM。
	Terminate()
	// Kill 向进程组发 SIGKILL。
	Kill()
}

// Launcher 启动 Chromium；测试用替身，生产用 ExecLauncher。
type Launcher interface {
	Start(path string, args []string) (Process, error)
}

// ExecLauncher 用 os/exec 启动进程：独立进程组（Linux 上父进程退出时收到 SIGTERM），
// stdin/stdout/stderr 全部接 /dev/null。
type ExecLauncher struct{}

// Start 启动 path 并在后台回收进程。
func (ExecLauncher) Start(path string, args []string) (Process, error) {
	cmd := exec.Command(path, args...) // 未设置的 Stdin/Stdout/Stderr 即 /dev/null
	cmd.SysProcAttr = launchAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 %s: %w", path, err)
	}
	p := &execProcess{pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		// 组长退出后进程组里可能还残留子进程（渲染、GPU 等），补一次强杀清干净。
		_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		close(p.done)
	}()
	return p, nil
}

type execProcess struct {
	pid  int
	done chan struct{}
}

func (p *execProcess) Done() <-chan struct{} { return p.done }

// Pid 返回组长进程号，同时也是进程组号。
func (p *execProcess) Pid() int { return p.pid }

// Setpgid 之后进程组号等于 pid，向 -pid 发信号覆盖整个进程组。
func (p *execProcess) Terminate() { _ = syscall.Kill(-p.pid, syscall.SIGTERM) }
func (p *execProcess) Kill()      { _ = syscall.Kill(-p.pid, syscall.SIGKILL) }
