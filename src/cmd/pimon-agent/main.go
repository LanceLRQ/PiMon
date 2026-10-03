// pimon-agent 是 PiMon 的被监控机器代理入口。
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const usage = `用法: pimon-agent <命令> [参数]

命令:
  install    安装并启动 agent 服务（拨入：--hub --fp --code；拨出：--listen）
  pair       拨出模式下开启 15 分钟配对窗口并打印配对信息
  run        守护进程（由服务管理器调用）
  status     显示模式、连接状态、已分配实例数、版本等
  repin      hub 证书重建后更新钉扎指纹（--fp SHA256:…）
  upgrade    升级 agent（拨入从 hub；拨出用 --release）
  uninstall  停止并删除服务（--purge 同时删除配置与数据）
  version    显示版本号
`

var subcommands = map[string]bool{
	"install": true, "pair": true, "run": true, "status": true,
	"repin": true, "upgrade": true, "uninstall": true,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run 分派子命令并返回退出码：0 成功，1 命令失败，2 用法错误。
func run(args []string, _ io.Reader, stdout, stderr io.Writer, _ func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	cmd := args[0]
	switch {
	case cmd == "version":
		_, _ = fmt.Fprintln(stdout, version.Version)
		return 0
	case cmd == "help" || cmd == "-h" || cmd == "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case subcommands[cmd]:
		_, _ = fmt.Fprintf(stderr, "错误: 命令 %s 尚未实现\n", cmd)
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n%s", cmd, usage)
		return 2
	}
}
