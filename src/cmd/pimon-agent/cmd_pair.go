package main

import (
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
)

// cmdPair 让守护进程打开 15 分钟的拨出配对窗口并打印配对信息。配对码只出现在这次终端输出里。
func (c *cli) cmdPair(args []string) int {
	fs, user := c.flagSet("pair")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	p, err := c.paths(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	ctx, cancel := ctlContext()
	defer cancel()
	w, err := ctl.Client{Path: p.CtlSock}.Pair(ctx)
	if err != nil {
		return c.ctlFailed(err, p.CtlSock)
	}
	ips := strings.Join(w.IPs, "、")
	if ips == "" {
		ips = "（未找到局域网 IPv4 地址）"
	}
	out := c.stdout
	line(out, "配对窗口", "已打开，有效至 "+w.ExpiresAt.Local().Format("15:04:05")+"（配对成功一次即关闭）")
	line(out, "监听端口", itoa(w.Port))
	line(out, "本机 IP", ips)
	line(out, "SPKI 指纹", w.Fingerprint)
	line(out, "配对码", w.Code)
	line(out, "提示", "在 PiMon 网页添加拨出设备时，地址请填 VPS 的公网地址（端口 "+itoa(w.Port)+"），并填入上面的指纹与配对码；本机 IP 只是局域网地址")
	return 0
}
