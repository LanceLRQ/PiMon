// Package agentsrv 是 hub 侧的 agent 通道服务：认证与配对（agentlink.Authenticator）、会话生命周期
// （hello 超时、版本协商、设备确认、manifest 入库、顶替、在线状态）、吊销断开、未确认占位的定时清理
// 与时钟偏移采样。监听与拨出由调用方负责，二者都用 HandlerFor 与 Attach 接入同一套会话处理。
package agentsrv

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	// HelloTimeout 是会话建立后等待 hello 的上限（走 Clock）。
	HelloTimeout = 10 * time.Second
	// CleanupInterval 是清理过期配对码与未确认占位的周期。
	CleanupInterval = time.Minute
	// SkewInterval 是读取会话时钟偏移样本的周期。
	SkewInterval = 15 * time.Second
)

// Config 是 Server 的依赖。
type Config struct {
	Devices  *devices.Store
	Presence *devices.Presence
	Plugins  *plugins.Registry
	// Clock 默认 clock.Real{}。
	Clock clock.Clock
	// Log 默认 slog.Default()。
	Log *slog.Logger
	// HubVersion 是 hub 软件版本，写入 pair_ok、hello_ok 与 incompatible。
	HubVersion string
	// Hostname 返回 hub 主机名，写入 pair_ok；默认 os.Hostname。
	Hostname func() string
	// Router 默认 NopRouter，可在运行前用 UseRouter 替换。
	Router Router
}

// Server 是 agent 通道服务。
//
// 锁的约定：flow 串行化「hello 被接受」「会话断开善后」「吊销」三类会改变注册表与在线状态的流程，
// 保证同一设备的注册、顶替与 Presence 调用顺序一致；mu 只保护内存表，持有 mu 时不调用仓库、
// Presence 或会话的阻塞方法。不在持锁时等待会话关闭。
type Server struct {
	devs       *devices.Store
	pres       *devices.Presence
	reg        *plugins.Registry
	clk        clock.Clock
	log        *slog.Logger
	hubVersion string
	hostname   func() string
	// hubVer、hubMin 是参与协商的 hub 协议版本，默认取 agent 包常量；在 flow 下读写（测试可改）。
	hubVer, hubMin int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup

	flow sync.Mutex

	mu       sync.Mutex
	closed   bool
	router   Router
	conns    map[*agentlink.Session]*conn
	sessions map[string]*agentlink.Session // 设备 ID → hello 已被接受的当前会话
}

// New 创建 Server，订阅设备仓库的删除事件，并启动每分钟一次的清理循环。用 Close 停止。
func New(cfg Config) (*Server, error) {
	if cfg.Devices == nil || cfg.Presence == nil || cfg.Plugins == nil {
		return nil, errors.New("agentsrv 缺少设备仓库、在线状态或插件注册表")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Hostname == nil {
		cfg.Hostname = func() string { h, _ := os.Hostname(); return h }
	}
	if cfg.Router == nil {
		cfg.Router = NopRouter{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		devs: cfg.Devices, pres: cfg.Presence, reg: cfg.Plugins, clk: cfg.Clock, log: cfg.Log,
		hubVersion: cfg.HubVersion, hostname: cfg.Hostname,
		hubVer: agent.ProtocolVersion, hubMin: agent.MinCompatibleVersion,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		router:   cfg.Router,
		conns:    make(map[*agentlink.Session]*conn),
		sessions: make(map[string]*agentlink.Session),
	}
	s.devs.OnChange(s.onDeviceChange)
	// 计时器在启动协程前登记，测试据此用假时钟同步。
	tick := s.clk.After(CleanupInterval)
	s.wg.Add(1)
	go s.cleanupLoop(tick)
	return s, nil
}

// UseRouter 替换业务消息的接收方（B6 的下发器）；r 为空时恢复为 NopRouter。
func (s *Server) UseRouter(r Router) {
	if r == nil {
		r = NopRouter{}
	}
	s.mu.Lock()
	s.router = r
	s.mu.Unlock()
}

func (s *Server) currentRouter() Router {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.router
}

// Session 返回设备当前的会话（hello 已被接受）。
func (s *Server) Session(deviceID string) (*agentlink.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[deviceID]
	return sess, ok
}

// SessionDone 返回设备当前会话（hello 已被接受）的结束信号；没有会话时 ok 为假。
// 吊销接口据此等待会话真正断开。
func (s *Server) SessionDone(deviceID string) (done <-chan struct{}, ok bool) {
	sess, ok := s.Session(deviceID)
	if !ok {
		return nil, false
	}
	return sess.Done(), true
}

// ClockSkew 返回设备当前会话最近 5 个时钟偏移样本（对端时钟 − hub 时钟）的中位数；
// 设备不在线或还没有样本时 ok 为假。只在内存中，不入库。
func (s *Server) ClockSkew(deviceID string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conns[s.sessions[deviceID]]
	if c == nil {
		return 0, false
	}
	return c.skew.median()
}

// Close 停止清理循环与采样，以 bye{shutdown} 关闭仍在的会话，并等待全部后台协程退出（含会话断开善后，
// 因此须在关闭数据库之前调用）。可重复调用。
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.closed = true
	close(s.done)
	sessions := make([]*agentlink.Session, 0, len(s.conns))
	for sess := range s.conns {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.Close(agent.ReasonShutdown)
		}()
	}
	wg.Wait()
	s.wg.Wait()
	s.cancel()
}

// spawn 在未关停时登记并启动一个后台协程，已关停返回假。
func (s *Server) spawn(fn func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn()
	}()
	return true
}

// cleanupLoop 每分钟清理过期配对码与满 15 分钟仍未确认的拨入占位。
func (s *Server) cleanupLoop(tick <-chan time.Time) {
	defer s.wg.Done()
	for {
		select {
		case <-tick:
			if err := s.devs.Cleanup(s.ctx); err != nil && s.ctx.Err() == nil {
				s.log.Warn("清理配对码与未确认设备失败", "err", err)
			}
			tick = s.clk.After(CleanupInterval)
		case <-s.done:
			return
		}
	}
}

// onDeviceChange 是设备仓库的变更回调：设备被删除（吊销、清理占位、重新配对替换）时
// 异步断开它的会话并清除在线状态与插件缓存。回调可能在仓库调用方持有 flow 时同步触发
// （如 hello 流程里的 Confirm），所以这里只起协程，不在回调里加锁等待。
func (s *Server) onDeviceChange(id string, removed bool) {
	if !removed {
		return
	}
	s.spawn(func() { s.revoke(id) })
}

// revoke 先从注册表摘除会话（之后它的断开不再触发 Disconnected），再清除在线状态与插件，
// 最后以 bye{revoked} 关闭会话。
func (s *Server) revoke(id string) {
	s.flow.Lock()
	s.mu.Lock()
	sess := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	s.pres.Forget(id)
	if err := s.reg.DropMachine(s.ctx, id); err != nil && s.ctx.Err() == nil {
		s.log.Warn("清除已删除设备的插件记录失败", "device_id", id, "err", err)
	}
	s.flow.Unlock()
	if sess != nil {
		s.log.Info("设备已被删除，断开其会话", "device_id", id)
		sess.Close(agent.ReasonRevoked)
	}
}
