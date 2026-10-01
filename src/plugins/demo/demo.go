// Package demo 是「演示数据」内置插件：按 DESIGN-SPEC 第 4 节的统一演示数据产出各类型数据项，
// 用于开发与截图。它照常注册，但不会自动创建实例。
package demo

import (
	"context"
	_ "embed"
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
	return &report.Report{
		Status:      report.StatusCritical,
		Summary:     "演示数据 / Demo data",
		CollectedAt: now.UnixMilli(),
		Items:       items(now),
	}, nil
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
