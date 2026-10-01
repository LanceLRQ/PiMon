// Package core 是「核心」内置插件的占位实现：只登记 id，采集返回 ok 的空报告。
// M1d 在此基础上补齐时钟等核心小组件的数据与小组件声明。
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
