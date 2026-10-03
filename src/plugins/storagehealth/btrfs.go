package storagehealth

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

const (
	prefixBtrfs = "btrfs"
	prefixScrub = "scrub"
	mountsPath  = "/proc/mounts"
)

// btrfsMount 是 /proc/mounts 里的一个 btrfs 挂载。
type btrfsMount struct {
	dev, point string
}

// parseBtrfsMounts 解析 /proc/mounts，只留文件系统类型为 btrfs 的挂载。
// 挂载点里的空白按八进制转义还原（\040）。
func parseBtrfsMounts(out string) []btrfsMount {
	var res []btrfsMount
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[2] != "btrfs" {
			continue
		}
		res = append(res, btrfsMount{dev: unescapeMount(f[0]), point: unescapeMount(f[1])})
	}
	return res
}

var octalEscape = regexp.MustCompile(`\\([0-7]{3})`)

func unescapeMount(s string) string {
	return octalEscape.ReplaceAllStringFunc(s, func(m string) string {
		v, _ := strconv.ParseUint(m[1:], 8, 8)
		return string(rune(v))
	})
}

// btrfsFS 是去重后的一个文件系统：规范设备名（或解析失败时的原始设备）与代表挂载点。
type btrfsFS struct {
	point string
	dev   string // 规范设备名；解析失败时为空
	err   string // 设备解析失败的原因
}

// groupBtrfs 把挂载按规范设备名去重（同一文件系统的子卷挂载只取一个）。
// 代表挂载点与 host-metrics 一致：路径最短优先，其次字典序。
func (p *Plugin) groupBtrfs(mounts []btrfsMount) []btrfsFS {
	type group struct {
		dev, err string
		points   []string
	}
	groups := map[string]*group{}
	var order []string
	for _, m := range mounts {
		g := &group{}
		key := "raw:" + m.dev
		canon, err := sudocmd.Canonicalize(m.dev, p.evalSymlinks)
		switch {
		case err != nil:
			g.err = deviceError(err, "无法确定 Btrfs 所在设备", "cannot determine the device of this Btrfs filesystem")
		case !sudocmd.MatchDevice(sudocmd.OpBtrfsDeviceStats, canon):
			g.err = deviceError(nil, "Btrfs 设备须为 dm、md、sd、nvme、mmc 的规范设备名", "Btrfs device must be a canonical dm, md, sd, nvme or mmc device")
		default:
			key, g.dev = "dev:"+canon, canon
		}
		if old, ok := groups[key]; ok {
			g = old
		} else {
			groups[key] = g
			order = append(order, key)
		}
		g.points = append(g.points, m.point)
	}
	var out []btrfsFS
	for _, k := range order {
		g := groups[k]
		sort.Slice(g.points, func(i, j int) bool {
			if len(g.points[i]) != len(g.points[j]) {
				return len(g.points[i]) < len(g.points[j])
			}
			return g.points[i] < g.points[j]
		})
		out = append(out, btrfsFS{point: g.points[0], dev: g.dev, err: g.err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].point < out[j].point })
	return out
}

// collectBtrfs 对每个 btrfs 文件系统执行 device stats 与 scrub status。
func (p *Plugin) collectBtrfs(ctx context.Context, now time.Time, last *report.Report, c config) (checkResult, error) {
	data, err := p.readFile(mountsPath)
	if err != nil {
		return carryOrFail(last, []string{prefixBtrfs, prefixScrub}, "无法读取 /proc/mounts / Cannot read /proc/mounts: "+err.Error(), "Btrfs"), nil
	}
	filesystems := p.groupBtrfs(parseBtrfsMounts(string(data)))
	if len(filesystems) == 0 {
		return checkResult{summary: "未发现 Btrfs 文件系统 / No Btrfs filesystems"}, nil
	}
	env, envErr := p.toolEnvironment(sudocmd.Btrfs)

	var res checkResult
	var n counts
	for _, fsys := range filesystems {
		if err := ctx.Err(); err != nil {
			return checkResult{}, fmt.Errorf("检查被中断（Btrfs）/ Btrfs check interrupted: %w", err)
		}
		var stats, scrub stateResult
		switch {
		case fsys.err != "":
			stats, scrub = failedState(fsys.err), failedState(fsys.err)
		case envErr != "":
			stats, scrub = failedState(envErr), failedState(envErr)
		default:
			var err error
			stats, scrub, err = p.checkBtrfsFS(ctx, env, fsys.dev, now, c.scrubMaxAgeDays)
			if err != nil {
				return checkResult{}, err
			}
		}
		res.items = append(res.items,
			report.Item{Key: prefixBtrfs + "[" + fsys.point + "]", Type: report.TypeState, State: stats.state, Text: stats.text, Error: stats.err},
			report.Item{Key: prefixScrub + "[" + fsys.point + "]", Type: report.TypeState, State: scrub.state, Text: scrub.text, Error: scrub.err},
		)
		n.total++
		switch {
		case stats.err != "" || scrub.err != "" || isAbnormal(stats.state) || isAbnormal(scrub.state):
			n.abnormal++
		case stats.state == report.StatusUnknown && scrub.state == report.StatusUnknown:
			n.unknown++
		default:
			n.ok++
		}
	}
	res.summary = fmt.Sprintf("Btrfs %d 个：正常 %d，异常 %d，未知 %d / Btrfs filesystems: %d, OK %d, abnormal %d, unknown %d",
		n.total, n.ok, n.abnormal, n.unknown, n.total, n.ok, n.abnormal, n.unknown)
	return res, nil
}

// sysfsBtrfs 是内核导出的 Btrfs 文件系统目录，下面每个 fsid 的 devices/ 列出全部成员设备。
const sysfsBtrfs = "/sys/fs/btrfs"

// btrfsMembers 用规范设备的名字在 sysfs 里找到所属文件系统，返回全部成员的 /dev 路径（已排序）。
// 不需要 root。失败时返回原因，调用方退回只检查传入的设备。
func (p *Plugin) btrfsMembers(dev string) ([]string, string) {
	base := path.Base(dev)
	fsids, err := p.readDir(sysfsBtrfs)
	if err != nil {
		return nil, "无法读取 " + sysfsBtrfs + "：" + err.Error()
	}
	sort.Strings(fsids)
	for _, id := range fsids {
		names, err := p.readDir(path.Join(sysfsBtrfs, id, "devices"))
		if err != nil || !slices.Contains(names, base) {
			continue
		}
		sorted := slices.Clone(names)
		sort.Strings(sorted)
		members := make([]string, 0, len(sorted))
		for _, n := range sorted {
			members = append(members, "/dev/"+n)
		}
		return members, ""
	}
	return nil, "在 " + sysfsBtrfs + " 下找不到 " + base + " 所属的文件系统"
}

// stateRank 给 state 条目的合并排序：critical > warning > unknown > ok。
// 合并多个成员时 unknown 高于 ok，保证拿不到结果的成员不会被其他成员的 ok 掩盖。
func stateRank(s report.Status) int {
	switch s {
	case report.StatusCritical:
		return 3
	case report.StatusWarning:
		return 2
	case report.StatusOK:
		return 0
	}
	return 1
}

// mergeMembers 把各成员的同类结果合成一条：状态取最差；有失败成员时带 error 并写明是哪个成员；
// 多个成员时文字逐个列出（含各自 scrub 时间）。
func mergeMembers(members []string, rs []stateResult, tail string) stateResult {
	out := stateResult{state: report.StatusOK}
	var texts, errs []string
	for i, r := range rs {
		if stateRank(r.state) > stateRank(out.state) {
			out.state = r.state
		}
		if r.err != "" {
			errs = append(errs, members[i]+"："+r.err)
			continue
		}
		if len(rs) == 1 {
			texts = append(texts, r.text)
		} else {
			texts = append(texts, members[i]+"："+r.text)
		}
	}
	out.err = strings.Join(errs, "；")
	out.text = strings.Join(texts, "；")
	if len(members) > 1 {
		out.text = "已检查成员 " + strings.Join(members, "、") + " / Checked members；" + out.text
	}
	if tail != "" {
		out.text += tail
	}
	return out
}

// checkBtrfsFS 检查一个文件系统的全部成员设备并汇总。无法枚举成员时退回只检查 dev，
// 文字注明「仅检查了」，且把本会判 ok 的结果改为 unknown。
func (p *Plugin) checkBtrfsFS(ctx context.Context, env toolEnv, dev string, now time.Time, maxAgeDays float64) (stateResult, stateResult, error) {
	interrupted := func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("检查被中断（Btrfs）/ Btrfs check interrupted: %w", err)
		}
		return nil
	}
	members, reason := p.btrfsMembers(dev)
	var tail string
	degraded := reason != ""
	if degraded {
		members = []string{dev}
		tail = "；仅检查了 " + dev + "，无法枚举全部成员（" + reason + "）/ Only checked " + dev + ", cannot enumerate all members"
	}
	stats := make([]stateResult, len(members))
	scrub := make([]stateResult, len(members))
	for i, m := range members {
		canon, err := sudocmd.Canonicalize(m, p.evalSymlinks)
		if err != nil || !sudocmd.MatchDevice(sudocmd.OpBtrfsDeviceStats, canon) || !sudocmd.MatchDevice(sudocmd.OpBtrfsScrubStatus, canon) {
			msg := "成员设备不在 sudoers 白名单范围内，未检查 / Member device is outside the sudoers allowlist, not checked"
			stats[i], scrub[i] = failedState(msg), failedState(msg)
			continue
		}
		stats[i] = p.btrfsDeviceStats(ctx, env, canon)
		if err := interrupted(); err != nil {
			return stateResult{}, stateResult{}, err
		}
		scrub[i] = p.btrfsScrub(ctx, env, canon, now, maxAgeDays)
		if err := interrupted(); err != nil {
			return stateResult{}, stateResult{}, err
		}
	}
	st, sc := mergeMembers(members, stats, tail), mergeMembers(members, scrub, tail)
	if degraded {
		for _, r := range []*stateResult{&st, &sc} {
			if r.state == report.StatusOK {
				r.state = report.StatusUnknown
			}
		}
	}
	return st, sc, nil
}

func isAbnormal(s report.Status) bool {
	return s == report.StatusWarning || s == report.StatusCritical
}

// stateResult 是一条 state 数据项的内容。
type stateResult struct {
	state     report.Status
	text, err string
}

func failedState(msg string) stateResult {
	return stateResult{state: report.StatusUnknown, err: msg}
}

// runBtrfs 执行一条 btrfs 命令，失败时返回双语原因。
func (p *Plugin) runBtrfs(ctx context.Context, env toolEnv, op sudocmd.Op, dev, what string) (string, string) {
	args, err := sudocmd.Args(op, dev)
	if err != nil {
		return "", "不支持的设备路径 / Unsupported device path: " + dev
	}
	res, err := p.runSudo(ctx, env, args)
	if err != nil {
		return "", err.Error()
	}
	if msg, ok := sudoFailure(res); ok {
		return "", msg
	}
	if res.ExitCode != 0 {
		return "", fmt.Sprintf("%s 退出码 %d：%s / %s exited with %d", what, res.ExitCode, strings.TrimSpace(res.Stderr), what, res.ExitCode)
	}
	return string(res.Stdout), ""
}

var deviceStatsRe = regexp.MustCompile(`^\[([^\]]+)\]\.(\w+)\s+(\d+)\s*$`)

// btrfsDeviceStats 汇总 device stats 的计数：任一计数非 0 为 critical。
func (p *Plugin) btrfsDeviceStats(ctx context.Context, env toolEnv, dev string) stateResult {
	out, msg := p.runBtrfs(ctx, env, sudocmd.OpBtrfsDeviceStats, dev, "btrfs device stats")
	if msg != "" {
		return failedState(msg)
	}
	var seen int
	var bad []string
	for _, line := range strings.Split(out, "\n") {
		m := deviceStatsRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		seen++
		if v, _ := strconv.ParseUint(m[3], 10, 64); v > 0 {
			bad = append(bad, fmt.Sprintf("%s %s=%s", m[1], m[2], m[3]))
		}
	}
	switch {
	case seen == 0:
		return failedState("无法解析 btrfs device stats 输出 / Cannot parse btrfs device stats output")
	case len(bad) > 0:
		return stateResult{state: report.StatusCritical, text: "设备错误计数非 0 / Device error counters non-zero：" + strings.Join(bad, "，")}
	}
	return stateResult{state: report.StatusOK, text: "无设备错误 / No device errors"}
}

// scrubInfo 是 scrub status 解析出的上次 scrub 信息。
type scrubInfo struct {
	never    bool
	started  string // 原文时间
	status   string // finished / running / aborted / interrupted / ...
	errors   int    // 错误总数
	errKnown bool
	errText  string
}

var (
	scrubKV         = regexp.MustCompile(`(\w+)=(\d+)`)
	oldScrubStarted = regexp.MustCompile(`scrub (?:started|resumed) at (.+?)(?:,? and (finished|was aborted|was interrupted)\b| and was (aborted|interrupted)\b|,? running for\b|$)`)
	oldScrubErrors  = regexp.MustCompile(`total bytes scrubbed: .+? with (\d+) errors?`)
)

// parseScrubStatus 解析 btrfs scrub status，兼容 btrfs-progs 5.x/6.x 的 `Key: value` 格式
// 与 4.x 的 `scrub started at …` 格式；`no stats available` 表示从未执行。
func parseScrubStatus(out string) (scrubInfo, bool) {
	var si scrubInfo
	lower := strings.ToLower(out)
	if strings.Contains(lower, "no stats available") {
		si.never = true
		return si, true
	}
	var found bool
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "Scrub started:"):
			si.started = strings.TrimSpace(strings.TrimPrefix(line, "Scrub started:"))
			found = true
		case strings.HasPrefix(line, "Scrub resumed:") && si.started == "":
			si.started = strings.TrimSpace(strings.TrimPrefix(line, "Scrub resumed:"))
			found = true
		case strings.HasPrefix(line, "Status:"):
			si.status = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "Status:")))
			found = true
		case strings.HasPrefix(line, "Error summary:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "Error summary:"))
			si.errKnown = true
			if strings.EqualFold(v, "no errors found") {
				break
			}
			si.errText = v
			for _, kv := range scrubKV.FindAllStringSubmatch(v, -1) {
				n, _ := strconv.Atoi(kv[2])
				si.errors += n
			}
			if si.errors == 0 {
				si.errors = 1 // 有摘要但读不出计数，按有错误处理
			}
		case strings.Contains(line, "scrub started at") || strings.Contains(line, "scrub resumed at"):
			if m := oldScrubStarted.FindStringSubmatch(line); m != nil {
				si.started = strings.TrimSpace(m[1])
				switch {
				case m[2] == "finished":
					si.status = "finished"
				case m[2] == "was aborted" || m[3] == "aborted":
					si.status = "aborted"
				case m[2] == "was interrupted" || m[3] == "interrupted":
					si.status = "interrupted"
				case strings.Contains(line, "running for"):
					si.status = "running"
				}
				found = true
			}
		case strings.Contains(line, "total bytes scrubbed:"):
			if m := oldScrubErrors.FindStringSubmatch(line); m != nil {
				si.errors, _ = strconv.Atoi(m[1])
				si.errKnown = true
				found = true
			}
		case strings.HasPrefix(line, "error details:"):
			si.errText = strings.TrimSpace(strings.TrimPrefix(line, "error details:"))
		}
	}
	return si, found
}

// parseScrubTime 把 scrub 输出里形如 `Tue Sep 30 03:00:01 2025` 的本地时间按 loc 解析。
func parseScrubTime(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(strings.Fields(s), " "), loc)
	return t, err == nil
}

// btrfsScrub 判定上次 scrub：有错误为 critical；中止、从未执行或超过 maxAgeDays 天为 warning。
// 时间取 scrub 开始时间，按 p.loc 解析，天数用传入的 now 计算。
func (p *Plugin) btrfsScrub(ctx context.Context, env toolEnv, dev string, now time.Time, maxAgeDays float64) stateResult {
	out, msg := p.runBtrfs(ctx, env, sudocmd.OpBtrfsScrubStatus, dev, "btrfs scrub status")
	if msg != "" {
		return failedState(msg)
	}
	si, ok := parseScrubStatus(out)
	if !ok {
		return failedState("无法解析 btrfs scrub status 输出 / Cannot parse btrfs scrub status output")
	}
	if si.never {
		return stateResult{state: report.StatusWarning, text: "从未执行 scrub / Scrub has never run"}
	}
	var notes []string
	st := report.StatusOK
	if si.errors > 0 {
		st = report.StatusCritical
		notes = append(notes, fmt.Sprintf("scrub 发现 %d 个错误 / Scrub found %d errors", si.errors, si.errors))
		if si.errText != "" {
			notes[len(notes)-1] += "（" + si.errText + "）"
		}
	}
	switch si.status {
	case "aborted", "interrupted":
		st = worse(st, report.StatusWarning)
		notes = append(notes, "上次 scrub 未完成（"+si.status+"）/ Last scrub did not finish ("+si.status+")")
	case "running":
		notes = append(notes, "scrub 进行中 / Scrub running")
	}
	started, parsed := parseScrubTime(si.started, p.loc)
	switch {
	case !parsed:
		notes = append(notes, "无法解析 scrub 时间 / Cannot parse scrub time")
		if st == report.StatusOK {
			st = report.StatusUnknown
		}
	default:
		age := now.Sub(started)
		days := age.Hours() / 24
		if age > time.Duration(maxAgeDays*24*float64(time.Hour)) {
			st = worse(st, report.StatusWarning)
			notes = append(notes, fmt.Sprintf("上次 scrub %s，已 %.0f 天，超过 %.0f 天 / Last scrub %s, %.0f days ago, over %.0f days", started.Format("2006-01-02 15:04"), days, maxAgeDays, started.Format("2006-01-02 15:04"), days, maxAgeDays))
		} else if st == report.StatusOK {
			notes = append(notes, fmt.Sprintf("上次 scrub %s，无错误 / Last scrub %s, no errors", started.Format("2006-01-02 15:04"), started.Format("2006-01-02 15:04")))
		}
	}
	if !si.errKnown && st == report.StatusOK {
		st = report.StatusUnknown
		notes = append(notes, "缺少错误统计 / Error summary missing")
	}
	return stateResult{state: st, text: strings.Join(notes, "；")}
}
