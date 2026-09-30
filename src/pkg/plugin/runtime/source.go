package runtime

import (
	"context"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// Input 是一次采集（或一次长连接运行）的输入。
type Input struct {
	// Config 是普通配置字段，不含密钥。
	Config map[string]any
	// Secrets 是解密后的密钥字段，与 Config 分开传递，插件不得写日志。
	Secrets map[string]string
	// Proxy 是解析好的代理；nil 或 IsDirect 表示直连。
	Proxy *proxy.Proxy
	// Last 是上次成功的报告，首次运行为 nil。
	Last *report.Report
	// State 是插件私有状态，原样回传，内容由插件自行定义。
	State string
	// Clock 是注入的时钟。
	Clock clock.Clock
}

// Source 是数据源插件的统一接口。
type Source interface {
	// Manifest 返回插件描述，调用方只读使用。
	Manifest() *manifest.Manifest
	// Collect 执行一次采集，受 ctx 超时控制。
	Collect(ctx context.Context, in Input) (*report.Report, error)
}

// Streamer 由事件驱动型插件（Hooks、长连接）额外实现。
// Run 阻塞运行直到 ctx 结束或出错，每产生一份报告就调用 emit。
// 生命周期（重启、退避）由调度器负责。
type Streamer interface {
	Run(ctx context.Context, in Input, emit func(*report.Report)) error
}

// Candidate 是 lookup 类型字段的一个候选项。
type Candidate struct {
	// Value 是写入配置的值。
	Value string `json:"value"`
	// Label 是给用户看的文字。
	Label string `json:"label"`
}

// Lookuper 由带 lookup 类型配置字段的插件实现（如 weather 的城市搜索）。
type Lookuper interface {
	// Lookup 按字段 key 与查询词返回候选；lang 为 zh 或 en。
	Lookup(ctx context.Context, key, query, lang string) ([]Candidate, error)
}
