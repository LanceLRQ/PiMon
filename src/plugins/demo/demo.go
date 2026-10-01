// Package demo 是「演示数据」内置插件：按 DESIGN-SPEC 第 4 节的统一演示数据产出各类型数据项，
// 用于开发与截图。它照常注册，但不会自动创建实例。
package demo

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

//go:embed plugin.yaml
var manifestYAML []byte

type plugin struct{ m *manifest.Manifest }

func init() {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("demo 内置 manifest 不合法: " + err.Error())
	}
	runtime.Register(&plugin{m: m})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

func (p *plugin) Collect(_ context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	now := clk.Now()
	list := items(now)
	if profile, _ := in.Config["profile"].(string); profile == profileExtreme {
		list = extremeItems(list)
	}
	return &report.Report{
		Status:      report.StatusCritical,
		Summary:     "演示数据 / Demo data",
		CollectedAt: now.UnixMilli(),
		Items:       list,
	}, nil
}

// profile 配置取值：default 是统一演示数据；extreme 在此之上叠加极端数据，用于验证小组件不溢出。
const profileExtreme = "extreme"

// extremeItems 把默认数据项里的主机、告警与任务表替换为极端版本，并追加 4 币种余额：
// 50 项列表、超长文本、很长的名称与很大的金额。
func extremeItems(base []report.Item) []report.Item {
	out := make([]report.Item, 0, len(base)+54)
	for _, it := range base {
		if strings.HasPrefix(it.Key, "host[") || it.Key == "alert.disk" || it.Key == "tasks" {
			continue
		}
		out = append(out, it)
	}
	for i := 1; i <= 50; i++ {
		st := report.StatusOK
		switch {
		case i%17 == 0:
			st = report.StatusCritical
		case i%7 == 0:
			st = report.StatusWarning
		}
		h := state(fmt.Sprintf("host[node-%02d]", i), st,
			fmt.Sprintf("production-cluster-worker-node-%02d.internal.example.com · CPU 37%% · 46.2°C", i))
		h.Label = fmt.Sprintf("production-cluster-worker-node-%02d", i)
		out = append(out, h)
	}
	out = append(out,
		money("balance[cny]", 123456789.12, "CNY"),
		money("balance[usd]", 9876543.21, "USD"),
		money("balance[eur]", 1234567.89, "EUR"),
		money("balance[jpy]", 98765432100, "JPY"),
		state("alert.disk", report.StatusCritical, strings.Repeat("ubuntu-srv 系统盘 / 使用率 95%，已持续 6 分钟，请尽快清理日志与缓存目录。", 4)),
	)
	rows := make([][]any, 0, 30)
	for i := 1; i <= 30; i++ {
		rows = append(rows, []any{"Claude Code", fmt.Sprintf("第 %d 个很长的任务名称：重构整个监控面板的布局编辑器并补全端到端测试用例", i), "running", fmt.Sprintf("%d min", i)})
	}
	out = append(out, report.Item{Key: "tasks", Type: report.TypeTable, Columns: []string{"agent", "task", "state", "since"}, Rows: rows})
	return out
}

func items(now time.Time) []report.Item {
	codexReset := now.Add(75 * time.Minute).UnixMilli()
	claudeReset := nextFriday8(now).UnixMilli()
	return []report.Item{
		quota("quota.codex.5h", 72, &codexReset, ""),
		quota("quota.codex.tool", 45, nil, ""),
		quota("quota.claude.week", 38, &claudeReset, ""),
		quota("quota.claude.5h", 61, nil, ""),
		quota("quota.glm.5h", 85, nil, ""),
		quota("quota.glm.week", 60, nil, ""),
		money("money.deepseek", 86.40, "CNY"),
		money("money.openrouter", 12.75, "USD"),
		state("host[raspberrypi]", report.StatusOK, "CPU 37% · 46.2°C"),
		state("host[fnos]", report.StatusOK, "32 d"),
		state("host[ubuntu-srv]", report.StatusWarning, "disk 91%"),
		state("host[vps-xray]", report.StatusOK, "load 0.42"),
		gauge("cpu.pi", 37.5, "%"),
		gauge("mem.pi", 62, "%"),
		quota("disk.ubuntu-srv", 9, nil, "%"),
		{
			Key: "tasks", Type: report.TypeTable,
			Columns: []string{"agent", "task", "state", "since"},
			Rows: [][]any{
				{"Claude Code", "PiMon UI 布局编辑器", "running", "12 min"},
				{"Codex", "爬虫数据清洗", "waiting", "2 min ago"},
				{"Claude Code", "部署脚本清理", "done", "3 min ago"},
			},
		},
		state("alert.disk", report.StatusCritical, "ubuntu-srv 系统盘 / 使用率 95%，已持续 6 分钟"),
	}
}

func quota(key string, remainingPct float64, resetsAt *int64, unit string) report.Item {
	return report.Item{Key: key, Type: report.TypeQuota, RemainingPct: &remainingPct, ResetsAt: resetsAt, Unit: unit}
}

func money(key string, amount float64, currency string) report.Item {
	return report.Item{Key: key, Type: report.TypeMoney, Amount: &amount, Currency: currency}
}

func gauge(key string, v float64, unit string) report.Item {
	return report.Item{Key: key, Type: report.TypeGauge, Value: &v, Unit: unit}
}

func state(key string, st report.Status, text string) report.Item {
	return report.Item{Key: key, Type: report.TypeState, State: st, Text: text}
}

// nextFriday8 返回 now 之后最近一个周五 08:00（与 now 同时区）。
func nextFriday8(now time.Time) time.Time {
	days := (int(time.Friday) - int(now.Weekday()) + 7) % 7
	t := time.Date(now.Year(), now.Month(), now.Day()+days, 8, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 7)
	}
	return t
}
