// Package docker 是内置的 Docker 容器插件：经 unix socket 只读访问 Docker Engine API，
// 汇报每个容器的运行状态与健康检查结果，以及运行数与总数。只能在 agent 上运行（需要读本机 socket）。
//
// 只用 GET 请求，不启停容器、不读容器内资源占用。
package docker

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
)

//go:embed plugin.yaml
var manifestYAML []byte

const (
	defaultSocket = "/var/run/docker.sock"
	// startingGrace 是健康检查处于 starting 时的最短容忍时长；
	// 实际阈值取它与容器自身 StartPeriod 的较大者。
	startingGrace = 5 * time.Minute
)

// Plugin 是 docker 的 Source 实现。
type Plugin struct {
	m *manifest.Manifest
}

func init() {
	runtime.Register(New())
}

// New 构造插件。
func New() *Plugin {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("docker 内置 manifest 不合法: " + err.Error())
	}
	return &Plugin{m: m}
}

// Manifest 实现 runtime.Source。
func (p *Plugin) Manifest() *manifest.Manifest { return p.m }

func socketPath(c map[string]any) string {
	if s := strings.TrimSpace(cfg.String(c, "socket")); s != "" {
		return s
	}
	return defaultSocket
}

// Collect 实现 runtime.Source。socket 不可用时返回 error（运行时据此保留旧值并标记过期）。
func (p *Plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	now := clk.Now()
	includeStopped := cfg.Bool(in.Config, "include_stopped", true)
	filters := cfg.List(in.Config, "names")

	cli := newClient(socketPath(in.Config))
	defer cli.close()
	if err := cli.negotiate(ctx); err != nil {
		return nil, err
	}
	list, err := cli.list(ctx, includeStopped)
	if err != nil {
		return nil, err
	}

	rep := &report.Report{Status: report.StatusOK, CollectedAt: now.UnixMilli()}
	var running, total, abnormal int
	for _, c := range list {
		name := containerName(c)
		if !matchNames(filters, name) {
			continue
		}
		total++
		if c.State == "running" {
			running++
		}
		it := report.Item{Key: "container[" + name + "]", Type: report.TypeState}
		detail, err := cli.inspect(ctx, c.ID)
		if err != nil {
			it.State = report.StatusUnknown
			it.Text = "状态未知 / Unknown"
			it.Error = "无法读取容器详情 / Cannot inspect container"
		} else {
			it.State, it.Text = classify(detail, now)
		}
		if it.State == report.StatusWarning || it.State == report.StatusCritical {
			abnormal++
		}
		rep.Status = raise(rep.Status, it.State)
		rep.Items = append(rep.Items, it)
	}
	rn, tn := float64(running), float64(total)
	rep.Items = append(rep.Items,
		report.Item{Key: "containers.running", Type: report.TypeNumber, Value: &rn},
		report.Item{Key: "containers.total", Type: report.TypeNumber, Value: &tn},
	)
	rep.Summary = fmt.Sprintf("运行 %d / 共 %d，异常 %d / Running %d of %d, %d abnormal", running, total, abnormal, running, total, abnormal)
	return rep, nil
}

// containerName 取容器的主名称：去掉前导 /，优先不含 / 的名字（带 / 的是 link 别名）。
func containerName(c listEntry) string {
	for _, n := range c.Names {
		n = strings.TrimPrefix(n, "/")
		if n != "" && !strings.Contains(n, "/") {
			return n
		}
	}
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:min(12, len(c.ID))]
}

// matchNames 判断名称是否命中过滤列表；列表为空表示全部。支持 * 通配。
func matchNames(filters []string, name string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if ok, _ := path.Match(strings.TrimSpace(f), name); ok {
			return true
		}
	}
	return false
}

// classify 把容器详情归为状态与文字。
func classify(d inspectResult, now time.Time) (report.Status, string) {
	s := d.State
	suffix := ""
	if d.RestartCount > 0 {
		suffix = fmt.Sprintf("，已重启 %d 次 / , restarted %d times", d.RestartCount, d.RestartCount)
	}
	if s.Restarting || s.Status == "restarting" {
		return report.StatusCritical, "重启循环中 / Restart loop" + suffix
	}
	switch s.Status {
	case "running":
		health := ""
		if s.Health != nil {
			health = s.Health.Status
		}
		switch health {
		case "unhealthy":
			return report.StatusWarning, "健康检查失败 / Unhealthy" + suffix
		case "starting":
			var period time.Duration
			if d.Config.Healthcheck != nil {
				period = time.Duration(d.Config.Healthcheck.StartPeriod)
			}
			started, err := time.Parse(time.RFC3339Nano, s.StartedAt)
			if err != nil || started.Year() <= 1 {
				return report.StatusOK, "启动中 / Starting" + suffix
			}
			if st := startingState(now.Sub(started), period); st != report.StatusOK {
				return st, "健康检查迟迟未通过 / Health check stuck in starting" + suffix
			}
			return report.StatusOK, "启动中 / Starting" + suffix
		case "healthy":
			return report.StatusOK, "运行中（健康）/ Running (healthy)" + suffix
		}
		return report.StatusOK, "运行中 / Running" + suffix
	case "paused":
		return report.StatusWarning, "已暂停 / Paused" + suffix
	case "created":
		return report.StatusUnknown, "已创建未启动 / Created"
	case "removing":
		return report.StatusUnknown, "删除中 / Removing"
	case "dead":
		return report.StatusCritical, "已失效 / Dead"
	case "exited":
		return exitedState(s.ExitCode, s.OOMKilled)
	}
	return report.StatusUnknown, "状态未知 / Unknown"
}

// startingState 判断健康检查 starting 的持续时间是否过长：阈值为 max(StartPeriod, 5 分钟)。
func startingState(age, startPeriod time.Duration) report.Status {
	limit := max(startPeriod, startingGrace)
	if age > limit {
		return report.StatusWarning
	}
	return report.StatusOK
}

// exitedState 判断已退出容器。退出码 0、143（SIGTERM）与未被 OOM 杀死的 137（SIGKILL）
// 视为正常停止（docker stop 的结果），其余非 0 或 OOM 判严重。
func exitedState(code int, oom bool) (report.Status, string) {
	switch {
	case oom:
		return report.StatusCritical, fmt.Sprintf("内存不足被杀（退出码 %d）/ OOM killed (exit %d)", code, code)
	case code == 0 || code == 143 || code == 137:
		return report.StatusUnknown, "已停止 / Stopped"
	}
	return report.StatusCritical, fmt.Sprintf("异常退出（退出码 %d）/ Exited abnormally (exit %d)", code, code)
}

// raise 按 ok < warning < critical 取更严重者；unknown 不抬高整体状态。
func raise(cur, s report.Status) report.Status {
	rank := map[report.Status]int{report.StatusOK: 0, report.StatusWarning: 1, report.StatusCritical: 2}
	if r, ok := rank[s]; ok && r > rank[cur] {
		return s
	}
	return cur
}
