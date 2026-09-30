// Package hubself 是「中枢自身」内置插件：输出写库错误、数据目录剩余空间、推送失败、
// 在线 agent、屏幕在线与运行时长，并可向外部心跳地址（dead man's switch）发 GET。
//
// 统计来源在 app 装配完成后通过 Bind 晚绑定；未绑定时相关数据项一律不输出，
// 不会用 0 冒充真实值。
package hubself

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

//go:embed plugin.yaml
var manifestYAML []byte

// 数据项键名。
const (
	keyWriteErrors  = "hub.write_errors"
	keyDiskFree     = "hub.disk_free"
	keyPushFailures = "hub.push_failures"
	keyAgentsOnline = "hub.agents_online"
	keyScreenOnline = "hub.screens_online"
	keyUptime       = "hub.uptime"

	// EventRestarted 是启动后首份报告携带的事件类型，M4 的告警引擎消费。
	EventRestarted = "hub.restarted"

	secretHeartbeat = "heartbeat_url"

	diskWarnPct     = 10.0
	diskCriticalPct = 5.0
)

// Stats 是 hub-self 需要的中枢内部统计，由 app 实现并通过 Bind 注入。
type Stats interface {
	// WriteErrors 是写库错误累计次数。
	WriteErrors() int64
	// OnlineAgents 是在线 agent 数。
	OnlineAgents() int
	// ScreenOnline 报告是否有屏幕在线；known 为 false 表示来源尚未接入（未知，不当作离线）。
	ScreenOnline() (online, known bool)
	// PushFailures 是推送失败累计次数。
	PushFailures() int64
	// StartedAt 是中枢启动时间。
	StartedAt() time.Time
	// DataDir 是数据目录路径，用于查询剩余空间。
	DataDir() string
}

// DiskFunc 查询 path 所在文件系统的可用空间与总空间（字节），测试可替换。
type DiskFunc func(path string) (free, total uint64, err error)

// Plugin 是 hub-self 的 Source 实现。
type Plugin struct {
	m    *manifest.Manifest
	disk DiskFunc

	mu        sync.Mutex
	stats     Stats
	restarted bool // 已经在某份报告里带出过重启事件
}

var defaultPlugin *Plugin

func init() {
	defaultPlugin = New(statDisk)
	runtime.Register(defaultPlugin)
}

// New 构造一个 hub-self 插件；disk 为空时用真实磁盘查询。
func New(disk DiskFunc) *Plugin {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("hub-self 内置 manifest 不合法: " + err.Error())
	}
	if disk == nil {
		disk = statDisk
	}
	return &Plugin{m: m, disk: disk}
}

// Bind 给已注册的 hub-self 绑定统计来源。传 nil 解除绑定。
func Bind(s Stats) { defaultPlugin.Bind(s) }

// Bind 绑定统计来源。
func (p *Plugin) Bind(s Stats) {
	p.mu.Lock()
	p.stats = s
	p.restarted = false // 重新绑定视为一次新的启动，首份报告再次带出重启事件
	p.mu.Unlock()
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

	p.mu.Lock()
	st := p.stats
	p.mu.Unlock()

	if st != nil {
		p.fillItems(rep, st, now, in.Last)
	}
	if hb := in.Secrets[secretHeartbeat]; hb != "" {
		if err := heartbeat(ctx, hb, in.Proxy); err != nil {
			rep.Status = worse(rep.Status, report.StatusWarning)
			rep.Summary = "外部心跳请求失败 / Heartbeat request failed: " + err.Error()
		}
	}
	if st != nil {
		p.attachRestartEvent(rep, st)
	}
	return rep, nil
}

func (p *Plugin) fillItems(rep *report.Report, st Stats, now time.Time, last *report.Report) {
	writeErrs := float64(st.WriteErrors())
	rep.Items = append(rep.Items,
		report.Item{Key: keyWriteErrors, Type: report.TypeNumber, Value: &writeErrs},
		p.diskItem(st.DataDir()),
		report.Item{Key: keyPushFailures, Type: report.TypeNumber, Value: ptr(float64(st.PushFailures()))},
		report.Item{Key: keyAgentsOnline, Type: report.TypeNumber, Value: ptr(float64(st.OnlineAgents()))},
		screenItem(st.ScreenOnline()),
		report.Item{Key: keyUptime, Type: report.TypeNumber, Unit: "s", Value: ptr(max(0, now.Sub(st.StartedAt()).Seconds()))},
	)
	// 写库错误是累计计数：只在比上次报告增长时才告警，一次瞬时失败不会让状态永久停在 warning；
	// 没有上次报告时无从比较，不升级。
	if grew(last, writeErrs) {
		rep.Status = worse(rep.Status, report.StatusWarning)
	}
	if d := rep.Find(keyDiskFree); d != nil && d.RemainingPct != nil {
		switch pct := *d.RemainingPct; {
		case pct < diskCriticalPct:
			rep.Status = worse(rep.Status, report.StatusCritical)
		case pct < diskWarnPct:
			rep.Status = worse(rep.Status, report.StatusWarning)
		}
	}
}

func (p *Plugin) diskItem(dir string) report.Item {
	it := report.Item{Key: keyDiskFree, Type: report.TypeQuota, Unit: "B"}
	free, total, err := p.disk(dir)
	if err != nil || total == 0 {
		// 查不到就标错误，不写 0。
		it.Error = "无法读取磁盘空间 / Cannot read disk space"
		return it
	}
	used := float64(total - min(free, total))
	it.Used, it.Total = &used, ptr(float64(total))
	it.Remaining = ptr(float64(free))
	it.RemainingPct = ptr(float64(free) / float64(total) * 100)
	return it
}

// screenItem 屏幕离线只标 warning，不拉高整份报告的状态：
// 没有屏幕连着属于正常的部署形态。
// screenItem 输出屏幕在线项：来源未接入时为 unknown（未知显示为未知，Ruling 50）。
func screenItem(online, known bool) report.Item {
	if !known {
		return report.Item{Key: keyScreenOnline, Type: report.TypeState, State: report.StatusUnknown, Text: "unknown"}
	}
	if online {
		return report.Item{Key: keyScreenOnline, Type: report.TypeState, State: report.StatusOK, Text: "online"}
	}
	return report.Item{Key: keyScreenOnline, Type: report.TypeState, State: report.StatusWarning, Text: "offline"}
}

// attachRestartEvent 只在启动后第一份报告里带出 hub.restarted。
func (p *Plugin) attachRestartEvent(rep *report.Report, st Stats) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.restarted {
		return
	}
	p.restarted = true
	at := st.StartedAt().UnixMilli()
	rep.Events = append(rep.Events, report.Event{
		ID:   fmt.Sprintf("%s:%d", EventRestarted, at),
		Type: EventRestarted,
		At:   at,
	})
}

// heartbeat 向外部心跳地址发一次 GET，2xx 视为成功。
// 返回的错误只含主机名与状态码，不带完整地址（地址里常有令牌）。
func heartbeat(ctx context.Context, raw string, pr *proxy.Proxy) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return errors.New("地址不合法")
	}
	host := req.URL.Host
	cli := &http.Client{
		Transport: pr.Transport(),
		// 心跳不跟随重定向，避免令牌被带到别处。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := cli.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("请求 %s 失败: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s 返回状态 %d", host, resp.StatusCode)
	}
	return nil
}

// grew 报告 writeErrs 是否比上次报告里的写库错误数大。
func grew(last *report.Report, writeErrs float64) bool {
	if last == nil {
		return false
	}
	it := last.Find(keyWriteErrors)
	return it != nil && it.Value != nil && writeErrs > *it.Value
}

func worse(a, b report.Status) report.Status {
	rank := map[report.Status]int{report.StatusOK: 0, report.StatusWarning: 1, report.StatusCritical: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func ptr[T any](v T) *T { return &v }
