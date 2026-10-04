// Package storagehealth 是内置的存储健康插件：经 sudo 只读执行 smartctl、mdadm、btrfs，
// 汇总磁盘 SMART、mdadm 阵列与 Btrfs 的健康状态。只在 Linux agent 上运行；
// 需要 root 的命令一律经 sudocmd 生成的 argv 调用，与 install 写入的 sudoers 白名单逐字一致。
package storagehealth

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

//go:embed plugin.yaml
var manifestYAML []byte

const errLinuxOnly = "此插件仅支持 Linux / Linux only"

// defaultCmdTimeout 是单条 sudo 命令的时限：某块盘卡住时仍能交出其余结果。
const defaultCmdTimeout = 30 * time.Second

// Plugin 是 storage-health 的 Source 实现。
type Plugin struct {
	m            *manifest.Manifest
	runner       cmdexec.Runner
	goos         string
	lookPath     func(string) (string, error)
	resolve      func(string) (string, error)
	evalSymlinks func(string) (string, error)
	readFile     func(string) ([]byte, error)
	readDir      func(string) ([]string, error) // 返回目录下的条目名，用于从 sysfs 枚举 Btrfs 成员设备
	loc          *time.Location                 // btrfs scrub 输出里本地时间的时区
	cmdTimeout   time.Duration
}

func init() {
	runtime.Register(New(cmdexec.New()))
}

// New 构造插件，runner 为外部命令执行器。
func New(runner cmdexec.Runner) *Plugin {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("storage-health 内置 manifest 不合法: " + err.Error())
	}
	return &Plugin{
		m:            m,
		runner:       runner,
		goos:         goruntime.GOOS,
		lookPath:     exec.LookPath,
		resolve:      func(name string) (string, error) { return sudocmd.Resolve(sudocmd.SystemFS(), name) },
		evalSymlinks: filepath.EvalSymlinks,
		readFile:     os.ReadFile,
		readDir:      readDirNames,
		loc:          time.Local,
		cmdTimeout:   defaultCmdTimeout,
	}
}

// Manifest 实现 runtime.Source。
func (p *Plugin) Manifest() *manifest.Manifest { return p.m }

// config 是整理后的实例配置。
type config struct {
	smart, raid, btrfs bool
	smartDevices       []string
	satDevices         []string
	tempWarn, tempCrit float64
	nvmeWarn, nvmeCrit float64
	scrubMaxAgeDays    float64
}

func readConfig(c map[string]any) config {
	return config{
		smart:           cfg.Bool(c, "smart", true),
		raid:            cfg.Bool(c, "raid", true),
		btrfs:           cfg.Bool(c, "btrfs", true),
		smartDevices:    cfg.List(c, "smart_devices"),
		satDevices:      cfg.List(c, "sat_devices"),
		tempWarn:        cfg.Number(c, "temp_warn", 50),
		tempCrit:        cfg.Number(c, "temp_critical", 60),
		nvmeWarn:        cfg.Number(c, "nvme_temp_warn", 70),
		nvmeCrit:        cfg.Number(c, "nvme_temp_critical", 80),
		scrubMaxAgeDays: cfg.Number(c, "scrub_max_age_days", 35),
	}
}

// checkResult 是一类检查（SMART、RAID、Btrfs）的结果。
type checkResult struct {
	items   []report.Item
	summary string
	failed  string // 非空表示整类检查没有拿到任何结果（没有任何条目），内容为原因
	partial bool   // 条目之外还有部分失败（如 mdadm 详情不可用），整体至少 warning
}

// Collect 实现 runtime.Source。非 Linux、ctx 结束、或所有启用的检查都拿不到任何结果时返回 error；
// 其余情况（某项检查失败、单块盘或单条命令失败）都产出报告，失败部分条目带 error，整体至少 warning。
func (p *Plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	if p.goos != "linux" {
		return nil, errors.New(errLinuxOnly)
	}
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	c := readConfig(in.Config)

	var results []checkResult
	run := func(enabled bool, check func() (checkResult, error)) error {
		if !enabled {
			return nil
		}
		r, err := check()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("存储检查被中断 / Storage check interrupted: %w", err)
		}
		results = append(results, r)
		return nil
	}
	for _, step := range []struct {
		enabled bool
		check   func() (checkResult, error)
	}{
		{c.smart, func() (checkResult, error) { return p.collectSMART(ctx, in.Last, c) }},
		{c.raid, func() (checkResult, error) { return p.collectRAID(ctx, in.Last) }},
		{c.btrfs, func() (checkResult, error) { return p.collectBtrfs(ctx, clk.Now(), in.Last, c) }},
	} {
		if err := run(step.enabled, step.check); err != nil {
			return nil, err
		}
	}

	rep := &report.Report{}
	var parts, failures []string
	partial := false
	for _, r := range results {
		rep.Items = append(rep.Items, r.items...)
		partial = partial || r.partial
		if r.failed != "" {
			failures = append(failures, r.failed)
			parts = append(parts, r.failed)
			continue
		}
		parts = append(parts, r.summary)
	}
	if len(results) > 0 && len(failures) == len(results) {
		return nil, errors.New(strings.Join(failures, "；"))
	}
	rep.Status = overallStatus(rep.Items, len(failures) > 0 || partial)
	if len(parts) == 0 {
		rep.Summary = "未启用任何检查 / No checks enabled"
	} else {
		rep.Summary = strings.Join(parts, " | ")
	}
	rep.CollectedAt = clk.Now().UnixMilli()
	return rep, nil
}

// overallStatus 取所有 state 条目中最严重者；有条目 error 或检查部分失败时至少 warning。
// 只要有 state 条目且全部为 unknown（没有条目 error、没有失败），整体取 unknown，不报 ok。
func overallStatus(items []report.Item, degraded bool) report.Status {
	st := report.StatusOK
	states, unknown := 0, 0
	for _, it := range items {
		if it.Type == report.TypeState {
			states++
			if it.State == report.StatusUnknown {
				unknown++
			}
			st = raise(st, it.State)
		}
		if it.Error != "" {
			degraded = true
		}
	}
	if degraded {
		st = raise(st, report.StatusWarning)
	}
	if st == report.StatusOK && states > 0 && unknown == states {
		return report.StatusUnknown
	}
	return st
}

// raise 按 ok < warning < critical 取更严重者；unknown 不抬高整体状态。
func raise(cur, s report.Status) report.Status {
	rank := map[report.Status]int{report.StatusOK: 0, report.StatusWarning: 1, report.StatusCritical: 2}
	if r, ok := rank[s]; ok && r > rank[cur] {
		return s
	}
	return cur
}

// readDirNames 返回目录下的条目名（含符号链接条目，不跟随）。
func readDirNames(name string) ([]string, error) {
	ents, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}
