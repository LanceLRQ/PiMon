package main

import (
	"fmt"
	"strconv"

	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
)

// cmdRepin 在 hub 证书重建后经守护进程更新钉扎指纹并重连。
func (c *cli) cmdRepin(args []string) int {
	fs, user := c.flagSet("repin")
	fp := fs.String("fp", "", "hub 的新 SPKI 指纹（SHA256:…，在 hub 网页的设备页查看）")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	if *fp == "" {
		_, _ = fmt.Fprintln(c.stderr, "错误: 缺少 --fp（hub 的新指纹，形如 SHA256:…）")
		return 2
	}
	pin, err := certs.ParseSPKI(*fp)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "错误: 指纹格式无效：%v\n", err)
		return 2
	}
	p, err := c.paths(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	ctx, cancel := ctlContext()
	defer cancel()
	if err := (ctl.Client{Path: p.CtlSock}).Repin(ctx, pin.String()); err != nil {
		return c.ctlFailed(err, p.CtlSock)
	}
	_, _ = fmt.Fprintf(c.stdout, "已把 hub 钉扎指纹更新为 %s，正在按新指纹重连；用 pimon-agent status 查看连接状态\n", pin.String())
	return 0
}

func itoa(n int) string { return strconv.Itoa(n) }
