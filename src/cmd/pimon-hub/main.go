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
	"github.com/LanceLRQ/PiMon/src/internal/hub/install"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugindev"
	"github.com/LanceLRQ/PiMon/src/internal/kiosk"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const usage = `用法: pimon-hub <命令> [参数]

命令:
  serve                     启动中枢服务
  setup-code [--if-needed]  生成新的首次设置码（尚未设置管理员时）；--if-needed 沿用仍有效的现有码，已有管理员时退出码为 3
  reset-password            从标准输入读取新密码并重置管理员密码
  restore [参数] <备份文件>  从备份包恢复（必须先停止服务）
  install [--desktop-user <用户>] [--kiosk]
                            在树莓派上一键部署 hub（需 root：sudo ./pimon-hub install）
  kiosk [参数]              屏幕守护进程：看护 Chromium kiosk（由 labwc autostart 以桌面用户启动）
  plugin <子命令>           插件开发者工具：validate 校验目录、run 本机运行一次（详见 plugin help）
  version                   显示版本号
  help                      显示本说明

参数（用于 serve、setup-code、reset-password、restore，须写在位置参数之前）:
  --addr <地址>        监听地址，默认 :31415（环境变量 PIMON_ADDR）
  --data-dir <目录>    数据目录，默认 /var/lib/pimon（环境变量 PIMON_DATA_DIR）
  --log-level <级别>   debug|info|warn|error，默认 info（环境变量 PIMON_LOG_LEVEL）

install 的参数:
  --desktop-user <用户>  桌面用户，加入 pimon 组以读取屏幕令牌（默认读取 lightdm 自动登录用户）
  --kiosk                同时配置桌面会话：labwc autostart 启动 kiosk、关闭系统息屏（删 swayidle 行，先备份）、安装透明鼠标指针（需要桌面用户）

kiosk 的参数:
  --hub <地址>         中枢网页地址，默认 http://127.0.0.1:31415
  --token-file <文件>  屏幕令牌文件，默认 /var/lib/pimon/screen.token
  --chromium <路径>    Chromium 可执行文件，默认 /usr/bin/chromium
  --log-level <级别>   debug|info|warn|error，默认 info
`

func main() {
	// 日志必须最早创建，mono 才从进程启动时刻算起。
	// 级别在解析配置后通过 LevelVar 调整，logger 本身不再重建。
	var lv slog.LevelVar
	slog.SetDefault(logging.NewWithRing(os.Stderr, &lv, logRing))
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, &lv))
}

// logRingSize 是内存日志缓冲保留的条数，系统页「最近日志」读取它。
const logRingSize = 500

// logRing 与默认日志同时创建，保证启动阶段的日志也进入缓冲。
var logRing = logging.NewRing(logRingSize)

// run 分派子命令并返回退出码：0 成功，1 命令失败，2 用法错误。
// lv 为日志级别变量，解析配置后设置；可为 nil（测试中不触碰全局日志）。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, lv *slog.LevelVar) int {
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
	case "install":
		sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := install.Command(sigCtx, rest, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "错误:", err)
			var ue install.UsageError
			if errors.As(err, &ue) {
				return 2
			}
			return 1
		}
		return 0
	case "kiosk":
		// 守护进程随图形会话常驻；SIGTERM/Ctrl-C 经 ctx 触发有序退出（先停 Chromium）。
		sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := kiosk.Command(sigCtx, rest, stderr, getenv); err != nil {
			_, _ = fmt.Fprintln(stderr, "错误:", err)
			var ue kiosk.UsageError
			if errors.As(err, &ue) {
				return 2
			}
			return 1
		}
		return 0
	case "plugin":
		if len(rest) > 0 && (rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help") {
			_, _ = fmt.Fprint(stdout, plugindev.Usage)
			return 0
		}
		// Ctrl-C 经 ctx 取消采集，执行器会杀掉插件进程组。
		sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := plugindev.Command(sigCtx, rest, stdout, stderr); err != nil {
			_, _ = fmt.Fprintln(stderr, "错误:", err)
			var ue plugindev.UsageError
			if errors.As(err, &ue) {
				return 2
			}
			return 1
		}
		return 0
	case "serve", "setup-code", "reset-password", "restore":
	default:
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n%s", cmd, usage)
		return 2
	}

	ifNeeded := false
	if cmd == "setup-code" {
		rest, ifNeeded = takeFlag(rest, "--if-needed")
	}
	cfg, pos, err := config.Parse(rest, getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "错误:", err)
		return 2
	}
	if lv != nil {
		lv.Set(cfg.SlogLevel())
	}

	if err := dispatch(context.Background(), cmd, ifNeeded, cfg, pos, stdin, stdout, stderr, getenv); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		_, _ = fmt.Fprintln(stderr, "错误:", err)
		var ue usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	return 0
}

// takeFlag 从参数里去掉布尔开关 name（遇到 -- 之后不再识别），返回剩余参数与是否出现过。
func takeFlag(args []string, name string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == name {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

// exitSetupCodeAdminExists 是 setup-code --if-needed 在已有管理员时的退出码，供安装脚本区分「无需设置码」与失败。
const exitSetupCodeAdminExists = 3

// exitError 携带需要原样返回的退出码；说明文字已由命令自己输出，run 不再追加「错误:」。
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("退出码 %d", e.code) }

type usageError string

func (e usageError) Error() string { return string(e) }

// checkRootRun 是「root 运行拒绝」的检查点，测试中替换以免依赖真实 root。
var checkRootRun = app.CheckRootRun

func dispatch(ctx context.Context, cmd string, ifNeeded bool, cfg config.Config, pos []string,
	stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	// serve 由 systemd 以服务用户运行，不做此检查；其余会写数据目录的子命令必须拒绝 root。
	if cmd != "serve" {
		if err := checkRootRun(cfg); err != nil {
			return err
		}
	}
	if cmd == "restore" {
		if len(pos) != 1 {
			return usageError("用法: pimon-hub restore [参数] <备份文件>")
		}
		return app.Restore(cfg, pos[0], stdout)
	}
	if cmd == "serve" {
		// 在打开数据库之前取数据目录锁，持有到进程结束，防止 restore 在服务运行时覆盖数据库。
		lk, err := app.LockDataDir(cfg)
		if err != nil {
			return err
		}
		defer func() { _ = lk.Release() }()
	}
	a, err := app.Open(ctx, cfg, app.WithGetenv(getenv), app.WithStderr(stderr), app.WithLogRing(logRing))
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	switch cmd {
	case "serve":
		// 只有 serve 接管信号；第一次信号后立即恢复默认处理，
		// 使优雅关闭期间再按一次 Ctrl-C 能强制退出。
		sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-sigCtx.Done()
			stop()
		}()
		return a.Serve(sigCtx)
	case "setup-code":
		gen := a.SetupCode
		if ifNeeded {
			gen = a.SetupCodeIfNeeded
		}
		code, exp, err := gen(ctx)
		if ifNeeded && errors.Is(err, app.ErrAdminExists) {
			_, _ = fmt.Fprintln(stdout, "已设置管理员，不需要设置码")
			return exitError{code: exitSetupCodeAdminExists}
		}
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
