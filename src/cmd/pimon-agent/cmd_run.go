package main

import (
	"context"
	"fmt"
	"log/slog"
	"syscall"

	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// cmdRun 运行守护进程：SIGINT/SIGTERM 优雅退出，SIGHUP 重新加载（重扫插件目录、重读配置）。
func (c *cli) cmdRun(args []string) int {
	fs, user := c.flagSet("run")
	level := fs.String("log-level", "info", "日志级别 debug|info|warn|error")
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
	if c.log == nil {
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
	errc := make(chan error, 1)
	go func() { errc <- d.Run(ctx) }()
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
			c.log.Info("收到退出信号，正在停止", "signal", s.String())
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
