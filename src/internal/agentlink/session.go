// Package agentlink 是 hub 与 agent 共用的连接层：TLS 钉扎、WebSocket 拨号与接受、
// 认证/配对握手、会话（心跳、请求/响应关联、发送队列）与重连。
package agentlink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 会话参数。
const (
	// HeartbeatInterval 是发送 ping 的间隔。
	HeartbeatInterval = 15 * time.Second
	// HeartbeatTimeout 是判断连接断开的静默时长：连续 2 次心跳（30 秒）加 5 秒余量内没收到任何帧。
	HeartbeatTimeout = 35 * time.Second
	// SendQueueSize 是发送队列与收件队列的默认容量。
	SendQueueSize = 256
	// MaxQueuedBytes 是发送队列与收件队列各自允许积压的总字节数默认值，超出按 queue_full 断开。
	MaxQueuedBytes int64 = 32 << 20
	// ByeFlushTimeout 是发送 bye 后等待写出的上限。
	ByeFlushTimeout = time.Second
	// writeTimeout 是单帧写出的上限（网络操作，用真实时间）。
	writeTimeout = 10 * time.Second
)

var (
	// ErrSessionClosed 表示会话已断开；关闭时所有等待中的请求立即收到此错误。
	ErrSessionClosed = errors.New("连接已断开")
	// ErrRequestTimeout 表示请求在超时前没有收到响应。
	ErrRequestTimeout = errors.New("请求超时")
	// ErrSendQueueFull 表示发送队列已满（对端处理不过来），会话随之断开。
	ErrSendQueueFull = errors.New("发送队列已满")
	// ErrRecvQueueFull 表示收件队列已满（Handler 处理不过来），会话随之断开。
	ErrRecvQueueFull = errors.New("收件队列已满")
	// ErrNoRequestID 表示请求号为空且消息不能代为写入（值类型），需传指针或先填请求号。
	ErrNoRequestID = errors.New("请求号为空且无法代为生成")
	// ErrFrameTooLarge 表示编码后的帧超过 agent.MaxFrame，不入队也不断开会话。
	ErrFrameTooLarge = errors.New("消息超过单帧上限")
	// ErrDuplicateRequestID 表示同一请求号已有等待者。
	ErrDuplicateRequestID = errors.New("请求号重复")
)

// CloseKind 是会话关闭的类别。
type CloseKind string

// 会话关闭类别。
const (
	// CloseLocal 表示本端调用 Close。
	CloseLocal CloseKind = "local"
	// ClosePeerBye 表示对端发来 bye。
	ClosePeerBye CloseKind = "peer_bye"
	// CloseHeartbeat 表示心跳超时（HeartbeatTimeout 内没收到任何帧）。
	CloseHeartbeat CloseKind = "heartbeat"
	// CloseReadError 表示读帧失败（连接断开、超出帧上限、非文本帧等）。
	CloseReadError CloseKind = "read_error"
	// CloseWriteError 表示写帧失败或超时。
	CloseWriteError CloseKind = "write_error"
	// CloseProtocolError 表示收到不合法的消息（*agent.InvalidMessageError）。
	CloseProtocolError CloseKind = "protocol_error"
	// CloseQueueFull 表示发送队列或收件队列已满，Err 区分是哪一个。
	CloseQueueFull CloseKind = "queue_full"
)

// CloseReason 是会话关闭的原因，会话关闭后由 Err 返回。
type CloseReason struct {
	Kind CloseKind
	// Bye 是 bye 的原因：本端 Close 时为传入的 reason，对端 bye 时为对端给出的 reason。
	Bye string
	// At 是关闭时刻（会话的 Clock）。
	At time.Time
	// Err 是导致关闭的底层错误，本端关闭与对端 bye 时为空。
	Err error
}

// Peer 描述会话对端。
type Peer struct {
	DeviceID   string
	RemoteAddr string
	// Role 是对端的业务角色（agent.RoleHub 或 agent.RoleAgent）。
	Role string
	// ProtocolVersion 是协商后的对端协议版本；构造时可为 0，hello 后由上层用 SetProtocolVersion 设置。
	ProtocolVersion int
}

// Handler 处理一条入站消息。它在会话的分发协程中串行调用，不得长时间阻塞：
// 收件队列满会断开会话。ping、pong、bye 与所有响应类消息（run_result、logs、upgrade_info）
// 都不会交给 Handler：响应只按 request_id 交给等待者，没有等待者的（例如超时后迟到的）
// 由会话层丢弃；请求类消息（run_now、logs_get、upgrade_info_get）一律交给 Handler。
// Done 关闭时，正在执行的 Handler 可能仍在运行，会话不等待它返回。
// 会话断开（对端 bye、读错误、本端 Close 等）时，收件队列中尚未交给 Handler 的消息可能被丢弃，
// Handler 返回后不保证还有后续回调。
type Handler func(s *Session, msg agent.Message)

// SessionOptions 是会话的可选参数，零值字段取默认值。
type SessionOptions struct {
	Clock   clock.Clock  // 默认 clock.Real{}
	Handler Handler      // 为空时入站业务消息被丢弃
	Log     *slog.Logger // 默认 slog.Default()
	// SendQueueSize 与 RecvQueueSize 默认 SendQueueSize（256）。
	SendQueueSize int
	RecvQueueSize int
	// MaxQueuedBytes 是发件与收件各自在队列中积压的总字节上限（分开计数），默认 MaxQueuedBytes（32 MiB）。
	MaxQueuedBytes int64
}

type outFrame struct {
	data []byte
	// flushed 非空时在该帧写出后关闭（bye 用）。
	flushed chan struct{}
	// counted 是已计入发件字节预算的字节数，写出后扣减；bye 等不计预算的帧为 0。
	counted int64
}

// inFrame 是收件队列中的一条消息及其计入预算的原始帧长度。
type inFrame struct {
	msg agent.Message
	n   int64
}

// Session 是认证完成后的一条 hub↔agent 连接，不关心消息的业务含义。
//
// 每个会话有 4 个协程：读协程（解码、心跳应答、响应关联、投递收件队列）、
// 写协程（按序写出发送队列）、分发协程（串行调用 Handler）、心跳协程（定时 ping 与判活）。
// 任一处判定结束都走 finish：记录原因、取消 ctx（各协程随之退出）、关闭连接、关闭 done。
type Session struct {
	fc      frameConn
	clk     clock.Clock
	log     *slog.Logger
	handler Handler

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	sendQ chan outFrame
	inbox chan inFrame
	// maxBytes 是发件、收件各自的字节预算；sendBytes、recvBytes 是当前在队列中的字节数。
	maxBytes  int64
	sendBytes atomic.Int64
	recvBytes atomic.Int64
	// lastSeen 是最近一次读到帧的时刻（Clock 的 UnixNano），心跳协程据此判活。
	lastSeen atomic.Int64

	mu       sync.Mutex
	peer     Peer
	closing  bool         // 已调用 Close，不再接受新的发送
	local    *CloseReason // Close 给出的原因，优先于并发发生的其他原因
	finished bool
	reason   CloseReason
	waiters  map[string]chan agent.Message
	skew     time.Duration
	skewOK   bool
}

// newSession 在已完成握手的 fc 上启动会话：读上限设为 agent.MaxFrame，并启动读、写、分发、心跳协程。
func newSession(fc frameConn, peer Peer, opts SessionOptions) *Session {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.SendQueueSize <= 0 {
		opts.SendQueueSize = SendQueueSize
	}
	if opts.RecvQueueSize <= 0 {
		opts.RecvQueueSize = SendQueueSize
	}
	if opts.MaxQueuedBytes <= 0 {
		opts.MaxQueuedBytes = MaxQueuedBytes
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		maxBytes: opts.MaxQueuedBytes,
		fc:       fc,
		clk:      opts.Clock,
		log:      opts.Log.With("device_id", peer.DeviceID, "remote_addr", peer.RemoteAddr),
		handler:  opts.Handler,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		sendQ:    make(chan outFrame, opts.SendQueueSize),
		inbox:    make(chan inFrame, opts.RecvQueueSize),
		peer:     peer,
		waiters:  make(map[string]chan agent.Message),
	}
	fc.SetReadLimit(agent.MaxFrame)
	s.lastSeen.Store(s.clk.Now().UnixNano())
	// 计时器在启动协程前登记，测试据此用假时钟的 Waiters() 同步。
	ping := s.clk.After(HeartbeatInterval)
	alive := s.clk.After(HeartbeatTimeout)
	go s.readLoop()
	go s.writeLoop()
	go s.dispatchLoop()
	go s.heartbeatLoop(ping, alive)
	return s
}

// Peer 返回对端信息。
func (s *Session) Peer() Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peer
}

// SetProtocolVersion 记录协商后的对端协议版本（hello 之后由上层调用）。
func (s *Session) SetProtocolVersion(v int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peer.ProtocolVersion = v
}

// Done 在会话关闭后关闭。
func (s *Session) Done() <-chan struct{} { return s.done }

// Err 返回关闭原因；会话未关闭时返回零值。
func (s *Session) Err() CloseReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// ClockSkew 返回最近一次 pong 算出的对端时钟偏移（对端时钟 − 本端时钟）；还没收到 pong 时 ok 为假。
func (s *Session) ClockSkew() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skew, s.skewOK
}

// NewRequestID 生成请求号：16 字节随机数的十六进制。
func NewRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read 不会返回错误
	return hex.EncodeToString(b[:])
}

// Send 校验并编码消息后非阻塞入队；队列满即断开会话并返回 ErrSendQueueFull，
// 会话关闭（或正在关闭）时返回 ErrSessionClosed，校验失败返回 *agent.InvalidMessageError，
// 编码后超过 agent.MaxFrame 返回 ErrFrameTooLarge（后两种不入队、不断开）。
func (s *Session) Send(msg agent.Message) error {
	b, err := agent.Encode(msg)
	if err != nil {
		return err
	}
	if len(b) > agent.MaxFrame {
		return fmt.Errorf("%w：%s 编码后 %d 字节", ErrFrameTooLarge, msg.MsgType(), len(b))
	}
	return s.enqueue(b)
}

func (s *Session) enqueue(b []byte) error {
	s.mu.Lock()
	if s.closing || s.finished {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	n := int64(len(b))
	if s.sendBytes.Add(n) <= s.maxBytes {
		select {
		case s.sendQ <- outFrame{data: b, counted: n}:
			s.mu.Unlock()
			return nil
		default:
		}
	}
	s.sendBytes.Add(-n)
	s.mu.Unlock()
	s.log.Warn("agent 会话发送队列已满（条数或字节预算），断开连接")
	s.finish(CloseReason{Kind: CloseQueueFull, Err: ErrSendQueueFull})
	return ErrSendQueueFull
}

// Request 发送请求并等待按 request_id 关联的响应。msg 须同时是 agent.Message；
// request_id 为空时由会话生成并经 agent.RequestIDSetter 写回（需传指针，否则返回 ErrNoRequestID）。
// timeout ≤ 0 表示不设超时（仍受 ctx 与会话关闭约束）；超时返回 ErrRequestTimeout，
// 会话关闭返回 ErrSessionClosed，ctx 结束返回 ctx.Err()。超时不断开会话。
func (s *Session) Request(ctx context.Context, msg agent.Requester, timeout time.Duration) (agent.Message, error) {
	m, ok := msg.(agent.Message)
	if !ok {
		return nil, fmt.Errorf("请求 %T 不是协议消息", msg)
	}
	id := msg.GetRequestID()
	if id == "" {
		setter, ok := msg.(agent.RequestIDSetter)
		if !ok {
			return nil, ErrNoRequestID
		}
		id = NewRequestID()
		setter.SetRequestID(id)
	}
	ch := make(chan agent.Message, 1)
	s.mu.Lock()
	if s.closing || s.finished {
		s.mu.Unlock()
		return nil, ErrSessionClosed
	}
	if _, dup := s.waiters[id]; dup {
		s.mu.Unlock()
		return nil, ErrDuplicateRequestID
	}
	s.waiters[id] = ch
	s.mu.Unlock()
	defer s.dropWaiter(id)

	// 计时器先于发送登记：对端可能立刻应答，测试也据此同步假时钟。
	var expired <-chan time.Time
	if timeout > 0 {
		expired = s.clk.After(timeout)
	}
	if err := s.Send(m); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-s.done:
		return nil, ErrSessionClosed
	case <-expired:
		return nil, ErrRequestTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) dropWaiter(id string) {
	s.mu.Lock()
	delete(s.waiters, id)
	s.mu.Unlock()
}

// deliver 把响应交给等待者；没有等待者返回假。
func (s *Session) deliver(id string, msg agent.Message) bool {
	s.mu.Lock()
	ch, ok := s.waiters[id]
	if ok {
		delete(s.waiters, id)
	}
	s.mu.Unlock()
	if ok {
		ch <- msg // 容量 1 且每个等待者只投递一次，不会阻塞
	}
	return ok
}

// Close 关闭会话并等到关闭完成，可重复与并发调用（后续调用只等待）。
// reason 非空时把 bye{reason} 排在已入队帧之后，先按序写出此前 Send 的帧再写 bye，
// 最多等 ByeFlushTimeout（走 Clock），超时即放弃；发送队列已满时不发 bye 直接关闭。
// reason 为空时不发 bye、不等待，已入队未写出的帧可能被丢弃；需要保证先前 Send 的帧送达，必须带 reason。
// 会话已因其他原因关闭时保留原有原因。
func (s *Session) Close(reason string) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		<-s.done
		return
	}
	s.closing = true
	s.local = &CloseReason{Kind: CloseLocal, Bye: reason}
	var flushed chan struct{}
	if reason != "" && !s.finished {
		if b, err := agent.Encode(&agent.Bye{Reason: reason}); err == nil {
			f := outFrame{data: b, flushed: make(chan struct{})}
			select {
			case s.sendQ <- f:
				flushed = f.flushed
			default:
			}
		}
	}
	s.mu.Unlock()
	if flushed != nil {
		timeout := s.clk.After(ByeFlushTimeout)
		select {
		case <-flushed:
		case <-timeout:
		case <-s.done:
		}
	}
	s.finish(CloseReason{Kind: CloseLocal, Bye: reason})
	// finish 可能已在别的协程中进行（如读错误），等它完成，保证返回时 Done 已关闭。
	<-s.done
}

// finish 只生效一次：记录原因（已调用 Close 时以本端原因为准）、取消 ctx、关闭连接、关闭 done。
// 等待者通过 done 立即失败。
func (s *Session) finish(r CloseReason) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	if s.local != nil {
		r = *s.local
	}
	r.At = s.clk.Now()
	s.reason = r
	s.mu.Unlock()

	s.cancel()
	_ = s.fc.Close()
	close(s.done)
	s.log.Info("agent 会话已关闭", "kind", string(r.Kind), "bye", r.Bye, "err", r.Err)
}

func (s *Session) readLoop() {
	for {
		b, err := s.fc.Read(s.ctx)
		if err != nil {
			s.finish(CloseReason{Kind: CloseReadError, Err: err})
			return
		}
		s.lastSeen.Store(s.clk.Now().UnixNano())
		if !s.handleFrame(b) {
			return
		}
	}
}

// handleFrame 处理一帧，返回假表示会话已结束。
func (s *Session) handleFrame(b []byte) bool {
	msg, err := agent.Decode(b)
	if errors.Is(err, agent.ErrUnknownType) {
		s.log.Warn("忽略未知类型的消息", "type", unknownType(b))
		return true
	}
	if err != nil {
		s.log.Warn("收到不合法的消息，断开连接", "err", err)
		s.finish(CloseReason{Kind: CloseProtocolError, Err: err})
		return false
	}
	switch m := msg.(type) {
	case *agent.Ping:
		pong, err := agent.Encode(&agent.Pong{Echo: m.SentAt, SentAt: s.clk.Now()})
		if err == nil {
			_ = s.enqueue(pong) // 失败时会话已关闭或因队列满断开，读协程下一轮读取即退出
		}
		return true
	case *agent.Pong:
		recvAt := s.clk.Now()
		mid := m.Echo.Add(recvAt.Sub(m.Echo) / 2)
		s.mu.Lock()
		s.skew, s.skewOK = m.SentAt.Sub(mid), true
		s.mu.Unlock()
		return true
	case *agent.Bye:
		s.finish(CloseReason{Kind: ClosePeerBye, Bye: m.Reason})
		return false
	}
	if r, ok := isResponse(msg); ok {
		if !s.deliver(r.GetRequestID(), msg) {
			s.log.Debug("丢弃没有等待者的响应", "type", string(msg.MsgType()))
		}
		return true
	}
	n := int64(len(b))
	if s.recvBytes.Add(n) <= s.maxBytes {
		select {
		case s.inbox <- inFrame{msg: msg, n: n}:
			return true
		default:
		}
	}
	s.recvBytes.Add(-n)
	s.log.Warn("agent 会话收件队列已满（条数或字节预算），断开连接", "type", string(msg.MsgType()))
	s.finish(CloseReason{Kind: CloseQueueFull, Err: ErrRecvQueueFull})
	return false
}

// isResponse 判断响应类消息：带 request_id 但不能被写入请求号（RequestIDSetter 只由请求类的指针实现；
// Decode 得到的都是指针，判定可靠）。
func isResponse(msg agent.Message) (agent.Requester, bool) {
	r, ok := msg.(agent.Requester)
	if !ok {
		return nil, false
	}
	if _, isRequest := msg.(agent.RequestIDSetter); isRequest {
		return nil, false
	}
	return r, true
}

// maxLoggedType 是日志里对端可控的 type 的最大字节数。
const maxLoggedType = 64

// unknownType 取出帧的 type 用于日志，截断到 maxLoggedType 字节且保持 UTF-8 合法。
func unknownType(b []byte) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b, &head) // Decode 已确认是合法 JSON 且有 type
	t := head.Type
	if len(t) > maxLoggedType {
		t = strings.ToValidUTF8(t[:maxLoggedType], "") + "…"
	}
	return t
}

func (s *Session) writeLoop() {
	for {
		select {
		case f := <-s.sendQ:
			wctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
			err := s.fc.Write(wctx, f.data)
			cancel()
			s.sendBytes.Add(-f.counted)
			if err != nil {
				s.finish(CloseReason{Kind: CloseWriteError, Err: err})
				return
			}
			if f.flushed != nil {
				close(f.flushed)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Session) dispatchLoop() {
	for {
		select {
		case m := <-s.inbox:
			s.recvBytes.Add(-m.n)
			if s.handler != nil {
				s.handler(s, m.msg)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// heartbeatLoop 每 HeartbeatInterval 发一次 ping。判活只用一个计时器：到期时若距最近一帧
// 不足 HeartbeatTimeout，就按剩余时长重新登记，否则判心跳超时。
func (s *Session) heartbeatLoop(ping, alive <-chan time.Time) {
	for {
		select {
		case <-ping:
			ping = s.clk.After(HeartbeatInterval)
			if b, err := agent.Encode(&agent.Ping{SentAt: s.clk.Now()}); err == nil {
				_ = s.enqueue(b)
			}
		case <-alive:
			idle := s.clk.Now().Sub(time.Unix(0, s.lastSeen.Load()))
			if idle < HeartbeatTimeout {
				alive = s.clk.After(HeartbeatTimeout - idle)
				continue
			}
			s.log.Warn("agent 会话心跳超时，断开连接")
			s.finish(CloseReason{Kind: CloseHeartbeat})
			return
		case <-s.ctx.Done():
			return
		}
	}
}
