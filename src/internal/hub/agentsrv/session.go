package agentsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// helloState 是会话在 hello 阶段的状态。
type helloState int

const (
	helloWaiting  helloState = iota // 等待 hello
	helloHandled                    // 已收到首条消息（hello 被接受、被拒绝或首条不是 hello）
	helloTimedOut                   // 超时，之后的消息一律忽略
)

var (
	// ErrHelloTimeout 表示会话在 HelloTimeout 内没有发来首条消息，已被断开。
	ErrHelloTimeout = errors.New("等待 agent hello 超时")
	// ErrHelloRejected 表示首条消息不是 hello、设备已不存在或处理 hello 时写库失败，会话已被断开。
	ErrHelloRejected = errors.New("agent hello 未被接受")
	// ErrSessionClosed 表示会话在 hello 有结果之前就已结束。
	ErrSessionClosed = errors.New("会话在 hello 完成前已结束")
)

// IncompatibleError 表示 agent 的 hello 协议版本与 hub 不兼容（已发 incompatible 并断开）。
type IncompatibleError struct {
	// Reason 取 agent.ReasonVersionTooOld 或 agent.ReasonVersionTooNew。
	Reason string
	// RequiredMin 是要求对端至少具备的协议版本。
	RequiredMin int
	// HubVersion 是 hub 软件版本。
	HubVersion string
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("agent 协议版本不兼容：%s（要求至少 %d）", e.Reason, e.RequiredMin)
}

// conn 是一条会话在本服务中的状态。
type conn struct {
	sess *agentlink.Session
	// handled 在离开 helloWaiting 时关闭，用于停止 hello 超时计时。
	handled chan struct{}

	// ready 在 hello 有结果（被接受、不兼容、被拒绝或超时）时关闭，outcome 在关闭前写入、之后只读；
	// 由 readyOnce 保证只设置一次。
	ready     chan struct{}
	readyOnce sync.Once
	outcome   error

	// mu 保护 state。
	mu    sync.Mutex
	state helloState

	// skew 与 attached 由 Server.mu 保护；attached 表示已 Attach（hello 超时已登记）。
	skew     skewSamples
	attached bool

	// accepted 表示 hello 已被接受（仅分发协程访问）。
	accepted bool
}

// connFor 取或建会话状态；Handler 可能早于 Attach 收到消息，二者都经它拿到同一个 conn。
// 会话已结束时不再登记（断开善后可能已删除过它），返回的 conn 只供本次调用使用。
func (s *Server) connFor(sess *agentlink.Session) *conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conns[sess]
	if c == nil {
		c = &conn{sess: sess, handled: make(chan struct{}), ready: make(chan struct{})}
		if !isDone(sess) {
			s.conns[sess] = c
		}
	}
	return c
}

func isDone(sess *agentlink.Session) bool {
	select {
	case <-sess.Done():
		return true
	default:
		return false
	}
}

// leaveWaiting 把状态从 helloWaiting 改为 to，成功返回真；已超时或已处理返回假。
func (c *conn) leaveWaiting(to helloState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != helloWaiting {
		return false
	}
	c.state = to
	close(c.handled)
	return true
}

// settle 记录 hello 的结果（nil 表示被接受）并唤醒 AttachReady；只有第一次调用生效，不阻塞。
func (c *conn) settle(outcome error) {
	c.readyOnce.Do(func() {
		c.outcome = outcome
		close(c.ready)
	})
}

// HandlerFor 为会话构造入站消息处理器，供 agentlink.ListenerConfig.Handler、DialerConfig.Handler
// 与 Reconnector 使用。首条消息必须是 hello，否则直接断开；hello 被接受后业务消息交给 Router。
func (s *Server) HandlerFor(agentlink.Peer) agentlink.Handler {
	return func(sess *agentlink.Session, msg agent.Message) {
		c := s.connFor(sess)
		if !c.accepted {
			s.handleFirst(c, msg)
			return
		}
		s.route(c, msg)
	}
}

// Attach 在会话建立后调用（agentlink.ListenerConfig.OnSession、Reconnector 的 OnSession，
// 或拨出配对的 Dial 返回后）：登记 hello 超时与会话断开善后。Server 已关停时以 bye{shutdown} 关闭会话。
func (s *Server) Attach(sess *agentlink.Session) {
	c := s.connFor(sess)
	s.mu.Lock()
	if s.closed {
		delete(s.conns, sess)
		s.mu.Unlock()
		sess.Close(agent.ReasonShutdown)
		return
	}
	// 计时器在启动协程前登记，测试据此用假时钟同步。
	timer := s.clk.After(HelloTimeout)
	c.attached = true
	s.wg.Add(2)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		s.helloTimeout(c, timer)
	}()
	go func() {
		defer s.wg.Done()
		s.watch(c)
	}()
}

// AttachReady 先 Attach 会话，再等待它的 hello 有结果：被接受（已注册、已标记在线）返回 nil；
// 不兼容返回 *IncompatibleError；hello 超时返回 ErrHelloTimeout；首条消息不是 hello 或设备已不存在等
// 返回包装 ErrHelloRejected 的错误；会话先行结束返回 ErrSessionClosed；ctx 结束返回 ctx.Err()（会话不受影响）。
// 供拨出配对使用：hello 可能早于 Attach 到达，结果记在会话状态上，不会丢失。
func (s *Server) AttachReady(ctx context.Context, sess *agentlink.Session) error {
	c := s.connFor(sess)
	s.Attach(sess)
	select {
	case <-c.ready:
		return c.outcome
	case <-sess.Done():
		select {
		case <-c.ready:
			return c.outcome
		default:
			return ErrSessionClosed
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// helloTimeout 在 HelloTimeout 内没有收到首条消息时直接断开（不发 bye）。
func (s *Server) helloTimeout(c *conn, timer <-chan time.Time) {
	select {
	case <-timer:
	case <-c.handled:
		return
	case <-c.sess.Done():
		return
	case <-s.done:
		return
	}
	if !c.leaveWaiting(helloTimedOut) {
		return
	}
	s.log.Info("agent 未在限定时间内发送 hello，断开连接", "device_id", c.sess.Peer().DeviceID, "timeout", HelloTimeout)
	c.settle(ErrHelloTimeout)
	c.sess.Close("")
}

// watch 等会话结束后善后：从表中删除；若它仍是该设备的当前会话，摘除注册并标记断开。
// 已被顶替或吊销的会话不再改动在线状态与注册表。
func (s *Server) watch(c *conn) {
	<-c.sess.Done()
	id := c.sess.Peer().DeviceID
	reason := c.sess.Err()

	s.flow.Lock()
	s.mu.Lock()
	delete(s.conns, c.sess)
	current := s.sessions[id] == c.sess
	if current {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
	if current {
		s.pres.Disconnected(id, reason.At)
	}
	s.flow.Unlock()

	if current {
		if err := s.devs.TouchLastSeen(s.ctx, id, reason.At); err != nil && !errors.Is(err, devices.ErrNotFound) {
			s.log.Warn("记录设备最后在线时间失败", "device_id", id, "err", err)
		}
	}
}

// handleFirst 处理会话的首条消息：hello 进入协商，其他消息直接断开。
func (s *Server) handleFirst(c *conn, msg agent.Message) {
	if !c.leaveWaiting(helloHandled) {
		return // 已超时或已处理（被拒绝的会话正在关闭）
	}
	hello, ok := msg.(*agent.Hello)
	if !ok {
		s.log.Info("agent 会话首条消息不是 hello，断开连接", "device_id", c.sess.Peer().DeviceID, "type", string(msg.MsgType()))
		c.settle(fmt.Errorf("%w：首条消息不是 hello", ErrHelloRejected))
		c.sess.Close("")
		return
	}
	s.handleHello(c, hello)
}

// handleHello 版本协商、更新设备行、确认占位、manifest 入库，接受后注册并顶替旧会话。
//
// 处理过程登记在 wg 里，Close 会等它结束（之后才可关库）；Close 之后才被分发的 hello 不再读写库。
func (s *Server) handleHello(c *conn, m *agent.Hello) {
	sess := c.sess
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		c.settle(ErrSessionClosed)
		sess.Close(agent.ReasonShutdown)
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	peer := sess.Peer()
	id := peer.DeviceID
	log := s.log.With("device_id", id)
	s.flow.Lock()
	reason := agent.CheckCompat(s.hubVer, s.hubMin, m.ProtocolVersion, m.MinCompatibleVersion)
	det, err := s.devs.Detail(s.ctx, id)
	if err != nil {
		s.flow.Unlock()
		s.rejectHello(c, log, err)
		return
	}
	info := devices.HelloInfo{
		Hostname: clipPeerField(m.Hostname), AgentVersion: clipPeerField(m.AgentVersion),
		OS: clipPeerField(m.OS), Arch: clipPeerField(m.Arch), ProtocolVersion: m.ProtocolVersion,
	}
	if det.Direction == model.DeviceDialIn {
		info.Address = remoteIP(peer.RemoteAddr)
	}

	if reason != "" {
		required := s.hubMin
		if reason == agent.ReasonVersionTooNew {
			required = m.MinCompatibleVersion
		}
		_ = sess.Send(&agent.Incompatible{Reason: reason, RequiredMin: required, HubVersion: s.hubVersion})
		info.Incompatible = &model.DeviceIncompatible{Reason: reason, RequiredMin: required}
		err := s.devs.UpdateHello(s.ctx, id, info)
		// agent 能发 hello 说明它已持有凭据：确认占位，升级 agent 后可直接用凭据接入，不必重新配对。
		// 重新配对的占位（replaces 非空）只记录不兼容原因，不确认、不迁移、不吊销旧设备：
		// 首个兼容的 hello 才接替旧设备（Ruling 22）；它不随占位清理删除（见 devices.Store.Cleanup）。
		if err == nil && !det.Confirmed && det.Replaces == "" {
			_, err = s.devs.Confirm(s.ctx, id)
		}
		s.flow.Unlock()
		if err != nil {
			log.Warn("记录 agent 不兼容原因失败", "err", err)
		}
		log.Warn("agent 协议版本不兼容，断开连接", "reason", reason,
			"agent_protocol", m.ProtocolVersion, "agent_min", m.MinCompatibleVersion, "required_min", required)
		c.settle(&IncompatibleError{Reason: reason, RequiredMin: required, HubVersion: s.hubVersion})
		sess.Close(reason)
		return
	}

	sess.SetProtocolVersion(m.ProtocolVersion)
	err = s.devs.UpdateHello(s.ctx, id, info)
	if err == nil && !det.Confirmed {
		var oldID string
		if oldID, err = s.devs.Confirm(s.ctx, id); err == nil && oldID != "" {
			log.Info("重新配对已确认，替换旧设备", "old_device_id", oldID)
		}
	}
	if err != nil {
		s.flow.Unlock()
		s.rejectHello(c, log, err)
		return
	}
	s.syncManifests(id, m.Plugins, log)
	// 会话可能在处理期间断开，且断开善后已先拿到 flow 执行完毕；此时不能再注册，否则设备会一直显示在线。
	if isDone(sess) {
		s.flow.Unlock()
		return
	}

	s.mu.Lock()
	old := s.sessions[id]
	s.sessions[id] = sess
	s.mu.Unlock()
	c.accepted = true
	if old != nil && old != sess {
		log.Info("同一设备的新会话顶替旧会话")
		s.spawn(func() { old.Close(agent.ReasonReplaced) })
	}
	_ = sess.Send(&agent.HelloOK{HubVersion: s.hubVersion, ServerTime: s.clk.Now()})
	s.pres.Connected(id)
	s.startSkew(c)
	s.flow.Unlock()
	c.settle(nil)

	log.Info("agent hello 已接受", "agent_version", m.AgentVersion, "protocol", m.ProtocolVersion, "plugins", len(m.Plugins))
	s.currentRouter().OnReady(id)
}

// rejectHello 在设备行不存在（已吊销或占位已清理）或写库失败时断开会话。
func (s *Server) rejectHello(c *conn, log *slog.Logger, err error) {
	sess := c.sess
	c.settle(fmt.Errorf("%w：%w", ErrHelloRejected, err))
	if errors.Is(err, devices.ErrNotFound) {
		log.Info("设备已不存在，断开会话")
		sess.Close(agent.ReasonRevoked)
		return
	}
	log.Warn("处理 hello 失败，断开会话", "err", err)
	sess.Close("")
}

// route 分发 hello 之后的业务消息。
func (s *Server) route(c *conn, msg agent.Message) {
	id := c.sess.Peer().DeviceID
	switch m := msg.(type) {
	case *agent.PluginsChanged:
		s.flow.Lock()
		s.mu.Lock()
		current := s.sessions[id] == c.sess
		s.mu.Unlock()
		if current {
			s.syncManifests(id, m.Plugins, s.log.With("device_id", id))
		}
		s.flow.Unlock()
		if current {
			s.currentRouter().OnReady(id)
		}
	case *agent.Report:
		s.currentRouter().OnReport(c.sess, id, m)
	case *agent.AssignResult:
		s.currentRouter().OnAssignResult(id, m)
	case *agent.UpgradeInfoGet:
		s.currentRouter().OnUpgradeInfoGet(c.sess, id, m)
	case *agent.Event:
		s.currentRouter().OnEvent(c.sess, id, m)
	default:
		s.log.Debug("忽略 agent 发来的消息", "device_id", id, "type", string(msg.MsgType()))
	}
}

// syncManifests 校验 agent 上报的 manifest 并入库（machine = 设备 ID）。不合法的条目跳过并记 Warn，不断开会话。
func (s *Server) syncManifests(id string, ps []agent.PluginManifest, log *slog.Logger) {
	ms := make([]plugins.MachineManifest, 0, len(ps))
	seen := make(map[string]bool, len(ps))
	for _, p := range ps {
		origin := plugins.Origin(p.Origin)
		if origin != plugins.OriginBuiltin && origin != plugins.OriginExec {
			log.Warn("忽略来源不合法的插件 manifest", "plugin_id", rawPluginID(p.Manifest), "origin", clipLog(p.Origin))
			continue
		}
		m, err := manifest.FromJSON(p.Manifest)
		if err != nil {
			log.Warn("忽略不合法的插件 manifest", "plugin_id", rawPluginID(p.Manifest), "err", err)
			continue
		}
		if seen[m.ID] {
			log.Warn("忽略重复的插件 manifest", "plugin_id", m.ID)
			continue
		}
		seen[m.ID] = true
		ms = append(ms, plugins.MachineManifest{Origin: origin, Manifest: m})
	}
	if err := s.reg.SyncMachine(s.ctx, id, ms); err != nil {
		log.Warn("agent 插件 manifest 入库失败", "err", err)
	}
}

// startSkew 启动时钟偏移采样：每 SkewInterval 读一次会话的偏移估计，保留最近 5 个样本。
func (s *Server) startSkew(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	tick := s.clk.After(SkewInterval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-tick:
				if d, ok := c.sess.ClockSkew(); ok {
					s.mu.Lock()
					c.skew.add(d)
					s.mu.Unlock()
				}
				tick = s.clk.After(SkewInterval)
			case <-c.sess.Done():
				return
			case <-s.done:
				return
			}
		}
	}()
}

// remoteIP 取 host:port 中的 IP（IPv4 映射地址还原为 IPv4），解析失败返回空串（不覆盖原地址）。
func remoteIP(addr string) string {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return ""
	}
	return ap.Addr().Unmap().String()
}

// maxPeerField 是对端可控字符串（hello 与 pair 的主机名、agent 版本、OS、架构）入库的最大字节数。
const maxPeerField = 256

// clipPeerField 把对端可控的字符串截断到 maxPeerField 字节内（保持 UTF-8 合法，截断时以「…」结尾）。
func clipPeerField(v string) string {
	return clipText(v, maxPeerField-len("…"))
}

// maxLogValue 是日志里对端可控字符串的最大字节数。
const maxLogValue = 64

// clipLog 截断对端可控的字符串（保持 UTF-8 合法）。
func clipLog(v string) string {
	if len(v) <= maxLogValue {
		return v
	}
	return strings.ToValidUTF8(v[:maxLogValue], "") + "…"
}

// rawPluginID 尽量从未通过校验的 manifest JSON 中取出插件 id 用于日志。
func rawPluginID(raw json.RawMessage) string {
	var head struct{ ID string }
	_ = json.Unmarshal(raw, &head)
	return clipLog(head.ID)
}
