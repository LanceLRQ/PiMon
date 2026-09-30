//go:build !unix

package runtime

import "os/exec"

// 非 unix 平台不支持进程组，退化为只杀主进程。
func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
