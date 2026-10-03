// Package storagehealth 是内置的存储健康插件：经 sudo 只读执行 smartctl（以及 D3b 的 mdadm、btrfs），
// 汇总磁盘 SMART、mdadm 阵列与 Btrfs 的健康状态。只在 Linux agent 上运行；
// 需要 root 的命令一律经 sudocmd 生成的 argv 调用，与 install 写入的 sudoers 白名单逐字一致。
package storagehealth

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
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
		cmdTimeout:   defaultCmdTimeout,
	}
}

// Manifest 实现 runtime.Source。
func (p *Plugin) Manifest() *manifest.Manifest { return p.m }

// config 是整理后的实例配置。
type config struct {
	smart              bool
	smartDevices       []string
	satDevices         []string
	tempWarn, tempCrit float64
	nvmeWarn, nvmeCrit float64
}

func readConfig(c map[string]any) config {
	return config{
		smart:        cfg.Bool(c, "smart", true),
		smartDevices: cfg.List(c, "smart_devices"),
		satDevices:   cfg.List(c, "sat_devices"),
		tempWarn:     cfg.Number(c, "temp_warn", 50),
		tempCrit:     cfg.Number(c, "temp_critical", 60),
		nvmeWarn:     cfg.Number(c, "nvme_temp_warn", 70),
		nvmeCrit:     cfg.Number(c, "nvme_temp_critical", 80),
	}
}

// Collect 实现 runtime.Source。非 Linux、ctx 结束、首次运行且设备发现失败时返回 error；
// 单块盘或单条命令失败落在条目 error，整体至少 warning。
func (p *Plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	if p.goos != "linux" {
		return nil, errors.New(errLinuxOnly)
	}
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	c := readConfig(in.Config)
	rep := &report.Report{Status: report.StatusOK}

	var sum smartSummary
	if c.smart {
		items, s, err := p.collectSMART(ctx, in.Last, c)
		if err != nil {
			return nil, err
		}
		rep.Items = append(rep.Items, items...)
		sum = s
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("存储检查被中断 / Storage check interrupted: %w", err)
	}

	for _, it := range rep.Items {
		if it.Type == report.TypeState {
			rep.Status = raise(rep.Status, it.State)
		}
		if it.Error != "" {
			rep.Status = raise(rep.Status, report.StatusWarning)
		}
	}
	if c.smart {
		rep.Summary = sum.String()
	} else {
		rep.Summary = "SMART 检查未启用 / SMART check disabled"
	}
	rep.CollectedAt = clk.Now().UnixMilli()
	return rep, nil
}

// raise 按 ok < warning < critical 取更严重者；unknown 不抬高整体状态。
func raise(cur, s report.Status) report.Status {
	rank := map[report.Status]int{report.StatusOK: 0, report.StatusWarning: 1, report.StatusCritical: 2}
	if r, ok := rank[s]; ok && r > rank[cur] {
		return s
	}
	return cur
}
