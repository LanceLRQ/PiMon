package plugindev

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// UsageError 表示命令行用法错误，调用方应以退出码 2 结束。
type UsageError string

// Error 实现 error 接口。
func (e UsageError) Error() string { return string(e) }

// Usage 是 plugin 子命令的帮助文字。
const Usage = `用法: pimon-hub plugin <子命令> ...

子命令:
  validate <目录>                            校验插件目录（plugin.yaml 带行号报错，检查 run 入口）
  run <目录> [--config <文件>] [--proxy <URL>]  在本机运行插件一次并打印报告摘要

  --config   插件配置文件，.json 或 .yaml/.yml，按 config_schema 校验；省略时用空配置
  --proxy    可选代理，如 socks5://127.0.0.1:1080
`

// Command 分派 plugin 子命令。args 不含 "plugin" 本身。
func Command(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return UsageError(Usage)
	}
	switch args[0] {
	case "validate":
		pos, _, err := parseArgs("validate", args[1:], nil)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return UsageError("用法: pimon-hub plugin validate <目录>")
		}
		return Validate(pos[0], stdout)
	case "run":
		var opts RunOptions
		pos, _, err := parseArgs("run", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&opts.ConfigPath, "config", "", "")
			fs.StringVar(&opts.ProxyURL, "proxy", "", "")
		})
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return UsageError("用法: pimon-hub plugin run <目录> [--config <文件>] [--proxy <URL>]")
		}
		return Run(ctx, pos[0], opts, stdout)
	default:
		_, _ = fmt.Fprint(stderr, Usage)
		return UsageError(fmt.Sprintf("未知子命令 %q", args[0]))
	}
}

// parseArgs 允许参数出现在位置参数之前或之后。
func parseArgs(name string, args []string, define func(*flag.FlagSet)) (pos []string, fs *flag.FlagSet, err error) {
	fs = flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if define != nil {
		define(fs)
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, fs, UsageError(err.Error())
		}
		if fs.NArg() == 0 {
			return pos, fs, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
