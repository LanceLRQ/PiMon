// Package tcpcheck 是内置的 TCP 端口检测插件：连接 host:port 并立即关闭，输出是否可达与连接延迟。
//
// 端口不可达是正常的检查结果，以 critical 状态的报告呈现；配置不合法，或选了无法转发
// 原始 TCP 的 http 代理时，Collect 返回错误（否则会绕过代理直连，暴露出口 IP）。
package tcpcheck

import (
	"context"
	_ "embed"
	"errors"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/probe"
)

//go:embed plugin.yaml
var manifestYAML []byte

// 数据项键名。
const (
	keyStatus  = "status"
	keyLatency = "latency"

	defaultTimeout = 5 * time.Second
)

type plugin struct{ m *manifest.Manifest }

func init() {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("tcp-check 内置 manifest 不合法: " + err.Error())
	}
	runtime.Register(&plugin{m: m})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

func (p *plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	host := strings.TrimSpace(cfg.String(in.Config, "host"))
	if host == "" {
		return nil, errors.New("未配置 host")
	}
	port := int(cfg.Number(in.Config, "port", 0))
	if port < 1 || port > 65535 {
		return nil, errors.New("port 必须在 1–65535 之间")
	}

	rep := &report.Report{Status: report.StatusOK, CollectedAt: clk.Now().UnixMilli()}
	latency, err := probe.TCP(ctx, probe.JoinHostPort(host, port), in.Proxy, cfg.Duration(in.Config, "timeout", defaultTimeout), clk)
	switch {
	case errors.Is(err, proxy.ErrRawTCPUnsupported):
		return nil, errors.New("tcp-check 只能选 socks5 或 socks5h 代理，http 代理无法转发原始 TCP")
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case err != nil:
		// 连不上：延迟未知，不输出。
		rep.Status = report.StatusCritical
		rep.Summary = "端口不可达 / Unreachable: " + err.Error()
		rep.Items = []report.Item{{Key: keyStatus, Type: report.TypeState, State: report.StatusCritical, Text: "closed"}}
		return rep, nil
	}
	ms := float64(latency) / float64(time.Millisecond)
	rep.Items = []report.Item{
		{Key: keyStatus, Type: report.TypeState, State: report.StatusOK, Text: "open"},
		{Key: keyLatency, Type: report.TypeNumber, Unit: "ms", Value: &ms},
	}
	return rep, nil
}
