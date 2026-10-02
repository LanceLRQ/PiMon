package kiosk

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// resolveExecutable 返回用于 exec 自身的路径。二进制被 rename 覆盖升级后，
// Linux 的 /proc/self/exe 会带 " (deleted)" 后缀，此时取去掉后缀的路径（即新二进制）；再解析符号链接。
func resolveExecutable(executable func() (string, error)) (string, error) {
	p, err := executable()
	if err != nil {
		return "", err
	}
	if trimmed, ok := strings.CutSuffix(p, " (deleted)"); ok {
		if _, err := os.Stat(trimmed); err == nil {
			p = trimmed
		}
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// syscallExec 用新进程映像替换当前进程；成功时不返回。
func syscallExec(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
