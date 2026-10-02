package kiosk

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
)

// UsageError 表示命令行参数错误。
type UsageError string

func (e UsageError) Error() string { return string(e) }

// Command 是 `pimon-hub kiosk` 的入口：解析参数、校验图形会话环境、启动守护进程并阻塞到结束。
// 日志写到 stderr（由 autostart 经 systemd-cat 送进 journal），每条带单调时钟。
func Command(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("kiosk", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	hub := fs.String("hub", "http://127.0.0.1:31415", "hub 网页地址")
	tokenFile := fs.String("token-file", "/var/lib/pimon/screen.token", "屏幕令牌文件")
	chromium := fs.String("chromium", "/usr/bin/chromium", "Chromium 可执行文件")
	levelName := fs.String("log-level", "info", "日志级别 debug|info|warn|error")
	if err := fs.Parse(args); err != nil {
		return UsageError(err.Error())
	}
	if fs.NArg() > 0 {
		return UsageError(fmt.Sprintf("kiosk 不接受位置参数: %v", fs.Args()))
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*levelName)); err != nil {
		return UsageError(fmt.Sprintf("无效的 --log-level %q", *levelName))
	}

	sock, err := SessionSocketPath(getenv)
	if err != nil {
		return err
	}
	paths, err := DefaultPaths(getenv)
	if err != nil {
		return err
	}
	cfg := Config{
		HubURL:       *hub,
		TokenPath:    *tokenFile,
		ChromiumPath: *chromium,
		Paths:        paths,
		Launcher:     ExecLauncher{},
		SessionProbe: SocketProbe(sock),
		Log:          logging.New(stderr, level),
	}
	bind, err := attachHubLink(&cfg, DefaultPeripherals(getenv))
	if err != nil {
		return UsageError(err.Error())
	}
	d, err := New(cfg)
	if err != nil {
		return err
	}
	bind(d)
	err = d.Run(ctx)
	if errors.Is(err, ErrLocked) {
		return fmt.Errorf("%w（锁文件 %s）", err, paths.LockPath)
	}
	return err
}
