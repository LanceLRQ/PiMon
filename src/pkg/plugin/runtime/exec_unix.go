//go:build unix

package runtime

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让子进程成为新进程组的组长，便于整组清理。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 向整个进程组发 SIGKILL（包括孙进程）。
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
