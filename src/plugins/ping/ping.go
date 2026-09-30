// Package ping 是内置的 ICMP 延迟与丢包检测插件。
//
// 使用非特权 ICMP（Linux 上依赖 net.ipv4.ping_group_range），不需要 root 或 CAP_NET_RAW。
// ICMP 收发由 Pinger 接口承担，测试用替身；目标不通是正常的检查结果（critical 报告），
// 只有配置错误、权限不足、上层 ctx 结束才返回错误。
package ping

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
)

//go:embed plugin.yaml
var manifestYAML []byte

// 数据项键名与配置默认值、上限。
const (
	keyLatency = "latency"
	keyLoss    = "loss"

	defaultCount   = 4
	maxCount       = 10
	defaultTimeout = 2 * time.Second
)

var (
	// ErrPermission 表示系统不允许创建非特权 ICMP 套接字。
	ErrPermission = errors.New("没有非特权 ICMP 权限 / No unprivileged ICMP permission")
	// ErrResolve 表示主机名无法解析为 IPv4 地址。
	ErrResolve = errors.New("无法解析主机 / Cannot resolve host")
)

// Result 是一轮探测的结果：发出的包数与收到应答的往返时间。
type Result struct {
	Sent int
	RTTs []time.Duration
}

// Pinger 对 host 依次发送 count 个 ICMP 回显请求，每个最多等待 timeout。
// 丢包不是错误；权限不足返回 ErrPermission，解析失败返回 ErrResolve。
type Pinger interface {
	Ping(ctx context.Context, host string, count int, timeout time.Duration) (Result, error)
}

type plugin struct {
	m      *manifest.Manifest
	pinger Pinger
}

func mustManifest() *manifest.Manifest {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("ping 内置 manifest 不合法: " + err.Error())
	}
	return m
}

func init() {
	runtime.Register(&plugin{m: mustManifest(), pinger: NewICMPPinger()})
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
	count := int(cfg.Number(in.Config, "count", defaultCount))
	if count < 1 || count > maxCount {
		return nil, fmt.Errorf("count 必须在 1–%d 之间", maxCount)
	}

	rep := &report.Report{Status: report.StatusOK, CollectedAt: clk.Now().UnixMilli()}
	res, err := p.pinger.Ping(ctx, host, count, cfg.Duration(in.Config, "timeout", defaultTimeout))
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, ErrPermission):
		return nil, fmt.Errorf("%w；请放开 net.ipv4.ping_group_range，或改用 tcp-check 插件", err)
	case errors.Is(err, ErrResolve):
		// 解析不了：丢包与延迟都未知，不输出。
		rep.Status = report.StatusCritical
		rep.Summary = "无法解析主机 / Cannot resolve host"
		return rep, nil
	case err != nil:
		return nil, err
	}

	if res.Sent <= 0 {
		return nil, errors.New("探测器没有发送任何数据包")
	}
	received := len(res.RTTs)
	loss := float64(res.Sent-received) * 100 / float64(res.Sent)
	lossMax, lossMin := 100.0, 0.0
	rep.Items = append(rep.Items, report.Item{Key: keyLoss, Type: report.TypeGauge, Unit: "%", Value: &loss, Min: &lossMin, Max: &lossMax})
	if received == 0 {
		// 全部丢失：延迟未知，不输出。
		rep.Status = report.StatusCritical
		rep.Summary = "全部丢包 / All packets lost"
	} else {
		var sum time.Duration
		for _, d := range res.RTTs {
			sum += d
		}
		avg := float64(sum) / float64(received) / float64(time.Millisecond)
		rep.Items = append(rep.Items, report.Item{Key: keyLatency, Type: report.TypeNumber, Unit: "ms", Value: &avg})
		if received < res.Sent {
			rep.Status = report.StatusWarning
			rep.Summary = fmt.Sprintf("丢包 %.0f%% / Packet loss %.0f%%", loss, loss)
		}
	}
	return rep, nil
}
