// Package systemd 是内置的 systemd 单元插件：经 `systemctl show` 只读查询指定单元的运行状态与重启次数。
// 不依赖 D-Bus 库，非 root 即可执行；hub 与 agent 都能运行。
package systemd

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
)

//go:embed plugin.yaml
var manifestYAML []byte

const showProperties = "Id,LoadState,ActiveState,SubState,Result,Type,NRestarts"

const errNotSystemd = "此机器不是 systemd 系统 / Not a systemd system"

// unitNamePattern 与 plugin.yaml 里 list 的 pattern 一致，Collect 里再校验一次，
// 防止绕过 schema 的配置把选项形态的字符串传给 systemctl。
var unitNamePattern = regexp.MustCompile(`^[A-Za-z0-9@:._\-]+$`)

var unitSuffixes = []string{".service", ".socket", ".timer", ".mount", ".automount", ".target", ".path", ".slice", ".scope", ".swap", ".device"}

// Plugin 是 systemd 的 Source 实现。
type Plugin struct {
	m        *manifest.Manifest
	runner   cmdexec.Runner
	goos     string
	lookPath func(string) (string, error)
}

func init() {
	runtime.Register(New(cmdexec.New()))
}

// New 构造插件，runner 为外部命令执行器。
func New(runner cmdexec.Runner) *Plugin {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("systemd 内置 manifest 不合法: " + err.Error())
	}
	return &Plugin{m: m, runner: runner, goos: goruntime.GOOS, lookPath: exec.LookPath}
}

// Manifest 实现 runtime.Source。
func (p *Plugin) Manifest() *manifest.Manifest { return p.m }

// normalizeUnits 去空白、省略后缀的补 .service、去重并保持顺序。
func normalizeUnits(raw []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, r := range raw {
		n := strings.TrimSpace(r)
		if n == "" {
			continue
		}
		if !unitNamePattern.MatchString(n) {
			return nil, fmt.Errorf("单元名「%s」不合法 / Invalid unit name %q", n, n)
		}
		has := false
		for _, s := range unitSuffixes {
			if strings.HasSuffix(n, s) {
				has = true
				break
			}
		}
		if !has {
			n += ".service"
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("未配置单元 / No units configured")
	}
	return out, nil
}

// Collect 实现 runtime.Source。非 systemd 系统、命令失败、被取消都返回 error（运行时据此保留旧值并标记过期）。
func (p *Plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	units, err := normalizeUnits(cfg.List(in.Config, "list"))
	if err != nil {
		return nil, err
	}
	if p.goos != "linux" {
		return nil, errors.New(errNotSystemd)
	}
	if _, err := p.lookPath("systemctl"); err != nil {
		return nil, errors.New(errNotSystemd)
	}

	args := append([]string{"show", "-p", showProperties, "--"}, units...)
	res, err := p.runner.Run(ctx, "systemctl", args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("查询单元被中断 / Unit query interrupted: %w", ctx.Err())
		}
		return nil, fmt.Errorf("执行 systemctl 失败 / systemctl failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("查询单元被中断 / Unit query interrupted: %w", err)
	}
	if res.ExitCode != 0 {
		if strings.Contains(res.Stderr, "not been booted") {
			return nil, errors.New(errNotSystemd)
		}
		return nil, fmt.Errorf("systemctl 退出码 %d：%s / systemctl exited with %d", res.ExitCode, strings.TrimSpace(res.Stderr), res.ExitCode)
	}
	sections := parseShow(string(res.Stdout))
	if len(sections) != len(units) {
		return nil, fmt.Errorf("systemctl 输出 %d 段，期望 %d 段 / Unexpected systemctl output: %d sections, want %d", len(sections), len(units), len(sections), len(units))
	}

	rep := &report.Report{Status: report.StatusOK, CollectedAt: clk.Now().UnixMilli()}
	var active, abnormal int
	for i, name := range units {
		st, text, errMsg, restarts := classify(sections[i])
		it := report.Item{Key: "unit[" + name + "]", Type: report.TypeState, State: st, Text: text, Error: errMsg}
		rep.Items = append(rep.Items, it)
		if sections[i]["LoadState"] == "loaded" || sections[i]["LoadState"] == "masked" {
			ri := report.Item{Key: "unit_restarts[" + name + "]", Type: report.TypeNumber, Value: restarts}
			rep.Items = append(rep.Items, ri)
		}
		switch {
		case st == report.StatusOK:
			active++
		case st == report.StatusWarning, st == report.StatusCritical, errMsg != "":
			abnormal++
		}
		rep.Status = raise(rep.Status, st)
		if errMsg != "" {
			rep.Status = raise(rep.Status, report.StatusWarning)
		}
	}
	rep.Summary = fmt.Sprintf("正常 %d / 共 %d，异常 %d / OK %d of %d, %d abnormal", active, len(units), abnormal, active, len(units), abnormal)
	return rep, nil
}

// parseShow 把 `systemctl show` 的多段输出解析为每段一个属性表；段以空行分隔，未知行忽略。
func parseShow(out string) []map[string]string {
	var secs []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			secs = append(secs, cur)
			cur = map[string]string{}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur[k] = v
	}
	flush()
	return secs
}

// classify 把一段属性归为状态、文字、条目错误与重启次数（缺失或无法解析为 nil，不当作 0）。
func classify(s map[string]string) (report.Status, string, string, *float64) {
	var restarts *float64
	if v, err := strconv.ParseFloat(s["NRestarts"], 64); err == nil && v >= 0 {
		restarts = &v
	}
	switch s["LoadState"] {
	case "not-found":
		return report.StatusUnknown, "单元不存在 / Unit not found", "单元不存在 / Unit not found", nil
	case "masked":
		return report.StatusUnknown, "已屏蔽 / Masked", "", restarts
	case "loaded":
	case "":
		return report.StatusUnknown, "状态未知 / Unknown", "无法读取单元状态 / Cannot read unit state", restarts
	default:
		return report.StatusUnknown, "加载异常 / Load problem: " + s["LoadState"], "单元加载失败 / Unit failed to load", restarts
	}
	active := s["ActiveState"]
	result := s["Result"]
	if active == "" {
		return report.StatusUnknown, "状态未知 / Unknown", "无法读取单元状态 / Cannot read unit state", restarts
	}
	if active == "failed" || (result != "" && result != "success") {
		text := "已失败 / Failed"
		if result != "" && result != "success" {
			text += "（" + result + "）/ (" + result + ")"
		}
		return report.StatusCritical, text, "", restarts
	}
	switch active {
	case "active":
		if s["SubState"] == "running" {
			return report.StatusOK, "运行中 / Running", "", restarts
		}
		return report.StatusOK, "已激活 / Active" + subSuffix(s["SubState"]), "", restarts
	case "activating":
		return report.StatusWarning, "启动中 / Activating" + subSuffix(s["SubState"]), "", restarts
	case "reloading":
		return report.StatusWarning, "重载中 / Reloading", "", restarts
	case "deactivating":
		return report.StatusWarning, "停止中 / Deactivating", "", restarts
	case "inactive":
		if s["Type"] == "oneshot" && result == "success" {
			return report.StatusOK, "已完成 / Completed", "", restarts
		}
		return report.StatusWarning, "未运行 / Inactive", "", restarts
	}
	return report.StatusUnknown, "状态未知 / Unknown: " + active, "", restarts
}

func subSuffix(sub string) string {
	if sub == "" {
		return ""
	}
	return "（" + sub + "）/ (" + sub + ")"
}

// raise 按 ok < warning < critical 取更严重者；unknown 不抬高整体状态。
func raise(cur, s report.Status) report.Status {
	rank := map[report.Status]int{report.StatusOK: 0, report.StatusWarning: 1, report.StatusCritical: 2}
	if r, ok := rank[s]; ok && r > rank[cur] {
		return s
	}
	return cur
}
