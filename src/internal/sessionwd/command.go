package sessionwd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	osuser "os/user"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	loginctlPath  = "/usr/bin/loginctl"
	systemctlPath = "/usr/bin/systemctl"
	loginctlLimit = 10 * time.Second
	restartLimit  = 2 * time.Minute
	dialLimit     = 3 * time.Second
)

// UserNamePattern 限制桌面用户名的字符集，install 写进 unit 的 ExecStart 前也用它校验。
var UserNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`)

// UsageError 表示命令行参数错误。
type UsageError string

func (e UsageError) Error() string { return string(e) }

// Command 是 `pimon-hub session-watchdog` 的入口：解析参数并用真实系统依赖运行到 ctx 结束。
// 日志写到 stderr（由 systemd 收进 journal）。
func Command(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("session-watchdog", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	userName := fs.String("user", "", "桌面用户（必填）")
	levelName := fs.String("log-level", "info", "日志级别 debug|info|warn|error")
	if err := fs.Parse(args); err != nil {
		return UsageError(err.Error())
	}
	if fs.NArg() > 0 {
		return UsageError(fmt.Sprintf("session-watchdog 不接受位置参数: %v", fs.Args()))
	}
	if *userName == "" {
		return UsageError("session-watchdog 需要 --user <桌面用户>")
	}
	if !UserNamePattern.MatchString(*userName) {
		return UsageError(fmt.Sprintf("无效的 --user %q", *userName))
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*levelName)); err != nil {
		return UsageError(fmt.Sprintf("无效的 --log-level %q", *levelName))
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("session-watchdog 仅支持 Linux（当前为 %s）", runtime.GOOS)
	}
	w := New(Config{User: *userName}, realDeps(logging.New(stderr, level)))
	err := w.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func realDeps(log *slog.Logger) Deps {
	return Deps{
		Clock: clock.Real{},
		Log:   log,
		Files: osFiles{},
		Loginctl: func(ctx context.Context, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, loginctlLimit)
			defer cancel()
			out, err := exec.CommandContext(ctx, loginctlPath, args...).Output()
			return string(out), err
		},
		Restart: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, restartLimit)
			defer cancel()
			if out, err := exec.CommandContext(ctx, systemctlPath, "restart", "lightdm").CombinedOutput(); err != nil {
				return fmt.Errorf("systemctl restart lightdm: %w: %s", err, out)
			}
			return nil
		},
		Dial: func(path string) error {
			c, err := net.DialTimeout("unix", path, dialLimit)
			if err != nil {
				return err
			}
			return c.Close()
		},
		LookupUID: func(name string) (int, error) {
			u, err := osuser.Lookup(name)
			if err != nil {
				return 0, err
			}
			return strconv.Atoi(u.Uid)
		},
	}
}

// osFiles 用真实文件系统满足 lightdm.Files。
type osFiles struct{}

func (osFiles) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func (osFiles) ReadDir(name string) ([]string, error) {
	es, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out, nil
}
