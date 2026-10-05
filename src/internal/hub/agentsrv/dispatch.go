package agentsrv

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	// AssignWindow 是合并实例变更再下发的窗口（走 Clock）。
	AssignWindow = time.Second
	// HostMetricsPluginID 是设备首次上线时自动创建实例的插件。
	HostMetricsPluginID = "host-metrics"
	// maxIssueMessage 是回执 Message 写入实例问题时的最大字节数。
	maxIssueMessage = 512
)

// issueFrameTooLarge 是下发内容编码后超过单帧上限时记到该设备全部实例上的问题。
const issueFrameTooLarge = "下发内容超过 8 MiB，设备无法接收 / assign exceeds 8 MiB"

// RemoteInstances 是下发器需要的实例服务能力（由 instances.Service 实现）。
type RemoteInstances interface {
	RemoteSpecs(ctx context.Context, machine string) ([]instances.RemoteSpec, error)
	SetAssignIssue(machine, id, msg string)
	Create(ctx context.Context, in model.InstanceInput) (model.InstanceDetail, error)
	ApplyRemoteResult(id, contentHash string, rep *report.Report, err error)
}

// DeviceRecords 是下发器需要的设备仓库能力（由 devices.Store 实现）。
type DeviceRecords interface {
	Get(ctx context.Context, id string) (model.Device, error)
	TakeAutoHostMetrics(ctx context.Context, id string) (bool, error)
	OnChange(fn func(id string, removed bool))
}

// MachinePlugins 按机器取可用插件（由 plugins.Registry 实现）。
type MachinePlugins interface {
	MachinePlugin(machine, id string) (plugins.Plugin, bool)
}

// sender 是下发器对会话的最小依赖（*agentlink.Session 实现），单元测试用替身。
type sender interface {
	Send(agent.Message) error
	Request(ctx context.Context, msg agent.Requester, timeout time.Duration) (agent.Message, error)
	Done() <-chan struct{}
}

// EventSink 接收 agent 事件（任务跟踪模块实现）。Ingest 返回 nil 表示已处理（下发器随即回 ack），
// 包括数据不合法、设备已不存在、重复等无需重发的情况；只有需要 agent 重发时（如写库失败）返回错误。
// 在会话分发协程中同步调用。
type EventSink interface {
	Ingest(ctx context.Context, deviceID string, m *agent.Event) error
}

// DispatcherConfig 是下发器的依赖。
type DispatcherConfig struct {
	// Server 提供设备当前会话（NewDispatcher 必填）。
	Server    *Server
	Instances RemoteInstances
	Devices   DeviceRecords
	Plugins   MachinePlugins
	// Clock 默认 clock.Real{}。
	Clock clock.Clock
	// Log 默认 slog.Default()。
	Log *slog.Logger
	// HubVersion 是中枢软件版本，写入 upgrade_info 与设备不兼容错误。
	HubVersion string
	// WebPort 返回网页实际监听端口（取不到时返回 0），写入 upgrade_info；为空视为取不到。
	WebPort func() int
	// AgentChecksums 返回内嵌 agent 的校验和（键 os-arch，值 SHA-256 十六进制），写入 upgrade_info；
	// 为空或返回 nil 视为没有内嵌。
	AgentChecksums func() map[string]string
	// Events 接收 kind=task 的 agent 事件，可为空：为空时 task 事件不 ack（agent 保留并稍后重发）。
	Events EventSink
}

// sentInst 是某实例在最近一次送达的 assign 里的摘要：hash 是发给 agent 的下发摘要，
// content 是组装时实例行的 config_hash（报告入库时据此丢弃旧配置的结果）。
type sentInst struct {
	hash, content string
}

// sentAssign 是某设备最近一次送达的 assign：版本与逐实例摘要。
type sentAssign struct {
	version int64
	hashes  map[string]sentInst
}

// Dispatcher 是远程实例的下发器与中继：实现 instances.Remote（实例变化 → 合并 1 秒后下发；
// 测试运行经通道转发）与 Router（设备就绪 → 立即全量下发；assign_result → 实例问题；
// report → 入库；upgrade_info_get → 回复；event → 转交 EventSink 并回 ack），另提供日志拉取。所有下发由唯一的工作协程串行发出，
// 保证同一设备的版本号与发送顺序一致；回调只登记工作、不阻塞，也不在持锁时 Send 或等待请求。
type Dispatcher struct {
	inst       RemoteInstances
	devs       DeviceRecords
	plugs      MachinePlugins
	clk        clock.Clock
	log        *slog.Logger
	sessions   func(id string) (sender, bool)
	hubVersion string
	webPort    func() int
	agentSums  func() map[string]string
	events     EventSink

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}
	wg     sync.WaitGroup

	// mu 保护以下字段；持有 mu 时不调用实例服务、设备仓库与会话。
	mu     sync.Mutex
	closed bool
	// pending 是等待合并窗口到期的设备；timer 是当前登记的合并计时（nil 表示未登记）。
	pending map[string]bool
	timer   <-chan time.Time
	// due 是待工作协程处理的设备，值为真表示设备刚就绪（还需检查自动创建 host-metrics）。
	due map[string]bool
	// busy 表示工作协程正在处理一批设备。
	busy bool
	// dueWaiters 是等待 due 中某设备被处理完的测试运行（flush）；工作协程取走一批时移入 running。
	dueWaiters map[string][]chan struct{}
	// running 是工作协程当前这一批里尚未处理完的设备及其等待者（键存在即在处理中）。
	running map[string][]chan struct{}
	// seqs 是按会话、按实例记录的最近报告序号（去重），会话结束时清理。
	seqs map[sender]map[string]int64
	// versions 是每台设备最近分配的版本号（内存中单调递增，hub 重启后从 1 开始）。
	versions map[string]int64
	// sent 是每台设备最近一次送达的 assign（lastSent）。
	sent map[string]sentAssign
}

// NewDispatcher 创建下发器，订阅设备仓库的变更并启动工作协程。用 Close 停止。
func NewDispatcher(cfg DispatcherConfig) (*Dispatcher, error) {
	if cfg.Server == nil {
		return nil, errors.New("下发器缺少 agent 通道服务")
	}
	srv := cfg.Server
	return newDispatcher(cfg, func(id string) (sender, bool) {
		sess, ok := srv.Session(id)
		if !ok {
			return nil, false
		}
		return sess, true
	})
}

func newDispatcher(cfg DispatcherConfig, sessions func(string) (sender, bool)) (*Dispatcher, error) {
	if cfg.Instances == nil || cfg.Devices == nil || cfg.Plugins == nil {
		return nil, errors.New("下发器缺少实例服务、设备仓库或插件注册表")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		inst: cfg.Instances, devs: cfg.Devices, plugs: cfg.Plugins, clk: cfg.Clock, log: cfg.Log, sessions: sessions,
		hubVersion: cfg.HubVersion, webPort: cfg.WebPort, agentSums: cfg.AgentChecksums, events: cfg.Events,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
		pending: map[string]bool{}, due: map[string]bool{},
		dueWaiters: map[string][]chan struct{}{}, running: map[string][]chan struct{}{},
		seqs:     map[sender]map[string]int64{},
		versions: map[string]int64{}, sent: map[string]sentAssign{},
	}
	d.devs.OnChange(d.onDeviceChange)
	d.wg.Add(1)
	go d.loop()
	return d, nil
}

// Close 停止工作协程并等待正在进行的下发结束（须在关闭数据库之前调用）。可重复调用；
// 之后的回调一律忽略。
func (d *Dispatcher) Close() {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.done)
	}
	d.mu.Unlock()
	d.cancel()
	d.wg.Wait()
}

// MachineChanged 实现 instances.Remote：登记该设备待下发，并在没有合并计时时登记一次。
// 调用方可能持有实例服务的锁，这里只做标记与非阻塞唤醒。
func (d *Dispatcher) MachineChanged(machine string) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.pending[machine] = true
	armed := false
	if d.timer == nil {
		d.timer = d.clk.After(AssignWindow)
		armed = true
	}
	d.mu.Unlock()
	if armed {
		d.kick() // 让工作协程开始等待新的计时
	}
}

// OnReady 实现 Router：设备就绪（hello 被接受或插件变化）后立即全量下发，不等合并窗口。
func (d *Dispatcher) OnReady(deviceID string) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.due[deviceID] = true
	d.mu.Unlock()
	d.kick()
}

// OnAssignResult 实现 Router：只认该设备最近一次送达的版本；逐实例写入或清除回执问题，
// 不在该版本里的实例 ID 忽略。
func (d *Dispatcher) OnAssignResult(deviceID string, m *agent.AssignResult) {
	type update struct{ id, msg string }
	d.mu.Lock()
	sa, ok := d.sent[deviceID]
	if d.closed || !ok || sa.version != m.Version {
		d.mu.Unlock()
		d.log.Debug("忽略非当前版本的 assign 回执", "device_id", deviceID, "version", m.Version)
		return
	}
	ups := make([]update, 0, len(m.Results))
	unknown := 0
	for _, it := range m.Results {
		if _, ok := sa.hashes[it.InstanceID]; !ok {
			unknown++
			continue
		}
		ups = append(ups, update{id: it.InstanceID, msg: assignIssueText(it)})
	}
	d.mu.Unlock()
	if unknown > 0 {
		d.log.Warn("assign 回执含未下发给该设备的实例，已忽略", "device_id", deviceID, "version", m.Version, "count", unknown)
	}
	for _, u := range ups {
		d.inst.SetAssignIssue(deviceID, u.id, u.msg)
	}
}

// SentHash 返回最近一次送达设备的 assign 里该实例的下发摘要；实例不在其中（或设备从未送达）时 ok 为假。
// 供报告处理校验实例归属与配置是否为当前版本。
func (d *Dispatcher) SentHash(deviceID, instanceID string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h, ok := d.sent[deviceID].hashes[instanceID]
	return h.hash, ok
}

// onDeviceChange 是设备仓库的变更回调（可能在 agentsrv.flow 内同步触发，只投递不阻塞）：
// 设备变化（如重新配对确认迁移了实例）时按库重新下发；删除时丢弃其下发记录。
func (d *Dispatcher) onDeviceChange(id string, removed bool) {
	if !removed {
		d.MachineChanged(id)
		return
	}
	d.mu.Lock()
	delete(d.pending, id)
	delete(d.due, id)
	delete(d.versions, id)
	delete(d.sent, id)
	// 设备已删除，不会再下发：唤醒等待它的测试运行（随后取会话时得到离线）。
	ws := d.dueWaiters[id]
	delete(d.dueWaiters, id)
	d.mu.Unlock()
	for _, ch := range ws {
		close(ch)
	}
}

// kick 非阻塞地唤醒工作协程。
func (d *Dispatcher) kick() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// loop 是唯一的工作协程：合并计时到期把待合并设备移入 due，唤醒时处理 due。
func (d *Dispatcher) loop() {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		timer := d.timer
		d.mu.Unlock()
		select {
		case <-d.wake:
		case <-timer:
			d.mu.Lock()
			d.timer = nil
			for m := range d.pending {
				if _, ok := d.due[m]; !ok {
					d.due[m] = false
				}
			}
			clear(d.pending)
			d.mu.Unlock()
		case <-d.done:
			return
		}
		d.drain()
	}
}

// drain 处理当前 due 里的全部设备；处理期间新登记的留到下一轮。
func (d *Dispatcher) drain() {
	d.mu.Lock()
	batch := d.due
	d.due = map[string]bool{}
	for m := range batch {
		// 即将按库全量下发，之前登记的合并变更已被覆盖。
		delete(d.pending, m)
	}
	for m := range batch {
		d.running[m] = d.dueWaiters[m]
		delete(d.dueWaiters, m)
	}
	d.busy = len(batch) > 0
	d.mu.Unlock()
	for dev, ready := range batch {
		if d.ctx.Err() != nil {
			break
		}
		if ready {
			d.autoHostMetrics(dev)
		}
		d.dispatch(dev)
		d.release(dev)
	}
	d.mu.Lock()
	d.busy = false
	for dev, ws := range d.running {
		for _, ch := range ws {
			close(ch)
		}
		delete(d.running, dev)
	}
	d.mu.Unlock()
}

// release 标记该设备在本批已处理完，唤醒等待它的测试运行。
func (d *Dispatcher) release(dev string) {
	d.mu.Lock()
	ws := d.running[dev]
	delete(d.running, dev)
	d.mu.Unlock()
	for _, ch := range ws {
		close(ch)
	}
}

// autoHostMetrics 在设备上报了可用的 host-metrics 且设备行带「自动创建」标记时，以设备名创建一个实例；
// 标记先原子清零，失败只记 Warn、不重试。实例的创建会登记一次合并下发，随后的全量下发已包含它。
func (d *Dispatcher) autoHostMetrics(dev string) {
	if _, ok := d.plugs.MachinePlugin(dev, HostMetricsPluginID); !ok {
		return
	}
	took, err := d.devs.TakeAutoHostMetrics(d.ctx, dev)
	if err != nil {
		if d.ctx.Err() == nil {
			d.log.Warn("读取自动创建 host-metrics 标记失败", "device_id", dev, "err", err)
		}
		return
	}
	if !took {
		return
	}
	// 标记已清零：之后即使开始关停也要把实例建成（Close 等本次处理结束后才关库），否则标记清零却没有实例。
	ctx := context.WithoutCancel(d.ctx)
	dv, err := d.devs.Get(ctx, dev)
	if err != nil {
		d.log.Warn("自动创建 host-metrics 实例失败：读取设备失败", "device_id", dev, "err", err)
		return
	}
	if _, err := d.inst.Create(ctx, model.InstanceInput{
		PluginID: HostMetricsPluginID, Name: dv.Name, RunsOn: dev, Config: map[string]any{},
	}); err != nil {
		d.log.Warn("自动创建 host-metrics 实例失败，不再重试", "device_id", dev, "err", err)
		return
	}
	d.log.Info("已为新设备自动创建 host-metrics 实例", "device_id", dev)
}

// dispatch 向在线设备全量下发其实例：按库组装、分配版本、记为最近送达后发送；
// 发送失败时恢复为上一次送达的记录。设备不在线时什么也不做（上线时 OnReady 会全量下发）。
func (d *Dispatcher) dispatch(dev string) {
	sess, ok := d.sessions(dev)
	if !ok {
		return
	}
	specs, err := d.inst.RemoteSpecs(d.ctx, dev)
	if err != nil {
		if d.ctx.Err() == nil {
			d.log.Error("组装下发内容失败", "device_id", dev, "err", err)
		}
		return
	}
	list := make([]agent.AssignedInstance, len(specs))
	hashes := make(map[string]sentInst, len(specs))
	for i, sp := range specs {
		list[i] = agent.AssignedInstance{
			ID: sp.ID, PluginID: sp.PluginID, Config: sp.Config, Secrets: sp.Secrets, ProxyURL: sp.ProxyURL,
			IntervalMs: sp.Interval.Milliseconds(), TimeoutMs: sp.Timeout.Milliseconds(), ConfigHash: sp.ConfigHash,
			ContentHash: sp.ContentHash,
		}
		hashes[sp.ID] = sentInst{hash: sp.ConfigHash, content: sp.ContentHash}
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.versions[dev]++
	v := d.versions[dev]
	prev, hadPrev := d.sent[dev]
	// 先记为已送达再发送：agent 的回执可能先于 Send 返回到达。
	d.sent[dev] = sentAssign{version: v, hashes: hashes}
	d.mu.Unlock()

	err = sess.Send(&agent.Assign{Version: v, Instances: list})
	if err == nil {
		d.log.Debug("已下发实例", "device_id", dev, "version", v, "instance_count", len(list))
		return
	}
	d.mu.Lock()
	if cur, ok := d.sent[dev]; ok && cur.version == v {
		if hadPrev {
			d.sent[dev] = prev
		} else {
			delete(d.sent, dev)
		}
	}
	d.mu.Unlock()
	if errors.Is(err, agentlink.ErrFrameTooLarge) {
		d.log.Error("下发内容超过单帧上限，未发送", "device_id", dev, "version", v, "instance_count", len(list), "err", err)
		for _, sp := range specs {
			d.inst.SetAssignIssue(dev, sp.ID, issueFrameTooLarge)
		}
		return
	}
	// 会话已关闭或发送队列满（会话随之断开）：重新上线时会全量下发。
	d.log.Info("下发实例未送达", "device_id", dev, "version", v, "err", err)
}

// assignIssueText 把一条回执转换为实例问题文字；started 返回空串（清除）。
func assignIssueText(it agent.AssignResultItem) string {
	var base string
	switch it.Status {
	case agent.AssignStarted:
		return ""
	case agent.AssignPluginMissing:
		base = "设备上没有该插件"
	case agent.AssignInvalidConfig:
		base = "设备拒绝配置"
	case agent.AssignIncompatible:
		base = "设备上的插件版本不兼容"
	default:
		base = "设备未能启动该实例"
	}
	msg := strings.TrimSpace(it.Message)
	if msg == "" {
		return base
	}
	if len(msg) > maxIssueMessage {
		msg = strings.ToValidUTF8(msg[:maxIssueMessage], "") + "…"
	}
	return base + "：" + msg
}
