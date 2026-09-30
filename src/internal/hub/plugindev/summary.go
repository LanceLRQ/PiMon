package plugindev

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

func printSummary(out io.Writer, m *manifest.Manifest, rep *report.Report, elapsed time.Duration, secretCount int) {
	_, _ = fmt.Fprintf(out, "插件: %s v%s\n", m.ID, m.Version)
	_, _ = fmt.Fprintf(out, "状态: %s\n", rep.Status)
	if rep.Summary != "" {
		_, _ = fmt.Fprintf(out, "摘要: %s\n", rep.Summary)
	}
	_, _ = fmt.Fprintf(out, "耗时: %s\n", elapsed.Round(time.Millisecond))
	if secretCount > 0 {
		_, _ = fmt.Fprintf(out, "密钥: 已向插件传入 %d 个（不显示）\n", secretCount)
	}
	if len(rep.Items) > 0 {
		_, _ = fmt.Fprintln(out, "数据项:")
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, it := range rep.Items {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", it.Key, it.Type, mainValue(it))
		}
		_ = tw.Flush()
	}
	if len(rep.Events) > 0 {
		_, _ = fmt.Fprintf(out, "事件: %d 条\n", len(rep.Events))
	}
	for _, w := range declarationWarnings(m, rep) {
		_, _ = fmt.Fprintln(out, "警告: "+w)
	}
}

func num(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'g', 6, 64)
}

// mainValue 给出数据项的主值：默认字段的值，必要时带单位；数据项自带 error 时显示错误。
func mainValue(it report.Item) string {
	var s string
	switch it.Type {
	case report.TypeGauge, report.TypeNumber:
		s = strings.TrimSpace(num(it.Value) + " " + it.Unit)
	case report.TypeQuota:
		switch {
		case it.RemainingPct != nil:
			s = "剩余 " + num(it.RemainingPct) + "%"
		case it.Used != nil && it.Total != nil:
			s = "已用 " + num(it.Used) + " / " + num(it.Total)
		case it.Remaining != nil:
			s = "剩余 " + num(it.Remaining)
		}
	case report.TypeMoney:
		s = strings.TrimSpace(num(it.Amount) + " " + it.Currency)
	case report.TypeState:
		s = string(it.State)
		if it.Text != "" {
			s += "（" + it.Text + "）"
		}
	case report.TypeText:
		s = it.Text
	case report.TypeTable:
		s = fmt.Sprintf("%d 列 %d 行", len(it.Columns), len(it.Rows))
	}
	if it.Error != "" {
		s = strings.TrimSpace(s + " [错误: " + it.Error + "]")
	}
	if s == "" {
		s = "-"
	}
	return s
}

// declarationWarnings 对照 outputs 声明：报告了未声明的数据项、声明了固定键却没报告，都提示一下。
func declarationWarnings(m *manifest.Manifest, rep *report.Report) []string {
	var out []string
	exact := map[string]bool{}
	var keys []report.Key
	for _, o := range m.Outputs {
		k, err := report.ParseKey(o.Key)
		if err != nil {
			continue
		}
		keys = append(keys, k)
		if !k.Dynamic {
			exact[o.Key] = true
		}
	}
	reported := map[string]bool{}
	for _, it := range rep.Items {
		reported[it.Key] = true
		declared := false
		for _, k := range keys {
			if k.Matches(it.Key) {
				declared = true
				break
			}
		}
		if !declared {
			out = append(out, fmt.Sprintf("报告了未在 outputs 中声明的数据项 %q，屏幕上无法绑定它", it.Key))
		}
	}
	for _, o := range m.Outputs {
		if exact[o.Key] && !reported[o.Key] {
			out = append(out, fmt.Sprintf("outputs 声明了 %q，但本次报告里没有", o.Key))
		}
	}
	return out
}
