package agentlink

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/ratelimit"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 监听方入口防护参数（hub 与 agent 的监听方一致）。
const (
	// AgentPath 是 agent 通道唯一接受升级的路径。
	AgentPath = "/agent"
	// HandshakeTimeout 是从升级完成到认证/配对完成的上限（走 Clock）。
	HandshakeTimeout = 10 * time.Second
	// MaxPendingHandshakes 是同时处于认证前阶段的连接上限。
	MaxPendingHandshakes = 16
	// AuthMaxFailures 与 AuthLockDuration：同一来源 IP 认证失败 10 次锁 15 分钟。
	AuthMaxFailures  = 10
	AuthLockDuration = 15 * time.Minute

	// limitKeyPrefix 是限流 key 前缀，后面拼来源 IP。
	limitKeyPrefix = "agent:"
	// readHeaderTimeout 也约束 TLS 握手（http.Server 取读超时中最小者），用真实时间。
	readHeaderTimeout = 10 * time.Second
	maxHeaderBytes    = 16 << 10
)

// ErrListenerServing 表示 Serve 已被调用过（每个 Listener 只能 Serve 一次）。
var ErrListenerServing = errors.New("监听方已在运行")

// AuthError 是认证或配对被拒绝的原因，Reason 取 agent.Reason*（bad_credential、bad_code、code_expired 等），
// 监听方原样回 auth_fail{reason}。
type AuthError struct {
	Reason string
}

func (e *AuthError) Error() string { return "认证被拒绝：" + e.Reason }

// Authenticator 完成认证与配对的业务校验（凭据哈希比对、配对码有效期与签发凭据），本层不碰存储。
// 拒绝时返回 *AuthError；返回其他错误（如存储故障）时监听方不回 auth_fail、不计失败，直接关闭连接。
// ctx 在握手超时或监听方关停时取消。
type Authenticator interface {
	// Authenticate 校验长期凭据，返回设备 ID（以此为准，不信任对端自报）。
	Authenticate(ctx context.Context, msg agent.Auth) (deviceID string, err error)
	// Pair 校验配对码并签发凭据，返回的 pair_ok 原样发给对端，其 DeviceID 作为会话的设备 ID。
	Pair(ctx context.Context, msg agent.Pair) (agent.PairOK, error)
}

// FailLimiter 是来源 IP 认证失败限流所需的最小接口，ratelimit.Limiter 满足它。
type FailLimiter interface {
	// Locked 返回 key 剩余的锁定时长，未锁定返回 0。
	Locked(key string) time.Duration
	// Fail 记一次失败。
	Fail(key string) int
	// Success 清零 key 的失败计数。
	Success(key string)
}

// ListenerConfig 是监听方的配置。
type ListenerConfig struct {
	// Cert 是本端 TLS 证书（拨号方钉它的 SPKI）。
	Cert tls.Certificate
	// Role 是本端业务角色（agent.RoleHub 或 agent.RoleAgent）；对端 auth/pair 里的 role 必须是另一方。
	Role string
	// Auth 完成认证与配对的业务校验，必填。
	Auth Authenticator
	// Limiter 为空时用 ratelimit.NewLimiter(Clock, AuthMaxFailures, AuthLockDuration)。
	Limiter FailLimiter
	// Clock 默认 clock.Real{}；握手超时、会话心跳都走它。
	Clock clock.Clock
	// Log 默认 slog.Default()。
	Log *slog.Logger
	// Handler 为每个会话构造入站消息处理器，在会话开始读之前调用；为空或返回空时入站业务消息被丢弃。
	Handler func(Peer) Handler
	// OnSession 在会话建立后调用（会话已在读写），上层在这里登记会话并顶替旧会话
	// （对旧会话 Close(agent.ReasonReplaced)）。它在连接自己的协程中同步调用，可以阻塞片刻。
	OnSession func(*Session)
}

// Listener 是 agent 通道的监听方：TLS + WebSocket 升级（只认 GET /agent），
// 认证前入口防护（并发上限、握手超时、首帧大小、来源 IP 失败锁定），握手成功后建立 Session。
type Listener struct {
	cfg     ListenerConfig
	clk     clock.Clock
	log     *slog.Logger
	limiter FailLimiter
	tlsConf *tls.Config
	pending chan struct{} // 认证前并发信号量
	served  atomic.Bool

	// baseCtx 在 Serve 中设置，握手随它取消；serveHTTP 只会在 Serve 启动 http.Server 之后运行。
	baseCtx context.Context

	mu       sync.Mutex
	closing  bool
	sessions map[*Session]struct{}
	// conns 统计所有在途请求（含握手与会话），Serve 返回前等它们结束。只在 mu 下且 !closing 时 Add。
	conns sync.WaitGroup
}

// NewListener 校验配置并创建监听方。
func NewListener(cfg ListenerConfig) (*Listener, error) {
	if cfg.Auth == nil {
		return nil, errors.New("监听方缺少 Authenticator")
	}
	if cfg.Role != agent.RoleHub && cfg.Role != agent.RoleAgent {
		return nil, fmt.Errorf("监听方角色非法：%q", cfg.Role)
	}
	if len(cfg.Cert.Certificate) == 0 {
		return nil, errors.New("监听方缺少 TLS 证书")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	limiter := cfg.Limiter
	if limiter == nil {
		limiter = ratelimit.NewLimiter(cfg.Clock, AuthMaxFailures, AuthLockDuration)
	}
	return &Listener{
		cfg:     cfg,
		clk:     cfg.Clock,
		log:     cfg.Log,
		limiter: limiter,
		tlsConf: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cfg.Cert},
			NextProtos:   []string{"http/1.1"},
		},
		pending:  make(chan struct{}, MaxPendingHandshakes),
		sessions: make(map[*Session]struct{}),
	}, nil
}

// Serve 在 ln 上提供 TLS 服务，直到 ctx 结束或底层监听出错；只能调用一次，ln 由 Serve 关闭。
// ctx 结束时：停止接受新连接、取消进行中的握手、对所有已建立会话 Close(agent.ReasonShutdown)，
// 等所有握手协程与会话结束后返回 nil。
func (l *Listener) Serve(ctx context.Context, ln net.Listener) error {
	if !l.served.CompareAndSwap(false, true) {
		return ErrListenerServing
	}
	baseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	l.baseCtx = baseCtx
	srv := &http.Server{
		Handler:           http.HandlerFunc(l.serveHTTP),
		ReadHeaderTimeout: readHeaderTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// TLS 握手失败等噪声降到 Debug，避免被扫描时刷屏。
		ErrorLog: slog.NewLogLogger(l.log.Handler(), slog.LevelDebug),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(tls.NewListener(ln, l.tlsConf)) }()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}

	l.mu.Lock()
	l.closing = true
	sessions := make([]*Session, 0, len(l.sessions))
	for s := range l.sessions {
		sessions = append(sessions, s)
	}
	l.mu.Unlock()

	cancel() // 取消进行中的握手
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Close(agent.ReasonShutdown)
		}()
	}
	wg.Wait()
	// 被升级（Hijack）的连接不归 http.Server 管，已由握手与会话自行关闭；这里只关监听与普通连接。
	_ = srv.Close()
	if serveErr == nil {
		<-errc
	}
	l.conns.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("agent 通道监听失败：%w", serveErr)
	}
	return nil
}

// serveHTTP 只接受 GET /agent 的 WebSocket 升级，其余一律 404；认证前并发满或正在关停时回 503。
func (l *Listener) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != AgentPath || r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.NotFound(w, r)
		return
	}
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		reject(w)
		return
	}
	l.conns.Add(1)
	l.mu.Unlock()
	defer l.conns.Done()

	select {
	case l.pending <- struct{}{}:
	default:
		l.log.Warn("agent 通道认证前连接数已满，拒绝新连接", "remote_addr", r.RemoteAddr)
		reject(w)
		return
	}
	s := l.accept(w, r)
	if s == nil {
		return
	}
	// 连接协程守到会话结束，Serve 据此等待。
	<-s.Done()
	l.mu.Lock()
	delete(l.sessions, s)
	l.mu.Unlock()
}

func reject(w http.ResponseWriter) {
	w.Header().Set("Connection", "close")
	http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
}

// accept 完成升级与握手并建立会话，失败返回 nil。认证前信号量在握手结束（无论成败）时释放。
func (l *Listener) accept(w http.ResponseWriter, r *http.Request) *Session {
	released := false
	release := func() {
		if !released {
			released = true
			<-l.pending
		}
	}
	defer release()

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		l.log.Debug("agent 通道升级失败", "remote_addr", r.RemoteAddr, "err", err)
		return nil
	}
	fc := newWSFrameConn(c)
	peer, ok := l.handshake(fc, r.RemoteAddr)
	release()
	if !ok {
		_ = fc.Close()
		return nil
	}

	var h Handler
	if l.cfg.Handler != nil {
		h = l.cfg.Handler(peer)
	}
	s := newSession(fc, peer, SessionOptions{Clock: l.clk, Handler: h, Log: l.log})
	l.mu.Lock()
	closing := l.closing
	if !closing {
		l.sessions[s] = struct{}{}
	}
	l.mu.Unlock()
	if closing {
		s.Close(agent.ReasonShutdown)
		return s
	}
	l.log.Info("agent 通道认证成功", "device_id", peer.DeviceID, "remote_addr", peer.RemoteAddr, "role", peer.Role)
	if l.cfg.OnSession != nil {
		l.cfg.OnSession(s)
	}
	return s
}

// handshake 在 fc 上完成认证或配对：首帧必须是 auth 或 pair，整个过程受 HandshakeTimeout（走 Clock）约束。
// 成功时已回 auth_ok/pair_ok；失败时视情况已回 auth_fail，调用方负责关闭连接。
func (l *Listener) handshake(fc frameConn, remoteAddr string) (Peer, bool) {
	ctx, cancel := context.WithCancel(l.baseCtx)
	defer cancel()
	timer := l.clk.After(HandshakeTimeout)
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	var timedOut atomic.Bool
	go func() {
		defer close(watchDone)
		select {
		case <-timer:
			timedOut.Store(true)
			cancel()
			_ = fc.Close()
		case <-stop:
		}
	}()

	peer, ok := l.doHandshake(ctx, fc, remoteAddr)
	close(stop)
	<-watchDone
	if timedOut.Load() {
		l.log.Info("agent 通道握手超时，断开连接", "remote_addr", remoteAddr)
		return Peer{}, false
	}
	return peer, ok
}

func (l *Listener) doHandshake(ctx context.Context, fc frameConn, remoteAddr string) (Peer, bool) {
	log := l.log.With("remote_addr", remoteAddr)
	fc.SetReadLimit(agent.MaxPreAuthFrame)
	b, err := fc.Read(ctx)
	if err != nil {
		log.Debug("agent 通道握手读首帧失败", "err", err)
		return Peer{}, false
	}
	msg, err := agent.Decode(b)
	if err != nil {
		log.Info("agent 通道首帧不合法，断开连接", "err", err)
		return Peer{}, false
	}

	var role string
	switch m := msg.(type) {
	case *agent.Auth:
		role = m.Role
	case *agent.Pair:
		role = m.Role
	default:
		log.Info("agent 通道首帧不是认证或配对，断开连接", "type", string(msg.MsgType()))
		return Peer{}, false
	}

	key := limitKeyPrefix + sourceIP(remoteAddr)
	if l.limiter.Locked(key) > 0 {
		log.Warn("agent 通道来源 IP 认证失败次数过多，已锁定")
		l.writeFrame(ctx, fc, &agent.AuthFail{Reason: agent.ReasonRateLimited})
		return Peer{}, false
	}
	if role != peerRole(l.cfg.Role) {
		l.fail(ctx, fc, log, key, agent.ReasonBadCredential, "role", role)
		return Peer{}, false
	}

	peer := Peer{RemoteAddr: remoteAddr, Role: role}
	var reply agent.Message
	switch m := msg.(type) {
	case *agent.Auth:
		id, err := l.cfg.Auth.Authenticate(ctx, *m)
		if err != nil {
			l.authError(ctx, fc, log, key, err)
			return Peer{}, false
		}
		peer.DeviceID = id
		reply = &agent.AuthOK{DeviceID: id}
	case *agent.Pair:
		ok, err := l.cfg.Auth.Pair(ctx, *m)
		if err != nil {
			l.authError(ctx, fc, log, key, err)
			return Peer{}, false
		}
		peer.DeviceID = ok.DeviceID
		reply = &ok
	}
	l.limiter.Success(key)
	if !l.writeFrame(ctx, fc, reply) {
		return Peer{}, false
	}
	return peer, true
}

// authError 处理 Authenticator 的错误：*AuthError 回 auth_fail 并按原因计入失败，其他错误直接断开。
func (l *Listener) authError(ctx context.Context, fc frameConn, log *slog.Logger, key string, err error) {
	var ae *AuthError
	if !errors.As(err, &ae) {
		log.Warn("agent 通道认证出错，断开连接", "err", err)
		return
	}
	l.fail(ctx, fc, log, key, ae.Reason)
}

// fail 回 auth_fail{reason}；bad_credential、bad_code、code_expired 计入来源 IP 的失败次数。
func (l *Listener) fail(ctx context.Context, fc frameConn, log *slog.Logger, key, reason string, attrs ...any) {
	switch reason {
	case agent.ReasonBadCredential, agent.ReasonBadCode, agent.ReasonCodeExpired:
		l.limiter.Fail(key)
	}
	log.Info("agent 通道认证失败", append([]any{"reason", reason}, attrs...)...)
	l.writeFrame(ctx, fc, &agent.AuthFail{Reason: reason})
}

func (l *Listener) writeFrame(ctx context.Context, fc frameConn, m agent.Message) bool {
	b, err := agent.Encode(m)
	if err != nil {
		l.log.Warn("agent 通道握手应答编码失败", "type", string(m.MsgType()), "err", err)
		return false
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := fc.Write(wctx, b); err != nil {
		l.log.Debug("agent 通道握手应答写出失败", "err", err)
		return false
	}
	return true
}

// peerRole 返回与本端角色相对的另一方角色。
func peerRole(self string) string {
	if self == agent.RoleHub {
		return agent.RoleAgent
	}
	return agent.RoleHub
}

// sourceIP 取 r.RemoteAddr 的 IP（不采信 X-Forwarded-*），IPv4 映射地址还原为 IPv4；解析失败时原样返回。
func sourceIP(remoteAddr string) string {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return ap.Addr().Unmap().String()
}
