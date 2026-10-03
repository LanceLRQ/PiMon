package main

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
	"github.com/LanceLRQ/PiMon/src/internal/agent/upgrade"
)

// cmdUpgrade 升级 agent：拨入模式从 hub 升级；--release <tag> [--proxy <URL>] 从 GitHub Releases 升级（拨出模式必须指定）。
func (c *cli) cmdUpgrade(args []string) int {
	fs, user := c.flagSet("upgrade")
	var o upgrade.Options
	fs.StringVar(&o.Release, "release", "", "从 GitHub Releases 升级到该版本（vX.Y.Z）；拨出模式必须指定")
	fs.StringVar(&o.Proxy, "proxy", "", "下载 Release 用的代理（http/https/socks5/socks5h://[用户:密码@]主机:端口）")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	p, err := c.paths(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = c.doUpgrade(ctx, upgrade.NewDeps(c.stdout, c.goos, p), o)
	var ue install.UsageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		_, _ = fmt.Fprintln(c.stderr, "错误:", ue)
		return 2
	}
	return c.ctlFailed(err, p.CtlSock)
}
