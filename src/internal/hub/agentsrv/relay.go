package agentsrv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	// RunNowGrace 是测试运行在插件超时之外留给通道往返的余量（走会话的 Clock）。
	RunNowGrace = 5 * time.Second
	// LogsTimeout 是拉取设备日志的等待上限（走会话的 Clock）。
	LogsTimeout = 5 * time.Second
	// MaxLogLimit 是一次拉取的日志条数上限（agent 环形缓冲容量）。
	MaxLogLimit = 2000
	// maxErrorText 是设备上报的错误文字写入实例状态前的最大字节数。
	maxErrorText = 2048
)

// errNotAssigned 是设备回 not_assigned 时的说明。
const errNotAssigned = "实例尚未下发到设备 / instance not assigned to the device yet"

// ---- report ----

// OnReport 实现 Router：校验归属（实例在最近一次送达该设备的 assign 里）、只认设备当前会话、
// 按会话与实例对序号去重、比对下发摘要（Ruling 2：为空视为当前），busy 与 not_assigned 不入库，
// 其余经 ApplyRemoteResult 与本机采集走同一条入库路径。在会话分发协程中同步调用，不阻塞。
func (d *Dispatcher) OnReport(s *agentlink.Session, deviceID string, m *agent.Report) {
	d.onReport(s, deviceID, m)
}

func (d *Dispatcher) onReport(s sender, deviceID string, m *agent.Report) {
	// 先取当前会话再加 d.mu：不在持 d.mu 时调用会话注册表。
	if cur, ok := d.sessions(deviceID); !ok || cur != s {
		d.log.Debug("丢弃非当前会话的报告", "device_id", deviceID, "instance_id", clipLog(m.InstanceID))
		return
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	si, owned := d.sent[deviceID].hashes[m.InstanceID]
	if !owned {
		d.mu.Unlock()
		d.log.Warn("丢弃不属于该设备的报告", "device_id", deviceID, "instance_id", clipLog(m.InstanceID))
		return
	}
	seqs := d.seqs[s]
	watch := seqs == nil
	if watch {
		seqs = map[string]int64{}
		d.seqs[s] = seqs
		d.wg.Add(1)
	}
	last := seqs[m.InstanceID]
	if m.Seq > last {
		seqs[m.InstanceID] = m.Seq
	}
	d.mu.Unlock()
	if watch {
		go d.forgetSession(s)
	}
	if m.Seq <= last {
		d.log.Debug("丢弃重复或过期序号的报告", "device_id", deviceID, "instance_id", m.InstanceID, "seq", m.Seq, "last", last)
		return
	}
	if m.ConfigHash != "" && m.ConfigHash != si.hash {
		d.log.Debug("丢弃旧配置产生的报告", "device_id", deviceID, "instance_id", m.InstanceID)
		return
	}
	if m.ErrorKind == agent.ErrorKindBusy || m.ErrorKind == agent.ErrorKindNotAssigned {
		d.log.Debug("报告未产生运行结果，不入库", "device_id", deviceID, "instance_id", m.InstanceID, "error_kind", m.ErrorKind)
		return
	}
	rep := m.Report
	if rep != nil {
		// 协议已拒绝带 state 的报告，这里再清空一次作为防御。
		c := *rep
		c.State = ""
		rep = &c
	}
	d.inst.ApplyRemoteResult(m.InstanceID, si.content, rep, outcomeErr(m.RunOutcome))
}

// forgetSession 在会话结束（或下发器关停）后清理它的去重状态。
func (d *Dispatcher) forgetSession(s sender) {
	defer d.wg.Done()
	select {
	case <-s.Done():
	case <-d.done:
	}
	d.mu.Lock()
	delete(d.seqs, s)
	d.mu.Unlock()
}

// outcomeErr 把设备给出的运行结果转换为实例服务认得的错误：成功为 nil；timeout → runtime.ErrTimeout；
// failed → runtime.ErrFailed；busy → instances.ErrRunBusy；not_assigned → 不入库的 runtime.ErrFailed。
// 错误文字截断到 2 KiB。
func outcomeErr(o agent.RunOutcome) error {
	if o.Report != nil {
		return nil
	}
	msg := clipText(strings.TrimSpace(o.Error), maxErrorText)
	withMsg := func(base error) error {
		if msg == "" {
			return base
		}
		return fmt.Errorf("%w: %s", base, msg)
	}
	switch o.ErrorKind {
	case agent.ErrorKindTimeout:
		return withMsg(runtime.ErrTimeout)
	case agent.ErrorKindBusy:
		return instances.ErrRunBusy
	case agent.ErrorKindNotAssigned:
		return instances.NotRecorded(fmt.Errorf("%w: %s", runtime.ErrFailed, errNotAssigned))
	default:
		return withMsg(runtime.ErrFailed)
	}
}

// clipText 把 v 截断到至多 n 字节（保持 UTF-8 合法）。
func clipText(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return strings.ToValidUTF8(v[:n], "") + "…"
}

// ---- run_now ----

// RunNow 实现 instances.Remote：设备不在线返回 ErrDeviceOffline（有不兼容记录时返回
// *instances.DeviceIncompatibleError）；先让工作协程立即发出该设备待发的 assign（跳过合并窗口，P11），
// 再发 run_now 等 run_result，超时为 timeout + RunNowGrace。结果映射见 outcomeErr 与 requestErr；
// 本方法不入库，由实例服务按运行开始时的 config_hash 入库。不持任何锁等待。
func (d *Dispatcher) RunNow(ctx context.Context, machine, instanceID string, timeout time.Duration) (*report.Report, error) {
	if _, err := d.liveSession(ctx, machine); err != nil {
		return nil, err
	}
	if err := d.flush(ctx, machine); err != nil {
		return nil, err
	}
	// 下发期间会话可能已被顶替或断开，重新取当前会话。
	sess, err := d.liveSession(ctx, machine)
	if err != nil {
		return nil, err
	}
	resp, err := sess.Request(ctx, &agent.RunNow{InstanceID: instanceID}, timeout+RunNowGrace)
	if err != nil {
		return nil, requestErr(err)
	}
	rr, ok := resp.(*agent.RunResult)
	if !ok || rr.InstanceID != instanceID {
		return nil, instances.NotRecorded(fmt.Errorf("%w: 设备的应答与请求不符 / unexpected reply from device", runtime.ErrFailed))
	}
	if rr.ConfigHash != "" {
		if h, ok := d.SentHash(machine, instanceID); !ok || h != rr.ConfigHash {
			return nil, instances.NotRecorded(fmt.Errorf("%w: 设备上的配置不是最新 / device ran a stale config", runtime.ErrFailed))
		}
	}
	if err := outcomeErr(rr.RunOutcome); err != nil {
		return nil, err
	}
	return rr.Report, nil
}

// liveSession 返回设备当前会话；不在线时按设备行区分不兼容与离线。
func (d *Dispatcher) liveSession(ctx context.Context, deviceID string) (sender, error) {
	if s, ok := d.sessions(deviceID); ok {
		return s, nil
	}
	if dev, err := d.devs.Get(ctx, deviceID); err == nil && dev.Incompatible != nil {
		return nil, &instances.DeviceIncompatibleError{
			Reason: dev.Incompatible.Reason, RequiredMin: dev.Incompatible.RequiredMin, HubVersion: d.hubVersion,
		}
	}
	return nil, instances.ErrDeviceOffline
}

// requestErr 映射会话请求的错误：会话关闭（含发送队列满而断开）→ ErrDeviceOffline；
// 等待超时 → 不入库的 runtime.ErrTimeout；ctx 结束原样返回；其余为不入库的 runtime.ErrFailed。
func requestErr(err error) error {
	switch {
	case errors.Is(err, agentlink.ErrSessionClosed), errors.Is(err, agentlink.ErrSendQueueFull):
		return fmt.Errorf("%w: %w", instances.ErrDeviceOffline, err)
	case errors.Is(err, agentlink.ErrRequestTimeout):
		return instances.NotRecorded(fmt.Errorf("%w: 设备未在限定时间内应答 / device did not reply in time", runtime.ErrTimeout))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return instances.NotRecorded(fmt.Errorf("%w: %w", runtime.ErrFailed, err))
	}
}

// flush 让工作协程立即处理该设备待发的变更并等它完成（受 ctx 约束）：设备在合并窗口里有待发变更时
// 移入立即队列；已在立即队列或正在处理时等那一轮结束；都没有时直接返回。会话的发送队列与对端的收件队列
// 都按 FIFO 处理，所以随后的 run_now 一定在新 assign 之后到达。
func (d *Dispatcher) flush(ctx context.Context, dev string) error {
	ch := make(chan struct{})
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	_, queued := d.due[dev]
	switch {
	case d.pending[dev] || queued:
		delete(d.pending, dev)
		if !queued {
			d.due[dev] = false
		}
		d.dueWaiters[dev] = append(d.dueWaiters[dev], ch)
	default:
		ws, inflight := d.running[dev]
		if !inflight {
			d.mu.Unlock()
			return nil
		}
		d.running[dev] = append(ws, ch)
	}
	d.mu.Unlock()
	d.kick()
	select {
	case <-ch:
		return nil
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---- logs ----

// Logs 经通道拉取设备最近的日志：limit 截到 MaxLogLimit（0 由 agent 取默认条数），minLevel 为空不过滤；
// 等待 LogsTimeout。设备不在线返回 ErrDeviceOffline（不兼容时 *instances.DeviceIncompatibleError），
// 超时返回 runtime.ErrTimeout。返回值不为 nil。
func (d *Dispatcher) Logs(ctx context.Context, deviceID string, limit int, minLevel string) ([]model.LogEntry, error) {
	limit = min(max(limit, 0), MaxLogLimit)
	sess, err := d.liveSession(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	resp, err := sess.Request(ctx, &agent.LogsGet{Limit: limit, MinLevel: minLevel}, LogsTimeout)
	if err != nil {
		return nil, requestErr(err)
	}
	lg, ok := resp.(*agent.Logs)
	if !ok {
		return nil, fmt.Errorf("%w: 设备的应答与请求不符 / unexpected reply from device", runtime.ErrFailed)
	}
	if lg.Entries == nil {
		return []model.LogEntry{}, nil
	}
	return lg.Entries, nil
}

// ---- upgrade_info ----

// OnUpgradeInfoGet 实现 Router：回复中枢版本、网页端口与内嵌 agent 校验和（M2c 填充，本期为空）。
func (d *Dispatcher) OnUpgradeInfoGet(s *agentlink.Session, deviceID string, m *agent.UpgradeInfoGet) {
	d.replyUpgradeInfo(s, deviceID, m)
}

// replyUpgradeInfo 只入队不等待。取不到合法网页端口时记 Warn、不回复：协议没有错误应答，
// 非法端口过不了 UpgradeInfo 的校验，而凑一个端口会让 agent 去错误的地址下载；不回复时
// agent 的请求按超时失败，稍后重试即可。
func (d *Dispatcher) replyUpgradeInfo(s sender, deviceID string, m *agent.UpgradeInfoGet) {
	port := 0
	if d.webPort != nil {
		port = d.webPort()
	}
	if port < 1 || port > 65535 {
		d.log.Warn("网页端口未知，暂不回复升级信息", "device_id", deviceID, "web_port", port)
		return
	}
	err := s.Send(&agent.UpgradeInfo{RequestID: m.RequestID, HubVersion: d.hubVersion, WebPort: port, Agents: map[string]string{}})
	if err != nil {
		d.log.Warn("回复升级信息失败", "device_id", deviceID, "err", err)
	}
}

// WebPortFrom 从 host:port 取端口，不合法（含 0）时返回 0。
func WebPortFrom(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}
