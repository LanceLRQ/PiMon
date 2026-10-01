package demo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// 周一深夜 22:47（DESIGN-SPEC 第 4 节的场景时刻）。
var monday = time.Date(2026, 9, 28, 22, 47, 0, 0, time.UTC)

func collect(t *testing.T) *report.Report { return collectWith(t, nil) }

func collectWith(t *testing.T, cfg map[string]any) *report.Report {
	t.Helper()
	src, ok := runtime.Builtin("demo")
	if !ok {
		t.Fatal("demo 应在 init 中注册")
	}
	rep, err := src.Collect(context.Background(), runtime.Input{Config: cfg, Clock: clock.NewFake(monday)})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func pct(t *testing.T, rep *report.Report, key string) float64 {
	t.Helper()
	it := rep.Find(key)
	if it == nil || it.RemainingPct == nil {
		t.Fatalf("缺少额度项 %s", key)
	}
	return *it.RemainingPct
}

func TestManifestNamedDemo(t *testing.T) {
	src, _ := runtime.Builtin("demo")
	n := src.Manifest().Name
	if !strings.Contains(n.Get("zh"), "演示") || !strings.Contains(n.Get("en"), "Demo") {
		t.Fatalf("名称应标注演示/Demo: %+v", n)
	}
}

func TestReportValidates(t *testing.T) {
	rep := collect(t)
	if err := rep.Validate(nil); err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusCritical {
		t.Errorf("含严重告警示例，status 应为 critical: %v", rep.Status)
	}
}

func TestEveryItemDeclaredInManifest(t *testing.T) {
	src, _ := runtime.Builtin("demo")
	rep := collect(t)
	for _, it := range rep.Items {
		declared := false
		for _, o := range src.Manifest().Outputs {
			k, err := report.ParseKey(o.Key)
			if err == nil && k.Matches(it.Key) && o.Type == it.Type {
				declared = true
			}
		}
		if !declared {
			t.Errorf("数据项 %s(%s) 未在 outputs 中声明", it.Key, it.Type)
		}
	}
	for _, o := range src.Manifest().Outputs {
		k, _ := report.ParseKey(o.Key)
		if len(rep.Select(o.Key)) == 0 && !k.Wildcard {
			t.Errorf("outputs 声明了 %s 但报告里没有", o.Key)
		}
	}
}

func TestQuotaValuesFollowDesignSpec(t *testing.T) {
	rep := collect(t)
	want := map[string]float64{
		"quota.codex.5h": 72, "quota.codex.tool": 45,
		"quota.claude.week": 38, "quota.claude.5h": 61,
		"quota.glm.5h": 85, "quota.glm.week": 60,
	}
	for k, v := range want {
		if got := pct(t, rep, k); got != v {
			t.Errorf("%s 剩余应为 %v，实际 %v", k, v, got)
		}
	}
	codex := rep.Find("quota.codex.5h")
	if codex.ResetsAt == nil || *codex.ResetsAt != monday.Add(75*time.Minute).UnixMilli() {
		t.Errorf("Codex 5h 应在 1h15m 后重置: %v", codex.ResetsAt)
	}
	claude := rep.Find("quota.claude.week")
	if claude.ResetsAt == nil {
		t.Fatal("Claude 周窗口应有重置时间")
	}
	at := time.UnixMilli(*claude.ResetsAt).UTC()
	if at.Weekday() != time.Friday || at.Hour() != 8 || at.Minute() != 0 || !at.After(monday) {
		t.Errorf("Claude 应在下个周五 08:00 重置: %v", at)
	}
}

func TestHostsMoneyAndGauges(t *testing.T) {
	rep := collect(t)
	hosts := map[string]report.Status{
		"host[raspberrypi]": report.StatusOK, "host[fnos]": report.StatusOK,
		"host[ubuntu-srv]": report.StatusWarning, "host[vps-xray]": report.StatusOK,
	}
	for k, st := range hosts {
		it := rep.Find(k)
		if it == nil || it.State != st || it.Text == "" {
			t.Errorf("%s 应为 %v 且带说明: %+v", k, st, it)
		}
	}
	ds, or := rep.Find("money.deepseek"), rep.Find("money.openrouter")
	if ds == nil || *ds.Amount != 86.40 || ds.Currency != "CNY" || or == nil || *or.Amount != 12.75 || or.Currency != "USD" {
		t.Errorf("余额错误: %+v %+v", ds, or)
	}
	if v := rep.Find("cpu.pi"); v == nil || *v.Value != 37.5 {
		t.Errorf("CPU 应为 37.5: %+v", v)
	}
	if v := rep.Find("mem.pi"); v == nil || *v.Value != 62 {
		t.Errorf("内存应为 62: %+v", v)
	}
	if d := rep.Find("disk.ubuntu-srv"); d == nil || *d.RemainingPct != 9 {
		t.Errorf("ubuntu-srv 磁盘使用 91%%: %+v", d)
	}
}

func TestTasksAndAlert(t *testing.T) {
	rep := collect(t)
	tb := rep.Find("tasks")
	if tb == nil || len(tb.Rows) != 3 || len(tb.Columns) == 0 {
		t.Fatalf("应有 3 条 AI 任务: %+v", tb)
	}
	for _, r := range tb.Rows {
		if len(r) != len(tb.Columns) {
			t.Errorf("行宽与列数不一致: %v", r)
		}
	}
	al := rep.Find("alert.disk")
	if al == nil || al.State != report.StatusCritical || !strings.Contains(al.Text, "95%") {
		t.Fatalf("严重告警示例错误: %+v", al)
	}
}

func TestProfileFieldInManifest(t *testing.T) {
	src, _ := runtime.Builtin("demo")
	var found bool
	for _, f := range src.Manifest().ConfigSchema {
		if f.Key != "profile" {
			continue
		}
		found = true
		if f.Type != "enum" || f.Default != "default" || len(f.Options) != 2 || f.Options[0].Value != "default" || f.Options[1].Value != "extreme" {
			t.Errorf("profile 应为 default|extreme 的枚举且默认 default: %+v", f)
		}
	}
	if !found {
		t.Fatal("manifest 缺少 profile 配置项")
	}
}

func TestDefaultProfileHasNoExtremeData(t *testing.T) {
	for _, cfg := range []map[string]any{nil, {}, {"profile": "default"}} {
		rep := collectWith(t, cfg)
		if n := len(rep.Select("host[*]")); n != 4 {
			t.Errorf("默认 profile 应有 4 台主机，实际 %d（config=%v）", n, cfg)
		}
		if n := len(rep.Select("balance[*]")); n != 0 {
			t.Errorf("默认 profile 不应有多币种余额: %d", n)
		}
	}
}

func TestExtremeProfile(t *testing.T) {
	rep := collectWith(t, map[string]any{"profile": "extreme"})
	if err := rep.Validate(nil); err != nil {
		t.Fatal(err)
	}
	if n := len(rep.Select("host[*]")); n != 50 {
		t.Errorf("extreme 应输出 50 项列表: %d", n)
	}
	cur := map[string]bool{}
	for _, it := range rep.Select("balance[*]") {
		cur[it.Currency] = true
	}
	if len(cur) != 4 {
		t.Errorf("extreme 应有 4 种币种的 money: %v", cur)
	}
	long := rep.Find("alert.disk")
	if long == nil || len([]rune(long.Text)) < 120 {
		t.Errorf("extreme 的告警文本应超长: %+v", long)
	}
	if tb := rep.Find("tasks"); tb == nil || len(tb.Rows) < 20 {
		t.Errorf("extreme 的任务表应有很多行: %+v", tb)
	}
	// 其余默认数据项仍在，保证种子布局与既有小组件照常有数据。
	if rep.Find("cpu.pi") == nil || rep.Find("quota.codex.5h") == nil {
		t.Error("extreme 应保留默认数据项")
	}
}

func TestUnknownProfileFallsBackToDefault(t *testing.T) {
	rep := collectWith(t, map[string]any{"profile": "nope"})
	if n := len(rep.Select("host[*]")); n != 4 {
		t.Errorf("未知 profile 按默认处理: %d", n)
	}
}
