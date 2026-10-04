package storagehealth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

const (
	prefixRaid     = "raid"
	prefixRaidSync = "raid_sync"
	mdstatPath     = "/proc/mdstat"
)

// mdMember 是阵列成员；flag 为 mdstat 括号里的标记（F 失效、S 备用、W 写优先、R 替换）。
type mdMember struct {
	name string
	flag string
}

// mdArray 是 /proc/mdstat 里的一个阵列。
type mdArray struct {
	name    string // md0
	active  bool
	mode    string // active 后括号里的说明，如 auto-read-only
	level   string // raid1；inactive 时为空
	members []mdMember

	hasCounts      bool
	total, working int    // [n/m]
	pattern        string // [UU_]
	syncAction     string // resync / recovery / reshape / check / repair
	syncPercent    *float64
	syncPending    string // DELAYED / PENDING
	syncFinish     string
}

var (
	mdHeadRe    = regexp.MustCompile(`^(md\d+)\s*:\s*(active|inactive)(?:\s+\(([^)]*)\))?\s*(.*)$`)
	mdMemberRe  = regexp.MustCompile(`^([A-Za-z0-9_./-]+)\[(\d+)\](?:\((\w)\))?$`)
	mdCountsRe  = regexp.MustCompile(`\[(\d+)/(\d+)\]\s*\[([U_]+)\]`)
	mdSyncRe    = regexp.MustCompile(`\b(resync|recovery|reshape|check|repair)\s*=\s*(\d+(?:\.\d+)?)%`)
	mdPendingRe = regexp.MustCompile(`\b(resync|recovery|reshape|check|repair)\s*=\s*(DELAYED|PENDING)\b`)
	mdFinishRe  = regexp.MustCompile(`finish=(\S+)`)
)

// parseMdstat 解析 /proc/mdstat。只认 md<数字> 命名的阵列；Personalities、unused devices 等行忽略。
func parseMdstat(out string) []mdArray {
	var arrays []mdArray
	cur := -1
	for _, line := range strings.Split(out, "\n") {
		if m := mdHeadRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil && !strings.HasPrefix(line, " ") {
			a := mdArray{name: m[1], active: m[2] == "active", mode: m[3]}
			fields := strings.Fields(m[4])
			for i, f := range fields {
				if mm := mdMemberRe.FindStringSubmatch(f); mm != nil {
					a.members = append(a.members, mdMember{name: mm[1], flag: mm[3]})
					continue
				}
				if i == 0 && a.active {
					a.level = f
				}
			}
			arrays = append(arrays, a)
			cur = len(arrays) - 1
			continue
		}
		if strings.TrimSpace(line) == "" {
			cur = -1
			continue
		}
		if cur < 0 {
			continue
		}
		a := &arrays[cur]
		if m := mdCountsRe.FindStringSubmatch(line); m != nil {
			a.hasCounts = true
			a.total, _ = strconv.Atoi(m[1])
			a.working, _ = strconv.Atoi(m[2])
			a.pattern = m[3]
		}
		if m := mdSyncRe.FindStringSubmatch(line); m != nil {
			a.syncAction = m[1]
			if v, err := strconv.ParseFloat(m[2], 64); err == nil {
				a.syncPercent = &v
			}
			if f := mdFinishRe.FindStringSubmatch(line); f != nil {
				a.syncFinish = f[1]
			}
		} else if m := mdPendingRe.FindStringSubmatch(line); m != nil {
			a.syncAction = m[1]
			a.syncPending = m[2]
		}
	}
	return arrays
}

func (a mdArray) failedMembers() []string {
	var out []string
	for _, m := range a.members {
		if m.flag == "F" {
			out = append(out, m.name)
		}
	}
	return out
}

func (a mdArray) degraded() bool {
	return a.hasCounts && (a.working < a.total || strings.Contains(a.pattern, "_"))
}

// rebuilding 报告阵列是否正在重建或重新同步（check 只是例行巡检，不算）。
func (a mdArray) rebuilding() bool {
	switch a.syncAction {
	case "resync", "recovery", "reshape", "repair":
		return true
	}
	return false
}

func (a mdArray) syncText() string {
	if a.syncAction == "" {
		return ""
	}
	switch {
	case a.syncPending != "":
		return fmt.Sprintf("%s 排队中 / %s %s", a.syncAction, a.syncAction, strings.ToLower(a.syncPending))
	case a.syncPercent != nil:
		t := fmt.Sprintf("%s %.1f%%", a.syncAction, *a.syncPercent)
		if a.syncFinish != "" {
			return fmt.Sprintf("%s，预计剩余 %s / %s, finish in %s", t, a.syncFinish, t, a.syncFinish)
		}
		return t
	}
	return a.syncAction
}

// assess 按 mdstat 判定阵列状态与说明：未激活或有失效成员为 critical；降级为 critical，
// 但正在重建且没有失效成员时为 warning；重新同步为 warning；例行 check 与正常为 ok。
func (a mdArray) assess() (report.Status, string) {
	var notes []string
	st := report.StatusOK
	if a.level != "" {
		notes = append(notes, strings.ToUpper(a.level))
	}
	if !a.active {
		return report.StatusCritical, "阵列未激活 / Array inactive"
	}
	if f := a.failedMembers(); len(f) > 0 {
		st = report.StatusCritical
		notes = append(notes, fmt.Sprintf("成员失效 %s / Failed members %s", strings.Join(f, ","), strings.Join(f, ",")))
	}
	if a.degraded() {
		desc := fmt.Sprintf("降级 %d/%d [%s] / Degraded %d/%d [%s]", a.working, a.total, a.pattern, a.working, a.total, a.pattern)
		notes = append(notes, desc)
		if a.rebuilding() && st != report.StatusCritical {
			st = report.StatusWarning
		} else {
			st = report.StatusCritical
		}
	}
	if a.syncAction != "" {
		notes = append(notes, a.syncText())
		if a.rebuilding() && st == report.StatusOK {
			st = report.StatusWarning
		}
	}
	if st == report.StatusOK {
		if a.hasCounts {
			notes = append(notes, fmt.Sprintf("正常 %d/%d / Healthy %d/%d", a.working, a.total, a.working, a.total))
		} else {
			notes = append(notes, "正常 / Healthy")
		}
	}
	return st, strings.Join(notes, "，")
}

// mdDetail 是 mdadm --detail 里用到的字段。
type mdDetail struct {
	state, name, failedDevices string
}

// parseMdDetail 解析 mdadm --detail 的「键 : 值」行；没有 State 行时第二个返回值为 false。
func parseMdDetail(out string) (mdDetail, bool) {
	var d mdDetail
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "State":
			if d.state == "" {
				d.state = v
			}
		case "Name":
			if d.name == "" {
				if f := strings.Fields(v); len(f) > 0 {
					d.name = f[0]
				}
			}
		case "Failed Devices":
			d.failedDevices = v
		}
	}
	return d, d.state != ""
}

// status 按 mdadm 的 State 文字给出判定：FAILED、Not Started 为 critical；降级为 critical，
// 同时在 recovering/resyncing/reshaping 为 warning；其余为 ok。
func (d mdDetail) status() report.Status {
	s := strings.ToLower(d.state)
	switch {
	case strings.Contains(s, "failed"), strings.Contains(s, "not started"):
		return report.StatusCritical
	case strings.Contains(s, "degraded"):
		if strings.Contains(s, "recovering") || strings.Contains(s, "resyncing") || strings.Contains(s, "reshaping") {
			return report.StatusWarning
		}
		return report.StatusCritical
	case strings.Contains(s, "recovering"), strings.Contains(s, "resyncing"), strings.Contains(s, "reshaping"):
		return report.StatusWarning
	}
	if n, err := strconv.Atoi(d.failedDevices); err == nil && n > 0 {
		return report.StatusWarning
	}
	return report.StatusOK
}

// collectRAID 读 /proc/mdstat（无需权限），再逐阵列经 sudo mdadm --detail 补充细节；
// 详情失败时保留 mdstat 的结论，只在文字里注明并让整体至少 warning。
func (p *Plugin) collectRAID(ctx context.Context, last *report.Report) (checkResult, error) {
	data, err := p.readFile(mdstatPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return checkResult{summary: "未发现 md 阵列 / No md arrays"}, nil
	case err != nil:
		return carryOrFail(last, []string{prefixRaid, prefixRaidSync}, "无法读取 /proc/mdstat / Cannot read /proc/mdstat: "+err.Error(), "RAID"), nil
	}
	arrays := parseMdstat(string(data))
	env, envErr := p.toolEnvironment(sudocmd.Mdadm)

	var res checkResult
	var n counts
	for _, a := range arrays {
		if err := ctx.Err(); err != nil {
			return checkResult{}, fmt.Errorf("RAID 检查被中断 / RAID check interrupted: %w", err)
		}
		dev := "/dev/" + a.name
		st, text := a.assess()
		var detailNote string
		if envErr != "" {
			detailNote = envErr
		} else if d, note := p.mdadmDetail(ctx, env, dev); note != "" {
			detailNote = note
			if ctx.Err() != nil {
				return checkResult{}, fmt.Errorf("RAID 检查被中断 / RAID check interrupted: %w", ctx.Err())
			}
		} else {
			if d.name != "" {
				text += "；阵列名 " + d.name + " / array name " + d.name
			}
			text += "；mdadm: " + d.state
			st = worse(st, d.status())
		}
		if detailNote != "" {
			text += "；阵列详情不可用 / Array detail unavailable（" + detailNote + "）"
			res.partial = true
		}
		res.items = append(res.items, report.Item{Key: prefixRaid + "[" + dev + "]", Type: report.TypeState, State: st, Text: text})
		if a.syncAction != "" && a.syncPercent != nil {
			res.items = append(res.items, report.Item{Key: prefixRaidSync + "[" + dev + "]", Type: report.TypeGauge, Unit: "%", Value: a.syncPercent, Min: ptr(0), Max: ptr(100)})
		}
		n.total++
		switch st {
		case report.StatusOK:
			n.ok++
		case report.StatusWarning, report.StatusCritical:
			n.abnormal++
		default:
			n.unknown++
		}
	}
	res.summary = n.arrayText()
	if res.partial {
		res.summary += "（部分阵列详情不可用 / some array details unavailable）"
	}
	return res, nil
}

func (n counts) arrayText() string {
	if n.total == 0 {
		return "未发现 md 阵列 / No md arrays"
	}
	return fmt.Sprintf("阵列 %d 个：正常 %d，异常 %d，未知 %d / Arrays: %d, OK %d, abnormal %d, unknown %d",
		n.total, n.ok, n.abnormal, n.unknown, n.total, n.ok, n.abnormal, n.unknown)
}

// mdadmDetail 执行 sudo mdadm --detail；失败时第二个返回值为双语原因。
func (p *Plugin) mdadmDetail(ctx context.Context, env toolEnv, dev string) (mdDetail, string) {
	args, err := sudocmd.Args(sudocmd.OpMdadmDetail, dev)
	if err != nil {
		return mdDetail{}, "不支持的设备 / Unsupported device: " + dev
	}
	res, err := p.runSudo(ctx, env, args)
	if err != nil {
		return mdDetail{}, err.Error()
	}
	if msg, ok := sudoFailure(res); ok {
		return mdDetail{}, msg
	}
	if res.ExitCode != 0 {
		return mdDetail{}, fmt.Sprintf("mdadm 退出码 %d：%s / mdadm exited with %d", res.ExitCode, strings.TrimSpace(res.Stderr), res.ExitCode)
	}
	d, ok := parseMdDetail(string(res.Stdout))
	if !ok {
		return mdDetail{}, "无法解析 mdadm 输出 / Cannot parse mdadm output"
	}
	return d, ""
}

// worse 取更严重的状态（unknown 视为最轻）。
func worse(a, b report.Status) report.Status {
	if a == report.StatusUnknown {
		return b
	}
	return raise(a, b)
}

// carryOrFail 在整类检查读不到数据时，沿用 last 里这类对象的键并标为 unknown 加 error，
// 避免对象被当成「消失」而关闭告警；last 里没有可沿用的键时返回 failed。
func carryOrFail(last *report.Report, prefixes []string, msg, label string) checkResult {
	var items []report.Item
	seen := map[string]bool{}
	for _, pre := range prefixes {
		for _, m := range lastMembers(last, pre) {
			k := pre + "[" + m + "]"
			if seen[k] {
				continue
			}
			seen[k] = true
			old, _ := lastItem(last, k)
			if old.Type == report.TypeState {
				items = append(items, report.Item{Key: k, Type: report.TypeState, State: report.StatusUnknown, Error: msg})
			} else {
				items = append(items, report.Item{Key: k, Type: old.Type, Unit: old.Unit, Min: old.Min, Max: old.Max})
			}
		}
	}
	if len(items) == 0 {
		return checkResult{failed: msg}
	}
	return checkResult{items: items, summary: label + " 检查失败，沿用上次的对象 / check failed, keeping known objects: " + msg}
}
