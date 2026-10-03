package storagehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

// 数据项前缀。
const (
	prefixSmart   = "smart"
	prefixTemp    = "smart_temp"
	prefixRealloc = "smart_realloc"
	prefixPending = "smart_pending"
	prefixHours   = "smart_hours"
)

// subPrefixes 是每块盘 smart[<设备>] 之外的数值项前缀。
var subPrefixes = []string{prefixTemp, prefixRealloc, prefixPending, prefixHours}

// smartctl 退出位（smartmontools smartctl.h）。
const (
	exitCmdLine    = 1 << 0 // 命令行解析失败
	exitDevOpen    = 1 << 1 // 打开设备失败；-n standby 跳过休眠盘时也用这一位
	exitSmartCmd   = 1 << 2 // 某条 SMART 命令失败或校验和错误
	exitFailing    = 1 << 3 // SMART STATUS 判定 FAILING
	exitPrefail    = 1 << 4 // 有预失效属性低于等于阈值
	exitPastThresh = 1 << 5 // 有属性曾经低于等于阈值
	exitErrLog     = 1 << 6 // 错误日志有记录
	exitSelfTest   = 1 << 7 // 自检日志有错误
)

const (
	protoATA   = "ata"
	protoNVMe  = "nvme"
	protoOther = "other"
)

// toolEnv 是经 sudo 执行某个命令所需的绝对路径。
type toolEnv struct {
	sudo, path, name string
}

// toolEnvironment 解析 sudo 与命令 name（smartctl、mdadm、btrfs）；失败时返回面向用户的双语原因。
func (p *Plugin) toolEnvironment(name string) (toolEnv, string) {
	sudo, err := p.lookPath("sudo")
	if err != nil {
		return toolEnv{}, "未安装 sudo / sudo is not installed"
	}
	path, err := p.resolve(name)
	switch {
	case errors.Is(err, sudocmd.ErrNotFound):
		pkg := map[string]string{sudocmd.Smartctl: "smartmontools", sudocmd.Mdadm: "mdadm", sudocmd.Btrfs: "btrfs-progs"}[name]
		return toolEnv{}, fmt.Sprintf("未安装 %s（%s）/ %s (%s) is not installed", name, pkg, name, pkg)
	case err != nil:
		return toolEnv{}, fmt.Sprintf("%s 的属主或权限不安全，拒绝经 sudo 执行 / %s has unsafe ownership or permissions: %s", name, name, err.Error())
	}
	return toolEnv{sudo: sudo, path: path, name: name}, ""
}

// disk 是一块待检查的盘。
type disk struct {
	key   string // smart[<key>] 的成员：配置原文或扫描到的设备名
	canon string // 规范设备名
	sat   bool
	err   string // 不执行命令、直接给出的条目错误
}

// counts 统计一类对象的数量，用于 summary。
type counts struct {
	total, ok, abnormal, unknown, standby int
}

func (n counts) smartText() string {
	if n.total == 0 {
		return "未发现磁盘 / No disks found"
	}
	return fmt.Sprintf("磁盘 %d 块：正常 %d，异常 %d，未知 %d，休眠 %d / Disks: %d, OK %d, abnormal %d, unknown %d, standby %d",
		n.total, n.ok, n.abnormal, n.unknown, n.standby, n.total, n.ok, n.abnormal, n.unknown, n.standby)
}

// collectSMART 采集全部盘的 SMART 条目。只有 ctx 结束时返回 error；
// 自动发现失败且 last 里没有可沿用的盘时没有任何条目，以 failed 说明原因，由 Collect 汇总处理。
func (p *Plugin) collectSMART(ctx context.Context, last *report.Report, c config) (checkResult, error) {
	env, envErr := p.toolEnvironment(sudocmd.Smartctl)
	var disks []disk
	if len(c.smartDevices) > 0 {
		disks = p.configuredDisks(c)
	} else {
		var discErr string
		if envErr != "" {
			discErr = envErr
		} else {
			var err error
			disks, err = p.discover(ctx, env, last)
			if ctx.Err() != nil {
				return checkResult{}, fmt.Errorf("SMART 设备发现被中断 / SMART discovery interrupted: %w", ctx.Err())
			}
			if err != nil {
				discErr = err.Error()
			}
		}
		if discErr != "" {
			// 发现失败：沿用上次的设备集合，避免盘被当成「消失」而关闭其告警。
			keys := lastMembers(last, prefixSmart)
			if len(keys) == 0 {
				return checkResult{failed: "SMART 设备发现失败 / SMART discovery failed: " + discErr}, nil
			}
			disks = nil
			for _, k := range keys {
				disks = append(disks, disk{key: k, err: discErr})
			}
		}
	}
	p.markSat(disks, c.satDevices)

	var items []report.Item
	var n counts
	for _, d := range disks {
		if err := ctx.Err(); err != nil {
			return checkResult{}, fmt.Errorf("SMART 检查被中断 / SMART check interrupted: %w", err)
		}
		var r smartResult
		switch {
		case d.err != "":
			r = errResult(d.err)
		case envErr != "":
			r = errResult(envErr)
		default:
			r = p.checkDisk(ctx, env, d, c)
			if ctx.Err() != nil {
				return checkResult{}, fmt.Errorf("SMART 检查被中断 / SMART check interrupted: %w", ctx.Err())
			}
			if d.canon != d.key && r.err == "" {
				r.text += "（" + d.canon + "）"
			}
		}
		items = append(items, r.items(d.key, last)...)
		n.total++
		switch {
		case r.standby:
			n.standby++
		case r.err != "" || r.state == report.StatusWarning || r.state == report.StatusCritical:
			n.abnormal++
		case r.state == report.StatusOK:
			n.ok++
		default:
			n.unknown++
		}
	}
	return checkResult{items: items, summary: n.smartText()}, nil
}

// configuredDisks 把用户填写的设备解析为规范设备名；解析失败或不在白名单内的条目带 error，不执行 sudo。
func (p *Plugin) configuredDisks(c config) []disk {
	var out []disk
	seen := map[string]bool{}
	for _, raw := range c.smartDevices {
		key := strings.TrimSpace(raw)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		d := disk{key: key}
		canon, err := sudocmd.Canonicalize(key, p.evalSymlinks)
		if err != nil || !sudocmd.MatchDevice(sudocmd.OpSmart, canon) {
			d.err = deviceError(err, "只支持 /dev/sdX、/dev/nvmeN 及指向它们的链接", "only /dev/sdX, /dev/nvmeN or links to them")
		}
		d.canon = canon
		out = append(out, d)
	}
	return out
}

// deviceError 区分设备路径无法解析（不存在或链接断裂）与解析后不在白名单内两种情况。
// err 是 sudocmd.Canonicalize 的结果：解析失败时它包着 EvalSymlinks 的原始错误，其余格式问题没有包装；
// zh、en 说明该用途允许的设备范围。
func deviceError(err error, zh, en string) string {
	if err != nil && errors.Unwrap(err) != nil {
		return "设备不存在或无法解析 / Device does not exist or cannot be resolved"
	}
	return "不支持的设备路径，" + zh + " / Unsupported device path; " + en
}

// markSat 把出现在「-d sat」列表里的盘（按规范设备名比对）标记为 sat。
func (p *Plugin) markSat(disks []disk, satDevices []string) {
	sat := map[string]bool{}
	for _, raw := range satDevices {
		if canon, err := sudocmd.Canonicalize(raw, p.evalSymlinks); err == nil {
			sat[canon] = true
		}
	}
	for i := range disks {
		if sat[disks[i].canon] {
			disks[i].sat = true
		}
	}
}

// discover 经 `sudo -n smartctl --scan-open` 自动发现磁盘。白名单外的设备（如 RAID 卡直通）跳过；
// 打开失败的设备只有在上次报告里出现过时才保留为带 error 的条目（读卡器之类的空设备不报）。
func (p *Plugin) discover(ctx context.Context, env toolEnv, last *report.Report) ([]disk, error) {
	args, err := sudocmd.Args(sudocmd.OpSmartScan, "")
	if err != nil {
		return nil, err
	}
	res, err := p.runSudo(ctx, env, args)
	if err != nil {
		return nil, err
	}
	if msg, ok := sudoFailure(res); ok {
		return nil, errors.New(msg)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("smartctl --scan-open 退出码 %d：%s / smartctl --scan-open exited with %d", res.ExitCode, strings.TrimSpace(res.Stderr), res.ExitCode)
	}
	known := map[string]bool{}
	for _, k := range lastMembers(last, prefixSmart) {
		known[k] = true
	}
	var out []disk
	seen := map[string]bool{}
	for _, s := range parseScan(string(res.Stdout)) {
		if seen[s.name] {
			continue
		}
		seen[s.name] = true
		sat := strings.HasPrefix(s.typ, "sat")
		op := sudocmd.OpSmart
		if sat {
			op = sudocmd.OpSmartSat
		}
		if !sudocmd.MatchDevice(op, s.name) {
			continue
		}
		d := disk{key: s.name, canon: s.name, sat: sat}
		if s.openErr != "" {
			if !known[s.name] {
				continue
			}
			d.err = "无法打开设备 / Cannot open device: " + s.openErr
		}
		out = append(out, d)
	}
	return out, nil
}

// scanDev 是 --scan-open 的一行。
type scanDev struct {
	name, typ, openErr string
}

// parseScan 解析 `smartctl --scan-open` 的文本输出：
// 正常行 `<设备> -d <类型> # <说明>`；打开失败的行以 `# ` 开头并含 `open failed: <原因>`；其余注释行忽略。
func parseScan(out string) []scanDev {
	var devs []scanDev
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var openErr string
		if strings.HasPrefix(line, "#") {
			_, msg, ok := strings.Cut(line, "open failed:")
			if !ok {
				continue
			}
			openErr = strings.TrimSpace(msg)
			line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		}
		head, _, _ := strings.Cut(line, " # ")
		fields := strings.Fields(head)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
			continue
		}
		d := scanDev{name: fields[0], openErr: openErr}
		if len(fields) >= 3 && fields[1] == "-d" {
			d.typ = fields[2]
		}
		devs = append(devs, d)
	}
	return devs
}

// runSudo 以单条命令时限执行 `sudo -n -- <命令绝对路径> <args…>`。
func (p *Plugin) runSudo(ctx context.Context, env toolEnv, args []string) (cmdexec.Result, error) {
	cctx, cancel := context.WithTimeout(ctx, p.cmdTimeout)
	defer cancel()
	res, err := p.runner.Run(cctx, env.sudo, sudocmd.SudoArgv(env.path, args)...)
	if err != nil {
		return res, describeRunErr(ctx, env.name, err)
	}
	return res, nil
}

// describeRunErr 把执行器错误转为双语原因；先排除信号终止，再区分单条命令超时。
func describeRunErr(ctx context.Context, name string, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, cmdexec.ErrSignaled):
		return fmt.Errorf("%s 被信号终止 / %s was killed by a signal: %w", name, name, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s 执行超时 / %s timed out: %w", name, name, err)
	default:
		return fmt.Errorf("执行 %s 失败 / Failed to run %s: %w", name, name, err)
	}
}

// sudoFailure 识别 `sudo -n` 自身的失败（无权限、需要密码、不在 sudoers 里）：
// 非 0 退出、stdout 不是 smartctl 的 JSON、stderr 有以「sudo: 」开头的行。smartctl、mdadm、btrfs 共用。
func sudoFailure(res cmdexec.Result) (string, bool) {
	if res.ExitCode == 0 || bytes.HasPrefix(bytes.TrimSpace(res.Stdout), []byte("{")) {
		return "", false
	}
	for _, line := range strings.Split(res.Stderr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "sudo: ") {
			return "无 sudo 权限，需要系统模式安装并写入 sudoers / No sudo permission; system-mode install with sudoers required（" + strings.TrimSpace(res.Stderr) + "）", true
		}
	}
	return "", false
}

// smartResult 是一块盘的判定结果。
type smartResult struct {
	state   report.Status
	text    string
	err     string
	standby bool
	proto   string
	temp    *float64
	realloc *float64
	pending *float64
	hours   *float64
	// carry 为 true 时不产出新数值，按 Last 沿用该盘的子项键（值为空），用于休眠与出错。
	carry bool
}

func errResult(msg string) smartResult {
	return smartResult{state: report.StatusUnknown, err: msg, carry: true}
}

// items 生成一块盘的数据项。
func (r smartResult) items(key string, last *report.Report) []report.Item {
	out := []report.Item{{Key: prefixSmart + "[" + key + "]", Type: report.TypeState, State: r.state, Text: r.text, Error: r.err}}
	if r.carry {
		for _, pre := range subPrefixes {
			k := pre + "[" + key + "]"
			if old, ok := lastItem(last, k); ok {
				out = append(out, report.Item{Key: k, Type: old.Type, Unit: old.Unit, Min: old.Min, Max: old.Max})
			}
		}
		return out
	}
	out = append(out, report.Item{Key: prefixTemp + "[" + key + "]", Type: report.TypeGauge, Unit: "°C", Value: r.temp, Min: ptr(0), Max: ptr(100)})
	if r.proto == protoATA {
		out = append(out,
			report.Item{Key: prefixRealloc + "[" + key + "]", Type: report.TypeNumber, Value: r.realloc},
			report.Item{Key: prefixPending + "[" + key + "]", Type: report.TypeNumber, Value: r.pending},
		)
	}
	out = append(out, report.Item{Key: prefixHours + "[" + key + "]", Type: report.TypeNumber, Unit: "h", Value: r.hours})
	return out
}

// checkDisk 对一块盘执行 smartctl 并判定。
func (p *Plugin) checkDisk(ctx context.Context, env toolEnv, d disk, c config) smartResult {
	op := sudocmd.OpSmart
	if d.sat {
		op = sudocmd.OpSmartSat
	}
	args, err := sudocmd.Args(op, d.canon)
	if err != nil {
		return errResult("不支持的设备路径 / Unsupported device path: " + d.canon)
	}
	res, err := p.runSudo(ctx, env, args)
	if err != nil {
		return errResult(err.Error())
	}
	if msg, ok := sudoFailure(res); ok {
		return errResult(msg)
	}
	return evaluate(res, c)
}

// smartDoc 是 smartctl -j 输出中用到的字段；全部为指针或可空，缺失即未知。
type smartDoc struct {
	Smartctl struct {
		Messages []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	Device struct {
		Protocol string `json:"protocol"`
	} `json:"device"`
	PowerMode *struct {
		Name string `json:"name"`
	} `json:"power_mode"`
	SmartStatus *struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current *float64 `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours *float64 `json:"hours"`
	} `json:"power_on_time"`
	ATA *struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value *float64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe *struct {
		CriticalWarning         *int     `json:"critical_warning"`
		AvailableSpare          *float64 `json:"available_spare"`
		AvailableSpareThreshold *float64 `json:"available_spare_threshold"`
		PercentageUsed          *float64 `json:"percentage_used"`
		MediaErrors             *float64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

// standbyMessage 匹配 smartctl 在 -n standby 跳过低功耗盘时的提示，如「Device is in STANDBY mode, exit(2)」。
var standbyMessage = regexp.MustCompile(`^Device is in (.+) mode, exit\(\d+\)$`)

func (d *smartDoc) standbyMode() string {
	for _, m := range d.Smartctl.Messages {
		if sm := standbyMessage.FindStringSubmatch(strings.TrimSpace(m.String)); sm != nil {
			return sm[1]
		}
	}
	return ""
}

func (d *smartDoc) errorMessages() string {
	var msgs []string
	for _, m := range d.Smartctl.Messages {
		if m.Severity == "error" {
			msgs = append(msgs, strings.TrimSpace(m.String))
		}
	}
	return strings.Join(msgs, "；")
}

func (d *smartDoc) protocol() string {
	switch {
	case strings.EqualFold(d.Device.Protocol, "NVMe"), d.Device.Protocol == "" && d.NVMe != nil:
		return protoNVMe
	case strings.EqualFold(d.Device.Protocol, "ATA"), d.Device.Protocol == "" && d.ATA != nil:
		return protoATA
	}
	return protoOther
}

func (d *smartDoc) ataRaw(id int) *float64 {
	if d.ATA == nil {
		return nil
	}
	for _, a := range d.ATA.Table {
		if a.ID == id {
			return a.Raw.Value
		}
	}
	return nil
}

// evaluate 解析 smartctl 输出并判定。退出位：bit0、bit1 视为命令失败（bit1 且提示休眠时为「休眠中」）；
// bit2 照常解析并注明；bit3 判 critical；bit4–7 只写进文字。
func evaluate(res cmdexec.Result, c config) smartResult {
	out := bytes.TrimSpace(res.Stdout)
	var doc smartDoc
	if !bytes.HasPrefix(out, []byte("{")) || json.Unmarshal(out, &doc) != nil {
		all := strings.ToLower(string(res.Stdout) + res.Stderr)
		if strings.Contains(all, "unrecognized option") || strings.Contains(all, "invalid option") {
			return errResult("smartctl 版本过旧，需要 7.0 及以上 / smartctl is too old (7.0+ required)")
		}
		return errResult(fmt.Sprintf("无法解析 smartctl 输出（退出码 %d）/ Cannot parse smartctl output (exit %d): %s", res.ExitCode, res.ExitCode, strings.TrimSpace(res.Stderr)))
	}
	exit := res.ExitCode
	if exit&exitDevOpen != 0 {
		if mode := doc.standbyMode(); mode != "" {
			return smartResult{state: report.StatusUnknown, text: "休眠中 / In standby（" + mode + "）", standby: true, carry: true}
		}
	}
	if exit&exitCmdLine != 0 {
		return errResult("smartctl 命令行错误 / smartctl command-line error: " + doc.errorMessages())
	}
	if exit&exitDevOpen != 0 {
		return errResult("无法打开设备 / Cannot open device: " + doc.errorMessages())
	}

	r := smartResult{state: report.StatusUnknown, proto: doc.protocol()}
	if doc.Temperature != nil {
		r.temp = doc.Temperature.Current
	}
	if doc.PowerOnTime != nil {
		r.hours = doc.PowerOnTime.Hours
	}
	var passed *bool
	if doc.SmartStatus != nil {
		passed = doc.SmartStatus.Passed
	}
	if passed != nil && *passed {
		r.state = report.StatusOK
	}
	var reasons []string
	bad := func(st report.Status, why string) {
		r.state = raise(r.state, st)
		reasons = append(reasons, why)
	}
	if (passed != nil && !*passed) || exit&exitFailing != 0 {
		bad(report.StatusCritical, "SMART 判定失败 / SMART reports FAILING")
	}

	warn, crit := c.tempWarn, c.tempCrit
	switch r.proto {
	case protoATA:
		r.realloc, r.pending = doc.ataRaw(5), doc.ataRaw(197)
		if r.pending != nil && *r.pending > 0 {
			bad(report.StatusCritical, fmt.Sprintf("待定扇区 %.0f / %.0f pending sectors", *r.pending, *r.pending))
		}
		if r.realloc != nil && *r.realloc > 0 {
			bad(report.StatusWarning, fmt.Sprintf("重映射扇区 %.0f / %.0f reallocated sectors", *r.realloc, *r.realloc))
		}
	case protoNVMe:
		warn, crit = c.nvmeWarn, c.nvmeCrit
		evaluateNVMe(&doc, bad)
	}
	if r.temp != nil {
		switch {
		case *r.temp >= crit:
			bad(report.StatusCritical, fmt.Sprintf("温度过高 %.0f°C / Temperature too high", *r.temp))
		case *r.temp >= warn:
			bad(report.StatusWarning, fmt.Sprintf("温度偏高 %.0f°C / Temperature high", *r.temp))
		}
	}

	switch {
	case len(reasons) > 0:
		r.text = strings.Join(reasons, "；")
	case r.state == report.StatusOK:
		r.text = "健康 / Healthy"
	default:
		r.text = "健康状态未知 / Health unknown"
	}
	for _, n := range exitNotes(exit) {
		r.text += "；" + n
	}
	return r
}

// evaluateNVMe 按 NVMe SMART/Health 日志判定（NVMe 没有重映射与待定扇区）：
// critical_warning 除温度位（bit1）外任一位置位、备用空间低于阈值、介质错误 > 0 判 critical；
// 只有温度位、寿命已用 ≥ 90% 判 warning。
func evaluateNVMe(doc *smartDoc, bad func(report.Status, string)) {
	n := doc.NVMe
	if n == nil {
		return
	}
	if cw := n.CriticalWarning; cw != nil && *cw != 0 {
		if *cw&^0x02 != 0 {
			bad(report.StatusCritical, fmt.Sprintf("严重警告 0x%02x / Critical warning 0x%02x", *cw, *cw))
		} else {
			bad(report.StatusWarning, "温度越限 / Temperature threshold exceeded")
		}
	}
	if n.AvailableSpare != nil && n.AvailableSpareThreshold != nil && *n.AvailableSpare < *n.AvailableSpareThreshold {
		bad(report.StatusCritical, fmt.Sprintf("备用空间 %.0f%% 低于阈值 %.0f%% / Available spare %.0f%% below threshold %.0f%%",
			*n.AvailableSpare, *n.AvailableSpareThreshold, *n.AvailableSpare, *n.AvailableSpareThreshold))
	}
	if n.MediaErrors != nil && *n.MediaErrors > 0 {
		bad(report.StatusCritical, fmt.Sprintf("介质错误 %.0f / %.0f media errors", *n.MediaErrors, *n.MediaErrors))
	}
	if n.PercentageUsed != nil && *n.PercentageUsed >= 90 {
		bad(report.StatusWarning, fmt.Sprintf("寿命已用 %.0f%% / %.0f%% endurance used", *n.PercentageUsed, *n.PercentageUsed))
	}
}

// exitNotes 返回非致命退出位的说明文字。
func exitNotes(exit int) []string {
	var notes []string
	for _, b := range []struct {
		bit  int
		text string
	}{
		{exitSmartCmd, "部分 SMART 命令失败 / Some SMART commands failed"},
		{exitPrefail, "有预失效属性低于阈值 / Pre-fail attribute at or below threshold"},
		{exitPastThresh, "有属性曾低于阈值 / Attribute was at or below threshold in the past"},
		{exitErrLog, "错误日志有记录 / Error log has entries"},
		{exitSelfTest, "自检日志有错误 / Self-test log has errors"},
	} {
		if exit&b.bit != 0 {
			notes = append(notes, b.text)
		}
	}
	return notes
}

// lastMembers 返回上次报告里前缀为 prefix 的动态成员，保持原顺序。
func lastMembers(last *report.Report, prefix string) []string {
	if last == nil {
		return nil
	}
	var out []string
	for _, it := range last.Items {
		k, err := report.ParseKey(it.Key)
		if err == nil && k.Dynamic && k.Prefix == prefix {
			out = append(out, k.Member)
		}
	}
	return out
}

func lastItem(last *report.Report, key string) (report.Item, bool) {
	if last == nil {
		return report.Item{}, false
	}
	for _, it := range last.Items {
		if it.Key == key {
			return it, true
		}
	}
	return report.Item{}, false
}

func ptr(v float64) *float64 { return &v }
