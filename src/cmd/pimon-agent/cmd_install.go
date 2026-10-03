package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"

	"golang.org/x/term"

	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
	"github.com/LanceLRQ/PiMon/src/internal/agent/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// cmdInstall 安装并启动 agent 服务。pair 帧不携带角色，角色在网页生成配对码时设置，所以不提供 --role。
func (c *cli) cmdInstall(args []string) int {
	fs, user := c.flagSet("install")
	var o install.Options
	fs.StringVar(&o.Hub, "hub", "", "拨入：hub 地址，如 wss://192.168.1.10:31418")
	fs.StringVar(&o.Fingerprint, "fp", "", "拨入：hub 证书的 SPKI 指纹（SHA256:…，网页上显示）")
	fs.StringVar(&o.Code, "code", "", "拨入：网页「添加设备」生成的一次性配对码")
	fs.StringVar(&o.Name, "name", "", "拨入：设备名（网页生成配对码时未预设名称才使用；角色在网页设置）")
	fs.BoolVar(&o.RePair, "re-pair", false, "拨入：已与该 hub 配对时仍用 --code 重新配对")
	fs.StringVar(&o.Listen, "listen", "", "拨出：监听地址，如 :31418")
	fs.BoolVar(&o.DockerGroup, "docker-group", false, "系统模式：把 pimon 加入 docker 组（近似授予 root 权限；非交互模式须显式指定）")
	fs.BoolVar(&o.Sudoers, "sudoers", false, "系统模式：写入 storage-health 只读命令的 sudoers 白名单（smartctl、mdadm、btrfs；非交互模式须显式指定）")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	o.User = *user
	d, err := c.installDeps(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return c.installExit(c.doInstall(ctx, d, o))
}

// cmdUninstall 停止并删除服务；--purge 同时删除配置与数据。
func (c *cli) cmdUninstall(args []string) int {
	fs, user := c.flagSet("uninstall")
	purge := fs.Bool("purge", false, "同时删除配置与数据目录")
	if code, ok := c.parse(fs, args); !ok {
		return code
	}
	d, err := c.installDeps(*user)
	if err != nil {
		return c.fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return c.installExit(c.doUninstall(ctx, d, install.UninstallOptions{User: *user, Purge: *purge}))
}

// installExit 把 install/uninstall 的结果转成退出码：参数错误 2；其他失败 1（原因与已完成步骤已输出到标准输出）。
func (c *cli) installExit(err error) int {
	var ue install.UsageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		_, _ = fmt.Fprintln(c.stderr, "错误:", ue)
		return 2
	}
	return 1
}

// installDeps 装配真实系统依赖；配对会话的 hello 由内置插件与插件目录组装。
func (c *cli) installDeps(userMode bool) (install.Deps, error) {
	self, err := os.Executable()
	if err != nil {
		return install.Deps{}, fmt.Errorf("定位当前可执行文件: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	username := c.getenv("USER")
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	interactive := false
	if f, ok := c.stdin.(*os.File); ok {
		interactive = term.IsTerminal(int(f.Fd()))
	}
	d := install.NewDeps(c.stdout, c.stdin, interactive, c.goos, c.getenv("HOME"), username, self)
	d.Hello = func() agent.Hello { return c.pairHello(userMode) }
	return d, nil
}

// pairHello 组装配对会话上的 hello：内置插件与插件目录里可用的 exec 插件。
func (c *cli) pairHello(userMode bool) agent.Hello {
	var ms []agent.PluginManifest
	if p, err := c.paths(userMode); err == nil {
		reg := plugins.New(plugins.Config{Dir: p.PluginDir, Builtins: runtime.Builtins(), Log: c.log})
		reg.Scan()
		ms, _ = reg.Manifests()
	}
	host, _ := os.Hostname()
	return link.NewHello(host, ms)
}
