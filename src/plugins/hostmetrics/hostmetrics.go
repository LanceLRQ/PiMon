// Package hostmetrics 是内置的主机指标插件：CPU、负载、内存、温度、各挂载点磁盘、网速、
// 运行时长，以及树莓派的欠压与降频标志。
//
// CPU 使用率与网速都由两次采集之间的计数器差值算出，上次的计数器放在插件私有 state 里；
// 首次运行、计数器回绕或取不到时对应数据项不输出，不会用 0 冒充真实值。
package hostmetrics

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
)

//go:embed plugin.yaml
var manifestYAML []byte

// 数据项键名；磁盘为动态集合 disk[<挂载点>]。
const (
	keyCPU       = "cpu"
	keyLoad      = "load"
	keyMem       = "mem"
	keyTemp      = "temp"
	keyNetRx     = "net_rx"
	keyNetTx     = "net_tx"
	keyUptime    = "uptime"
	keyThrottled = "throttled"
	diskPrefix   = "disk["

	defaultTempWarn     = 70.0
	defaultTempCritical = 80.0
	defaultDiskWarn     = 85.0
	defaultDiskCritical = 95.0
)

// counters 是上次采集的计数器快照，序列化后存入私有 state。
type counters struct {
	At       int64   `json:"at"` // 毫秒时间戳
	Busy     float64 `json:"busy,omitempty"`
	CPUTotal float64 `json:"cpu_total,omitempty"` // CPU 累计总时间
	Rx       uint64  `json:"rx,omitempty"`
	Tx       uint64  `json:"tx,omitempty"`
	HasC     bool    `json:"has_cpu,omitempty"`
	HasN     bool    `json:"has_net,omitempty"`
}

// Plugin 是 host-metrics 的 Source 实现。
type Plugin struct {
	m *manifest.Manifest
	s Sampler
}

func init() {
	runtime.Register(New(newSystemSampler(throttleIO{run: runCommand, readFile: os.ReadFile})))
}

// New 用给定取数来源构造插件。
func New(s Sampler) *Plugin {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("host-metrics 内置 manifest 不合法: " + err.Error())
	}
	return &Plugin{m: m, s: s}
}

// Manifest 实现 runtime.Source。
func (p *Plugin) Manifest() *manifest.Manifest { return p.m }

// Collect 实现 runtime.Source。
func (p *Plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	now := clk.Now()
	rep := &report.Report{Status: report.StatusOK, CollectedAt: now.UnixMilli()}
	th := thresholds{
		tempWarn:     cfg.Number(in.Config, "temp_warn", defaultTempWarn),
		tempCritical: cfg.Number(in.Config, "temp_critical", defaultTempCritical),
		diskWarn:     cfg.Number(in.Config, "disk_warn_pct", defaultDiskWarn),
		diskCritical: cfg.Number(in.Config, "disk_critical_pct", defaultDiskCritical),
	}

	var prev counters
	if in.State != "" {
		_ = json.Unmarshal([]byte(in.State), &prev) // 状态损坏视为首次运行
	}
	cur := counters{At: now.UnixMilli()}
	elapsed := now.Sub(time.UnixMilli(prev.At)).Seconds()

	p.cpu(ctx, rep, &cur, prev)
	if v, err := p.s.Load1(ctx); err == nil {
		rep.Items = append(rep.Items, report.Item{Key: keyLoad, Type: report.TypeNumber, Value: &v})
	}
	p.memory(ctx, rep)
	p.temperature(ctx, rep, th)
	p.disks(ctx, rep, th)
	p.network(ctx, rep, &cur, prev, elapsed)
	if up, err := p.s.Uptime(ctx); err == nil {
		v := float64(up)
		rep.Items = append(rep.Items, report.Item{Key: keyUptime, Type: report.TypeNumber, Unit: "s", Value: &v})
	}
	p.throttled(ctx, rep)

	if st, err := json.Marshal(cur); err == nil {
		rep.State = string(st)
	}
	return rep, nil
}

type thresholds struct{ tempWarn, tempCritical, diskWarn, diskCritical float64 }

// cpu 用两次累计时间的差值算使用率；首次运行只记录快照。
func (p *Plugin) cpu(ctx context.Context, rep *report.Report, cur *counters, prev counters) {
	busy, total, err := p.s.CPUTimes(ctx)
	if err != nil {
		return
	}
	cur.Busy, cur.CPUTotal, cur.HasC = busy, total, true
	if !prev.HasC || total <= prev.CPUTotal || busy < prev.Busy {
		return
	}
	pct := clampPct((busy - prev.Busy) / (total - prev.CPUTotal) * 100)
	rep.Items = append(rep.Items, report.Item{
		Key: keyCPU, Type: report.TypeGauge, Unit: "%", Value: &pct, Min: ptr(0.0), Max: ptr(100.0),
	})
}

func (p *Plugin) memory(ctx context.Context, rep *report.Report) {
	used, total, err := p.s.Memory(ctx)
	if err != nil || total == 0 {
		return
	}
	pct := clampPct(float64(used) / float64(total) * 100)
	rep.Items = append(rep.Items, report.Item{
		Key: keyMem, Type: report.TypeGauge, Unit: "%", Value: &pct, Min: ptr(0.0), Max: ptr(100.0),
	})
}

// temperature 取不到（macOS、虚拟机）就不输出。
func (p *Plugin) temperature(ctx context.Context, rep *report.Report, th thresholds) {
	t, err := p.s.Temperature(ctx)
	if err != nil {
		return
	}
	rep.Items = append(rep.Items, report.Item{
		Key: keyTemp, Type: report.TypeGauge, Unit: "°C", Value: &t, Min: ptr(0.0), Max: ptr(100.0),
	})
	switch {
	case t >= th.tempCritical:
		raise(rep, report.StatusCritical, "温度过高 / Temperature too high")
	case t >= th.tempWarn:
		raise(rep, report.StatusWarning, "温度偏高 / Temperature high")
	}
}

// disks 输出 disk[<挂载点>]；同一设备只取路径最短的挂载点，单个挂载点取不到容量时只标该项错误。
func (p *Plugin) disks(ctx context.Context, rep *report.Report, th thresholds) {
	parts, err := p.s.Partitions(ctx)
	if err != nil {
		return
	}
	var cands []Partition
	for _, part := range parts {
		if realMount(part) {
			cands = append(cands, part)
		}
	}
	// 先按「最短路径优先、其次字典序」排序再去重，保留的挂载点不随 gopsutil 返回顺序漂移，
	// 否则历史会按键断成两条。同挂载点（叠加挂载）与同设备（子卷、bind 挂载）都只留一个。
	sort.Slice(cands, func(i, j int) bool {
		if len(cands[i].Mount) != len(cands[j].Mount) {
			return len(cands[i].Mount) < len(cands[j].Mount)
		}
		return cands[i].Mount < cands[j].Mount
	})
	seenMount, seenDev := map[string]bool{}, map[string]bool{}
	var kept []Partition
	for _, part := range cands {
		if seenMount[part.Mount] || (part.Device != "" && seenDev[part.Device]) {
			continue
		}
		seenMount[part.Mount], seenDev[part.Device] = true, true
		kept = append(kept, part)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Mount < kept[j].Mount })
	for _, part := range kept {
		key := diskPrefix + part.Mount + "]"
		u, err := p.s.Usage(ctx, part.Mount)
		if err != nil || u.Total == 0 {
			rep.Items = append(rep.Items, report.Item{Key: key, Type: report.TypeQuota, Error: "无法读取容量 / Cannot read usage"})
			continue
		}
		free := u.Total - min(u.Used, u.Total)
		usedPct := float64(u.Used) / float64(u.Total) * 100
		rep.Items = append(rep.Items, report.Item{
			Key: key, Type: report.TypeQuota, Unit: "B",
			Used: ptr(float64(u.Used)), Total: ptr(float64(u.Total)),
			Remaining: ptr(float64(free)), RemainingPct: ptr(clampPct(100 - usedPct)),
		})
		switch {
		case usedPct >= th.diskCritical:
			raise(rep, report.StatusCritical, part.Mount+" 空间不足 / Low disk space")
		case usedPct >= th.diskWarn:
			raise(rep, report.StatusWarning, part.Mount+" 空间偏少 / Disk space getting low")
		}
	}
}

// network 用累计字节差除以间隔算速度；计数器回绕（重启、网卡重置）或间隔异常时不输出。
func (p *Plugin) network(ctx context.Context, rep *report.Report, cur *counters, prev counters, elapsed float64) {
	rx, tx, err := p.s.NetCounters(ctx)
	if err != nil {
		return
	}
	cur.Rx, cur.Tx, cur.HasN = rx, tx, true
	if !prev.HasN || elapsed <= 0 || rx < prev.Rx || tx < prev.Tx {
		return
	}
	rxRate := float64(rx-prev.Rx) / elapsed
	txRate := float64(tx-prev.Tx) / elapsed
	rep.Items = append(rep.Items,
		report.Item{Key: keyNetRx, Type: report.TypeNumber, Unit: "B/s", Value: &rxRate},
		report.Item{Key: keyNetTx, Type: report.TypeNumber, Unit: "B/s", Value: &txRate},
	)
}

// throttled 只在读得到时输出；当前欠压或降频抬为 warning。
func (p *Plugin) throttled(ctx context.Context, rep *report.Report) {
	v, err := p.s.Throttled(ctx)
	if err != nil {
		return
	}
	st, text := throttleState(v)
	rep.Items = append(rep.Items, report.Item{Key: keyThrottled, Type: report.TypeState, State: st, Text: text})
	if st == report.StatusWarning {
		raise(rep, report.StatusWarning, "检测到欠压或降频 / Under-voltage or throttling detected")
	}
}

// raise 在 st 比当前状态更严重时抬高整体状态与摘要。
func raise(rep *report.Report, st report.Status, summary string) {
	rank := map[report.Status]int{report.StatusOK: 0, report.StatusWarning: 1, report.StatusCritical: 2}
	if rank[st] > rank[rep.Status] {
		rep.Status, rep.Summary = st, summary
	}
}

func clampPct(v float64) float64 { return min(100, max(0, v)) }

func ptr[T any](v T) *T { return &v }
