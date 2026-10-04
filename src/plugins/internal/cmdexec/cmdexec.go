// Package cmdexec 给内置插件提供可注入的外部命令执行器：
// 带 stdout 上限、stderr 尾部截取、超时杀整个进程组，环境固定 LC_ALL=C。
//
// 注意：超时杀进程组对经 sudo 提权的子进程是尽力而为——非 root 的 agent 向 root 属主的进程
// 发 SIGKILL 会 EPERM，root 子进程可能残留。调用方应只运行有界的只读命令，必要时让命令自带超时。
package cmdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// MaxStdoutBytes 是 stdout 上限（1 MiB），超出返回 error。
	MaxStdoutBytes = 1 << 20
	// MaxStderrBytes 是 stderr 保留的尾部字节数（16 KiB）。
	MaxStderrBytes = 16 << 10
	// waitDelay 是进程被杀后等待 stdio 管道关闭的宽限。
	waitDelay = 2 * time.Second
)

// ErrOutputTooLarge 表示命令 stdout 超过上限。
var ErrOutputTooLarge = errors.New("命令 stdout 超过上限")

// ErrSignaled 表示命令被信号终止（ctx 未结束）。此时没有真实退出码，Run 返回 error，
// 以便调用方区分「退出码位」与「被信号终止」。
var ErrSignaled = errors.New("命令被信号终止")

// Result 是一次命令执行的结果。
type Result struct {
	Stdout   []byte
	Stderr   string // 仅保留尾部
	ExitCode int
}

// Runner 执行外部命令。非 0 退出不算 error（照样返回 stdout 与退出码），
// 只有启动失败、ctx 结束（超时或取消）、stdout 超限、被信号终止（ErrSignaled）才返回 error；
// ExitCode 只保存真实退出码。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

type system struct{}

// New 返回真实执行器。
func New() Runner { return system{} }

func (system) Run(ctx context.Context, name string, args ...string) (Result, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = waitDelay
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	stdout := &limitWriter{max: MaxStdoutBytes, onOverflow: cancel}
	stderr := &tailBuffer{max: MaxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("启动 %s 失败: %w", name, err)
	}
	waitErr := cmd.Wait()
	// 进程已退出：清理可能残留的同组孙进程。
	_ = killProcessGroup(cmd)

	switch {
	case stdout.isOverflowed():
		return Result{}, fmt.Errorf("%s: %w（%d 字节）", name, ErrOutputTooLarge, MaxStdoutBytes)
	case ctx.Err() != nil:
		return Result{}, ctx.Err()
	}
	res := Result{Stdout: stdout.buf.Bytes(), Stderr: strings.ToValidUTF8(stderr.String(), "")}
	if waitErr != nil {
		var ee *exec.ExitError
		if !errors.As(waitErr, &ee) {
			return Result{}, fmt.Errorf("运行 %s 失败: %w", name, waitErr)
		}
		if ee.ExitCode() < 0 {
			return Result{}, fmt.Errorf("%s: %w（%s）", name, ErrSignaled, ee.String())
		}
		res.ExitCode = ee.ExitCode()
	}
	return res, nil
}

// limitWriter 累积 stdout，超过上限时标记溢出并触发 onOverflow（用于杀进程组）。
type limitWriter struct {
	mu         sync.Mutex
	max        int
	buf        bytes.Buffer
	overflowed bool
	onOverflow func()
}

func (w *limitWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.overflowed {
		return 0, ErrOutputTooLarge
	}
	if w.buf.Len()+len(p) > w.max {
		w.overflowed = true
		w.onOverflow()
		return 0, ErrOutputTooLarge
	}
	return w.buf.Write(p)
}

func (w *limitWriter) isOverflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}

// tailBuffer 只保留最近写入的 max 字节。并发安全。
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if n >= b.max {
		b.buf = append(b.buf[:0], p[n-b.max:]...)
		return n, nil
	}
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		copy(b.buf, b.buf[over:])
		b.buf = b.buf[:b.max]
	}
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
