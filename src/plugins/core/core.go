// Package core 是「核心」内置插件：声明时钟与文本两个小组件，数据由前端合成，
// 采集始终返回 ok 的空报告。通用与聚合小组件由 hub 的小组件目录提供，不在 manifest 里声明。
package core

import (
	"context"
	_ "embed"

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
		panic("core 内置 manifest 不合法: " + err.Error())
	}
	runtime.Register(&plugin{m: m})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

func (p *plugin) Collect(context.Context, runtime.Input) (*report.Report, error) {
	return &report.Report{Status: report.StatusOK}, nil
}
