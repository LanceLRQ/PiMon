package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon"
	"github.com/LanceLRQ/PiMon/src/internal/agent/logfile"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// cmdRun 运行守护进程：SIGINT/SIGTERM 优雅退出（第二次强制退出，退出码 1），SIGHUP 重新加载（重扫插件目录、重读配置）。
func (c *cli) cmdRun(args []string) int {
	fs, user := c.flagSet("run")
	level := fs.String("log-level", "info", "日志级别 debug|info|warn|error")
	logFile := fs.String("log-file", "", "日志写入该文件（超过 5 MiB 轮转为 .1，只保留一个旧文件；macOS LaunchAgent 使用），默认写标准错误")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	lvl, err := parseLevel(*level)
	if err != nil {
		_, _ = fmt.Fprintln(c.stderr, "错误:", err)
		return 2
	}
	if c.lv == nil {
		c.lv = new(slog.LevelVar)
	}
	if c.ring == nil {
		c.ring = logging.NewRing(daemon.RingCapacity)
	}
	switch {
	case *logFile != "":
		if err := os.MkdirAll(filepath.Dir(*logFile), 0o700); err != nil {
			return c.fail("创建日志目录: %v", err)
		}
		w, err := logfile.Open(*logFile, logfile.DefaultMaxBytes)
		if err != nil {
			return c.fail("打开日志文件 %s: %v", *logFile, err)
		}
		defer func() { _ = w.Close() }()
		c.log = logging.NewWithRing(w, c.lv, c.ring)
	case c.log == nil:
		c.log = logging.NewWithRing(c.stderr, c.lv, c.ring)
	}
	c.lv.Set(lvl)
	p, err := c.paths(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	cfg := daemon.Config{Paths: p, Log: c.log, Ring: c.ring, Builtins: runtime.Builtins(), CtlSock: p.CtlSock}
	if c.tweak != nil {
		c.tweak(&cfg)
	}
	d := daemon.New(cfg)

	signals := c.signals
	if signals == nil {
		signals = notifySignals
	}
	sig, stopSig := signals()
	defer stopSig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDaemon := c.runDaemon
	if runDaemon == nil {
		runDaemon = func(ctx context.Context, d *daemon.Daemon) error { return d.Run(ctx) }
	}
	errc := make(chan error, 1)
	go func() { errc <- runDaemon(ctx, d) }()
	stopping := false
	c.log.Info("agent 守护进程启动", "config", p.Config, "ctl", p.CtlSock)
	for {
		select {
		case s := <-sig:
			if s == syscall.SIGHUP {
				c.log.Info("收到 SIGHUP，重新加载插件目录与配置")
				if err := d.Reload(ctx); err != nil {
					c.log.Warn("重新加载失败", "err", err)
				}
				continue
			}
			if stopping { // 关停卡住时第二次信号强制退出
				c.log.Warn("再次收到退出信号，强制退出", "signal", s.String())
				return 1
			}
			stopping = true
			c.log.Info("收到退出信号，正在停止（再次发送将强制退出）", "signal", s.String())
			cancel()
		case err := <-errc:
			if err != nil {
				return c.fail("%v", err)
			}
			return 0
		}
	}
}

// parseLevel 解析 --log-level。
func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("非法日志级别 %q，可选 debug|info|warn|error", s)
}
