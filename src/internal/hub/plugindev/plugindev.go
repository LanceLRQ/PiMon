package plugindev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

const (
	manifestFile = "plugin.yaml"
	runFile      = "run"
)

// loaded 是加载插件目录的结果。
type loaded struct {
	manifest *manifest.Manifest
	runPath  string
	// problems 是 manifest 之外的问题（入口缺失、目录名不符等）。
	problems []string
	// entryProblem 表示入口文件本身不可用，运行时是致命的。
	entryProblem bool
}

// load 读取并校验插件目录。manifest 不合法时返回 *manifest.Error。
func load(dir string) (*loaded, error) {
	// 转成绝对路径：执行器以插件目录为工作目录启动 run，相对路径会在那里失效。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, fmt.Errorf("无法读取 %s: %w", filepath.Join(dir, manifestFile), err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return nil, err
	}
	l := &loaded{manifest: m, runPath: filepath.Join(dir, runFile)}
	if filepath.Base(dir) != m.ID {
		l.problems = append(l.problems, fmt.Sprintf("目录名 %q 与 manifest 的 id %q 不一致（hub 要求两者相同）", filepath.Base(dir), m.ID))
	}
	if m.Runtime != manifest.RuntimeExec {
		l.problems = append(l.problems, fmt.Sprintf("插件目录里的 manifest 必须声明 runtime: exec，实际为 %q", m.Runtime))
	}
	// 与 hub 加载一致：用 Lstat，不跟随符号链接。
	info, err := os.Lstat(l.runPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		l.problems = append(l.problems, "缺少可执行入口 "+runFile)
		l.entryProblem = true
	case err != nil:
		l.problems = append(l.problems, fmt.Sprintf("无法读取入口 %s: %v", runFile, err))
		l.entryProblem = true
	case info.Mode()&fs.ModeSymlink != 0:
		l.problems = append(l.problems, "入口 "+runFile+" 是符号链接，hub 不允许（必须是插件目录内的普通文件）")
		l.entryProblem = true
	case !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0:
		l.problems = append(l.problems, fmt.Sprintf("入口 %s 必须是带执行权限的普通文件（chmod +x %s）", runFile, runFile))
		l.entryProblem = true
	}
	return l, nil
}

// Validate 校验插件目录：manifest 语法与语义（报错带行号），以及 run 入口存在且可执行。
// 任何问题都以非 nil 错误返回，错误文字含全部问题（manifest 问题为 *manifest.Error）；通过时把结果写入 out。
func Validate(dir string, out io.Writer) error {
	l, err := load(dir)
	if err != nil {
		return err
	}
	if len(l.problems) > 0 {
		return errors.New(strings.Join(l.problems, "; "))
	}
	m := l.manifest
	_, _ = fmt.Fprintf(out, "校验通过: %s v%s（配置字段 %d 个，数据项 %d 个，小组件 %d 个）\n",
		m.ID, m.Version, len(m.ConfigSchema), len(m.Outputs), len(m.Widgets))
	_, _ = fmt.Fprintln(out, "提示: 这里不检查文件属主与权限；hub 加载时还会检查目录、plugin.yaml 与 run 的属主（hub 运行用户或 root）以及是否可被组或其他用户写。")
	return nil
}

// RunOptions 是 Run 的参数。
type RunOptions struct {
	// ConfigPath 是配置文件（.json、.yaml、.yml）；为空时按空配置处理（仍会应用默认值并校验必填项）。
	ConfigPath string
	// ProxyURL 是可选代理，形如 socks5://host:1080；为空直连。
	ProxyURL string
}

// Run 在本机用真实时钟运行插件一次：校验配置、拆出密钥、按 manifest 超时执行、
// 校验报告并打印摘要。密钥与代理凭据不会打印。失败返回错误。
func Run(ctx context.Context, dir string, opts RunOptions, out io.Writer) error {
	l, err := load(dir)
	if err != nil {
		return err
	}
	if l.entryProblem {
		return errors.New(strings.Join(l.problems, "; "))
	}
	m := l.manifest

	raw, err := readConfig(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, errs := schema.Prepare(m.ConfigSchema, raw)
	if len(errs) > 0 {
		return fmt.Errorf("配置不符合 config_schema: %w", errs)
	}
	plain, secretMap := schema.Split(m.ConfigSchema, cfg)
	secrets := make(map[string]string, len(secretMap))
	for k, v := range secretMap {
		if s, ok := v.(string); ok {
			secrets[k] = s
		}
	}
	px, err := proxy.Parse(opts.ProxyURL)
	if err != nil {
		return fmt.Errorf("--proxy: %w", err)
	}

	src := runtime.NewExecSource(m, l.runPath)
	in := runtime.Input{Config: plain, Secrets: secrets, Proxy: px, Clock: clock.Real{}}
	start := in.Clock.Now()
	rep, err := runtime.CollectWithTimeout(ctx, src, in)
	elapsed := in.Clock.Now().Sub(start)
	if err != nil {
		_, _ = fmt.Fprintf(out, "运行失败（耗时 %s）\n", elapsed.Round(1e6))
		return err
	}
	printSummary(out, m, rep, elapsed, len(secrets))
	return nil
}
