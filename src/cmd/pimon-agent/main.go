// pimon-agent 是 PiMon 的被监控机器代理入口。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	goruntime "runtime"
	"syscall"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon"
	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
	"github.com/LanceLRQ/PiMon/src/internal/agent/upgrade"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const usage = `用法: pimon-agent <命令> [参数]

命令:
  install    安装并启动 agent 服务（拨入：--hub --fp --code [--name]；拨出：--listen；
             可选 --user、--docker-group、--re-pair）
  pair       拨出模式下开启 15 分钟配对窗口并打印配对信息
  run        守护进程（由服务管理器调用；--log-level debug|info|warn|error）
  status     显示模式、连接状态、已分配实例数、版本等
  repin      hub 证书重建后更新钉扎指纹（--fp SHA256:…）
  upgrade    升级 agent（拨入从 hub；拨出用 --release vX.Y.Z [--proxy URL]）
  uninstall  停止并删除服务（--purge 同时删除配置与数据）
  version    显示版本号

install、uninstall、pair、run、status、repin、upgrade 在 Linux 上加 --user 表示用户模式（macOS 总是用户模式）。
`

// ctlTimeout 是 CLI 经控制套接字调用一次的总超时（略大于服务端的操作上限）。
const ctlTimeout = ctl.OpTimeout + 2*ctl.IOTimeout

func main() {
	// 日志最早创建并设为默认（只在 main 里这样做，组件一律注入）；级别由 run --log-level 经 LevelVar 调整。
	var lv slog.LevelVar
	ring := logging.NewRing(daemon.RingCapacity)
	logger := logging.NewWithRing(os.Stderr, &lv, ring)
	slog.SetDefault(logger)
	c := newCLI(os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	c.lv, c.ring, c.log = &lv, ring, logger
	os.Exit(c.run(os.Args[1:]))
}

// run 分派子命令并返回退出码：0 成功，1 命令失败，2 用法错误。测试入口，不触碰全局日志。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	return newCLI(stdin, stdout, stderr, getenv).run(args)
}

// cli 是一次命令行调用的依赖。
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	goos           string
	now            func() time.Time

	// lv、ring、log 是守护进程的日志；为空时 run 命令自建（写 stderr）。
	lv   *slog.LevelVar
	ring *logging.Ring
	log  *slog.Logger
	// signals 返回守护进程要处理的信号通道与停止函数；为空用 signal.Notify（测试注入通道）。
	signals func() (<-chan os.Signal, func())
	// tweak 在创建守护进程前调整其配置（测试用）。
	tweak func(*daemon.Config)
	// runDaemon 运行守护进程，为空用 (*daemon.Daemon).Run（测试注入关停卡住的场景）。
	runDaemon func(ctx context.Context, d *daemon.Daemon) error
	// doInstall、doUninstall 执行安装与卸载（测试替换为记录参数，不触碰真实系统）。
	doInstall   func(ctx context.Context, d install.Deps, o install.Options) error
	doUninstall func(ctx context.Context, d install.Deps, o install.UninstallOptions) error
	// doUpgrade 执行升级（测试替换为记录参数，不下载、不替换二进制）。
	doUpgrade func(ctx context.Context, d upgrade.Deps, o upgrade.Options) error
}

func newCLI(stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) *cli {
	return &cli{stdin: stdin, stdout: stdout, stderr: stderr, getenv: getenv, goos: goruntime.GOOS, now: time.Now,
		doInstall: install.Install, doUninstall: install.Uninstall, doUpgrade: upgrade.Run}
}

func (c *cli) run(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(c.stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(c.stdout, version.Version)
		return 0
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(c.stdout, usage)
		return 0
	case "run":
		return c.cmdRun(rest)
	case "status":
		return c.cmdStatus(rest)
	case "pair":
		return c.cmdPair(rest)
	case "repin":
		return c.cmdRepin(rest)
	case "install":
		return c.cmdInstall(rest)
	case "uninstall":
		return c.cmdUninstall(rest)
	case "upgrade":
		return c.cmdUpgrade(rest)
	default:
		_, _ = fmt.Fprintf(c.stderr, "未知命令 %q\n\n%s", cmd, usage)
		return 2
	}
}

// flagSet 创建子命令的参数集，带公共的 --user。
func (c *cli) flagSet(name string) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet("pimon-agent "+name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	user := fs.Bool("user", false, "Linux 上使用用户模式（macOS 总是用户模式）")
	return fs, user
}

// parse 解析参数；失败或有多余参数时返回退出码与 false（-h 为 0）。
func (c *cli) parse(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(c.stderr, "错误: 多余的参数 %q\n", fs.Args())
		return 2, false
	}
	return 0, true
}

// paths 解析本机的运行路径（系统模式或用户模式）。
func (c *cli) paths(user bool) (config.Paths, error) {
	return config.Resolve(config.Env{GOOS: c.goos, EUID: os.Geteuid(), Home: c.getenv("HOME"), Getenv: c.getenv, User: user})
}

// fail 打印错误并返回退出码 1。
func (c *cli) fail(format string, a ...any) int {
	_, _ = fmt.Fprintf(c.stderr, "错误: "+format+"\n", a...)
	return 1
}

// ctlFailed 把控制套接字调用的错误转成中文提示，返回退出码 1。
func (c *cli) ctlFailed(err error, sock string) int {
	switch {
	case errors.Is(err, ctl.ErrNotRunning):
		return c.fail("守护进程未运行（控制套接字 %s 不存在或无人监听）。请先启动 agent 服务，或用 pimon-agent run 在前台运行", sock)
	case errors.Is(err, ctl.ErrPermission):
		return c.fail("没有权限访问控制套接字 %s，请用 sudo 执行", sock)
	}
	return c.fail("%v", err)
}

// ctlContext 返回一次控制调用的上下文。
func ctlContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), ctlTimeout)
}

// notifySignals 订阅守护进程处理的信号。
func notifySignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}
