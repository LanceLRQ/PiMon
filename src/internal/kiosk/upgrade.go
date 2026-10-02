package kiosk

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ExecForBuildEnv 是升级 exec 时传给新进程的环境变量，值为目标 build；新进程据此判断自己是不是升级后的产物。
const ExecForBuildEnv = "PIMON_KIOSK_EXEC_FOR_BUILD"

// splitExecForBuild 从 env 中取出 ExecForBuildEnv 的值，返回值和去掉该变量后的环境。
func splitExecForBuild(env []string) (value string, rest []string) {
	prefix := ExecForBuildEnv + "="
	rest = make([]string, 0, len(env))
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			value = v
			continue
		}
		rest = append(rest, kv)
	}
	return value, rest
}

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
