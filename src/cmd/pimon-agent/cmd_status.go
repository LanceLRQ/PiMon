package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
)

// cmdStatus 经控制套接字打印守护进程状态；守护进程未运行时改读配置文件并提示（退出码 1）。
// 任何情况下都不输出凭据、凭据哈希与配对码。
func (c *cli) cmdStatus(args []string) int {
	fs, user := c.flagSet("status")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	p, err := c.paths(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	ctx, cancel := ctlContext()
	defer cancel()
	st, err := ctl.Client{Path: p.CtlSock}.Status(ctx)
	switch {
	case err == nil:
		c.printStatus(st)
		return 0
	case errors.Is(err, ctl.ErrNotRunning):
		return c.statusOffline(p)
	}
	return c.ctlFailed(err, p.CtlSock)
}

// statusOffline 在守护进程未运行时显示配置里的连接信息。
func (c *cli) statusOffline(p config.Paths) int {
	conf, err := config.Load(p.Config, os.Geteuid())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return c.fail("尚未安装：找不到配置 %s，请先执行 pimon-agent install", p.Config)
	case errors.Is(err, fs.ErrPermission):
		return c.fail("守护进程未运行；没有权限读取配置 %s，请用 sudo 执行", p.Config)
	case err != nil:
		return c.fail("守护进程未运行；%v", err)
	}
	w := c.stdout
	line(w, "模式", modeText(conf.Mode))
	switch conf.Mode {
	case config.ModeDialIn:
		line(w, "hub 地址", conf.HubURL)
		line(w, "钉扎指纹", conf.HubPin)
	case config.ModeDialOut:
		line(w, "监听地址", orDefault(conf.Listen, ":31418"))
		paired := "否（请在本机执行 pimon-agent pair）"
		if conf.CredentialHash != "" {
			paired = "是"
		}
		line(w, "已配对", paired)
	}
	return c.fail("守护进程未运行（控制套接字 %s 不存在或无人监听）。请先启动 agent 服务，或用 pimon-agent run 在前台运行", p.CtlSock)
}

func (c *cli) printStatus(st ctl.Status) {
	w := c.stdout
	line(w, "模式", modeText(st.Mode))
	switch st.Mode {
	case config.ModeDialIn:
		line(w, "hub 地址", st.HubURL)
		line(w, "钉扎指纹", st.Pin)
	case config.ModeDialOut:
		line(w, "监听地址", st.Listen)
	}
	conn := "未连接"
	if st.Connected {
		conn = "已连接"
		if !st.Since.IsZero() {
			conn += fmt.Sprintf("，已持续 %s（自 %s）", humanDuration(c.now().Sub(st.Since)), st.Since.Local().Format("2006-01-02 15:04:05"))
		}
	}
	line(w, "连接", conn)
	line(w, "状态", stateText(st.State))
	if inc := st.Incompatible; inc != nil {
		line(w, "不兼容", fmt.Sprintf("%s（需要协议版本 ≥ %d，hub 版本 %s）", inc.Reason, inc.RequiredMin, inc.HubVersion))
	}
	if st.LastError != "" {
		line(w, "最近错误", st.LastError)
	}
	line(w, "已分配实例", fmt.Sprint(st.Instances))
	if !st.PairWindowExpiresAt.IsZero() {
		line(w, "配对窗口", "开放至 "+st.PairWindowExpiresAt.Local().Format("15:04:05"))
	}
	line(w, "版本", fmt.Sprintf("%s（协议版本 %d，最低兼容 %d）", st.Version, st.ProtocolVersion, st.MinCompatibleVersion))
}

func line(w io.Writer, k, v string) { _, _ = fmt.Fprintf(w, "%s：%s\n", k, v) }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func modeText(m string) string {
	switch m {
	case config.ModeDialIn:
		return "拨入（agent 主动连接 hub）"
	case config.ModeDialOut:
		return "拨出（hub 连接本机）"
	case "":
		return "未知（守护进程尚未读入配置，或配置有误）"
	}
	return m
}

func stateText(s string) string {
	switch s {
	case "running":
		return "运行中"
	case "revoked":
		return "已被吊销"
	case "bad_credential":
		return "凭据无效"
	case "config_error":
		return "配置错误"
	case "incompatible":
		return "协议版本不兼容"
	case "idle":
		return "空闲"
	}
	return s
}

// humanDuration 把时长格式化为「1 天 2 小时 3 分 4 秒」，省略高位的零。
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return "不到 1 秒"
	}
	d = d.Truncate(time.Second)
	parts := []struct {
		unit time.Duration
		name string
	}{{24 * time.Hour, "天"}, {time.Hour, "小时"}, {time.Minute, "分"}, {time.Second, "秒"}}
	var b []string
	for _, p := range parts {
		if n := d / p.unit; n > 0 || len(b) > 0 {
			b = append(b, fmt.Sprintf("%d %s", n, p.name))
			d -= n * p.unit
		}
	}
	return strings.Join(b, " ")
}
