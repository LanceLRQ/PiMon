package daemon

import (
	"fmt"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agent/runner"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// conn 是一条会话的守护进程侧状态（字段在 Daemon.mu 下读写）。
type conn struct {
	s *agentlink.Session
	// ready 表示已收到 hello_ok；ended 表示会话已结束（之后不再挂运行器、不再成为当前会话）。
	ready, ended bool
	// dirty 表示 hello 发出后、hello_ok 之前插件目录有变化，hello_ok 后补发一次 plugins_changed。
	dirty     bool
	binding   *runner.Binding
	instances int
}

// handlerFor 为每条会话返回入站消息处理器（拨入与拨出共用）。
func (d *Daemon) handlerFor(agentlink.Peer) agentlink.Handler { return d.handle }

// onSession 在会话建立后同步调用（拨入由重连循环、拨出由监听方调用）：登记会话、起协程等它结束，
// 并立即发 hello（Send 不阻塞，满足「应快速返回」）。
func (d *Daemon) onSession(s *agentlink.Session) {
	c := &conn{s: s}
	d.mu.Lock()
	d.conns[s] = c
	ok := d.spawnLocked(func() {
		<-s.Done()
		d.onSessionEnd(c)
	})
	d.mu.Unlock()
	if !ok { // 正在退出（不应发生：连接循环结束后才置 closing）
		s.Close(agent.ReasonShutdown)
		return
	}
	hello := link.NewHello(d.hostname(), d.manifests())
	if err := s.Send(&hello); err != nil {
		d.log.Warn("发送 hello 失败", "err", err)
	}
}

// handle 在会话的分发协程中串行处理入站消息，不长时间阻塞。
func (d *Daemon) handle(s *agentlink.Session, msg agent.Message) {
	switch m := msg.(type) {
	case *agent.HelloOK:
		d.onHelloOK(s, m)
	case *agent.Incompatible:
		d.onIncompatible(s, m)
	case *agent.Assign:
		if b := d.binding(s, len(m.Instances)); b != nil {
			b.Assign(m) // 同步应用，之后才处理后续的 run_now
		}
	case *agent.RunNow:
		if b := d.binding(s, -1); b != nil {
			b.RunNow(m)
		}
	case *agent.LogsGet:
		d.sendLogs(s, m)
	case *agent.Ack:
		// event/ack 在 M3 使用。
	default:
		d.log.Debug("忽略 hub 消息", "type", string(msg.MsgType()))
	}
}

// binding 返回会话的运行器绑定（未收到 hello_ok 时为空）；n ≥ 0 时同时记下当前下发的实例数。
func (d *Daemon) binding(s *agentlink.Session, n int) *runner.Binding {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.conns[s]
	if c == nil || c.binding == nil {
		d.log.Debug("会话未就绪，忽略 hub 消息")
		return nil
	}
	if n >= 0 {
		c.instances = n
	}
	return c.binding
}

// onHelloOK 标记会话就绪并成为当前会话：挂上运行器、另起协程顶替旧会话；
// 拨出模式下若这是用当前凭据认证的会话，清空上一个凭据哈希（P10）。
func (d *Daemon) onHelloOK(s *agentlink.Session, m *agent.HelloOK) {
	d.mu.Lock()
	c := d.conns[s]
	if c == nil || c.ended || c.ready || d.closing || d.runner == nil {
		d.mu.Unlock()
		return
	}
	c.ready = true
	c.binding = d.runner.Attach(s)
	old := d.cur
	d.cur = c
	d.since = d.clk.Now()
	d.hubVer = link.Clip(m.HubVersion, 64)
	d.state, d.lastErr, d.incompat = StateRunning, "", nil
	dirty := c.dirty
	c.dirty = false
	if old != nil && old != c {
		d.spawnLocked(func() { old.s.Close(agent.ReasonReplaced) })
	}
	cur, prev := d.conf.CredentialHash, d.conf.PrevCredentialHash
	if d.conf.Mode == config.ModeDialOut && prev != "" && cur != "" && s.Peer().DeviceID == agent.DeviceIDFromHash(cur) {
		d.spawnLocked(func() { d.clearPrevHash(cur) })
	}
	d.mu.Unlock()

	d.log.Info("hub 已接受 hello", "hub_version", link.Clip(m.HubVersion, 64), "replaced", old != nil)
	if dirty {
		d.sendPluginsChanged(s)
	}
}

// onIncompatible 记录不兼容原因（状态与 Warn），并以该原因关闭会话兜底（M2a Ruling 31）。
// 拨入方向随后由重连循环按最大退避重试；拨出方向 hub 侧也会断开，这里只记录。
func (d *Daemon) onIncompatible(s *agentlink.Session, m *agent.Incompatible) {
	inc := Incompatible{Reason: m.Reason, RequiredMin: m.RequiredMin, HubVersion: link.Clip(m.HubVersion, 64)}
	d.mu.Lock()
	c := d.conns[s]
	if c == nil || c.ended {
		d.mu.Unlock()
		return
	}
	// 只有当前会话（或尚无当前会话）才改全局状态；被顶替途中的其他会话只记日志并关闭。
	global := d.cur == nil || d.cur == c
	if global {
		d.state, d.incompat = StateIncompatible, &inc
		d.lastErr = fmt.Sprintf("与 hub 协议版本不兼容（%s，需要协议版本 ≥ %d，hub 版本 %s），等待升级", inc.Reason, inc.RequiredMin, inc.HubVersion)
	}
	d.spawnLocked(func() { s.Close(m.Reason) })
	d.mu.Unlock()
	d.log.Warn("hub 判定协议版本不兼容", "reason", inc.Reason, "required_min", inc.RequiredMin,
		"hub_version", inc.HubVersion, "protocol", agent.ProtocolVersion, "current", global)
}

// onSessionEnd 在会话结束后善后：摘除登记；若是当前会话则记为断开与最近错误；等运行器绑定收尾；
// 拨出方向对端 bye{revoked} 时清除对应凭据哈希。
func (d *Daemon) onSessionEnd(c *conn) {
	r := c.s.Err()
	d.mu.Lock()
	c.ended = true
	delete(d.conns, c.s)
	b := c.binding
	if d.cur == c {
		d.cur, d.since = nil, time.Time{}
		if msg := describeClose(r); msg != "" {
			d.lastErr = msg
		}
	}
	mode := d.conf.Mode
	d.mu.Unlock()
	if b != nil {
		<-b.Done()
	}
	if mode == config.ModeDialOut && r.Kind == agentlink.ClosePeerBye && r.Bye == agent.ReasonRevoked {
		d.dialOutRevoked(c.s.Peer().DeviceID)
	}
}

// describeClose 把当前会话的关闭原因转成最近错误；本端主动关闭（退出、顶替、重载）返回空。
func describeClose(r agentlink.CloseReason) string {
	if r.Kind == agentlink.CloseLocal {
		return ""
	}
	if r.Bye != "" {
		return fmt.Sprintf("与 hub 的连接已断开（%s，bye %s）", r.Kind, link.Clip(r.Bye, 64))
	}
	return fmt.Sprintf("与 hub 的连接已断开（%s）", r.Kind)
}

// pluginsChanged 是插件注册表的变化通知：只在当前会话已收到 hello_ok 时发 plugins_changed；
// 没有就绪会话就丢弃（下次 hello 本来就带全量），hello 已发出但未就绪的会话记为待补发。
func (d *Daemon) pluginsChanged() {
	d.mu.Lock()
	var s *agentlink.Session
	if d.cur != nil {
		s = d.cur.s
	}
	for _, c := range d.conns {
		if !c.ready {
			c.dirty = true
		}
	}
	d.mu.Unlock()
	if s == nil {
		d.log.Debug("没有已就绪的会话，丢弃插件变化通知")
		return
	}
	d.sendPluginsChanged(s)
}

func (d *Daemon) sendPluginsChanged(s *agentlink.Session) {
	if err := s.Send(&agent.PluginsChanged{Plugins: d.manifests()}); err != nil {
		d.log.Warn("发送 plugins_changed 失败", "err", err)
	}
}

// manifests 返回可用插件的 manifest；出错时记 Warn 并返回空列表。
func (d *Daemon) manifests() []agent.PluginManifest {
	ms, err := d.reg.Manifests()
	if err != nil {
		d.log.Warn("序列化插件 manifest 失败", "err", err)
		return []agent.PluginManifest{}
	}
	if ms == nil {
		ms = []agent.PluginManifest{}
	}
	return ms
}
