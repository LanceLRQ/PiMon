// pimon-hub 是 PiMon 的中枢服务入口。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // 内嵌时区库，精简系统上也能解析 IANA 时区

	"golang.org/x/term"

	"github.com/LanceLRQ/PiMon/src/internal/hub/app"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const usage = `用法: pimon-hub <命令> [参数]

命令:
  serve                     启动中枢服务
  setup-code                生成新的首次设置码（尚未设置管理员时）
  reset-password            从标准输入读取新密码并重置管理员密码
  restore [参数] <备份文件>  从备份包恢复（必须先停止服务）
  version                   显示版本号
  help                      显示本说明

参数（用于 serve、setup-code、reset-password、restore，须写在位置参数之前）:
  --addr <地址>        监听地址，默认 :31415（环境变量 PIMON_ADDR）
  --data-dir <目录>    数据目录，默认 /var/lib/pimon（环境变量 PIMON_DATA_DIR）
  --log-level <级别>   debug|info|warn|error，默认 info（环境变量 PIMON_LOG_LEVEL）
`

func main() {
	// 日志必须最早创建，mono 才从进程启动时刻算起。
	slog.SetDefault(logging.New(os.Stderr, slog.LevelInfo))
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run 分派子命令并返回退出码：0 成功，1 命令失败，2 用法错误。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(stdout, version.Version)
		return 0
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "serve", "setup-code", "reset-password", "restore":
	default:
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n%s", cmd, usage)
		return 2
	}

	cfg, pos, err := config.Parse(rest, getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "错误:", err)
		return 2
	}
	slog.SetDefault(logging.New(stderr, cfg.SlogLevel()))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := dispatch(ctx, cmd, cfg, pos, stdin, stdout, stderr, getenv); err != nil {
		_, _ = fmt.Fprintln(stderr, "错误:", err)
		var ue usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	return 0
}

type usageError string

func (e usageError) Error() string { return string(e) }

func dispatch(ctx context.Context, cmd string, cfg config.Config, pos []string,
	stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	if cmd == "restore" {
		if len(pos) != 1 {
			return usageError("用法: pimon-hub restore [参数] <备份文件>")
		}
		return app.Restore(cfg, pos[0], stdout)
	}
	a, err := app.Open(ctx, cfg, app.WithGetenv(getenv), app.WithStderr(stderr))
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	switch cmd {
	case "serve":
		return a.Serve(ctx)
	case "setup-code":
		code, exp, err := a.SetupCode(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "首次设置码: %s\n有效期至: %s\n", code, exp.Local().Format(time.DateTime))
		return nil
	default: // reset-password
		isTerm := false
		if f, ok := stdin.(*os.File); ok {
			isTerm = term.IsTerminal(int(f.Fd()))
		}
		return a.ResetPassword(ctx, stdin, stdout, isTerm)
	}
}
