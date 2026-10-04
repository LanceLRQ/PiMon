package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

var t0 = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

var errPipeClosed = errors.New("管道已关闭")

// fakeConn 是测试用 frameConn：in 供读取，out 接收写出；两端共享 closed，任一端关闭即两端都断。
type fakeConn struct {
	in, out   chan []byte
	closed    chan struct{}
	closeOnce *sync.Once
	readLimit atomic.Int64
	// blockWrite 为真时 Write 一直阻塞到连接关闭（模拟对端不读）。
	blockWrite bool
	// closeEntered 非空时 Close 先通知它，再等 closeRelease（模拟 finish 卡在关闭连接上）。
	closeEntered, closeRelease chan struct{}
}

// Read 与 TCP 一致：关闭前已写入的帧先读完，再报连接关闭（select 同时就绪时是随机的，需显式排序）。
func (c *fakeConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-c.in:
		return b, nil
	case <-c.closed:
		select {
		case b := <-c.in:
			return b, nil
		default:
		}
		return nil, errPipeClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *fakeConn) Write(ctx context.Context, b []byte) error {
	if c.blockWrite {
		select {
		case <-c.closed:
			return errPipeClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case c.out <- b:
		return nil
	case <-c.closed:
		return errPipeClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeConn) Close() error {
	if c.closeEntered != nil {
		select {
		case c.closeEntered <- struct{}{}:
		default:
		}
		<-c.closeRelease
	}
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) SetReadLimit(n int64) { c.readLimit.Store(n) }

// newPipe 返回对接的两端：a 写出的帧由 b 读到，反之亦然。
func newPipe() (*fakeConn, *fakeConn) {
	ab := make(chan []byte, 64)
	ba := make(chan []byte, 64)
	closed := make(chan struct{})
	once := &sync.Once{}
	a := &fakeConn{in: ba, out: ab, closed: closed, closeOnce: once}
	b := &fakeConn{in: ab, out: ba, closed: closed, closeOnce: once}
	return a, b
}

// peerEnd 是测试扮演的对端：直接在 fakeConn 上收发原始帧。
type peerEnd struct {
	t *testing.T
	c *fakeConn
}

func (p peerEnd) send(m agent.Message) {
	p.t.Helper()
	b, err := agent.Encode(m)
	if err != nil {
		p.t.Fatalf("Encode: %v", err)
	}
	p.sendRaw(b)
}

func (p peerEnd) sendRaw(b []byte) {
	p.t.Helper()
	select {
	case p.c.out <- b:
	case <-time.After(5 * time.Second):
		p.t.Fatal("发送帧超时")
	}
}

func (p peerEnd) recv() agent.Message {
	p.t.Helper()
	select {
	case b := <-p.c.in:
		m, err := agent.Decode(b)
		if err != nil {
			p.t.Fatalf("Decode %s: %v", b, err)
		}
		return m
	case <-time.After(5 * time.Second):
		p.t.Fatal("等待会话写出帧超时")
		return nil
	}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type recorder struct {
	mu   sync.Mutex
	msgs []agent.Message
}

func (r *recorder) handle(_ *Session, m agent.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
}

func (r *recorder) snapshot() []agent.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.Message(nil), r.msgs...)
}

type fixture struct {
	t    *testing.T
	clk  *clock.Fake
	s    *Session
	peer peerEnd
	conn *fakeConn
	rec  *recorder
}

// newFixture 起一个会话，对端由测试直接扮演；返回前等到心跳与判活两个计时器都已登记。
func newFixture(t *testing.T, mutate func(*SessionOptions, *fakeConn)) *fixture {
	t.Helper()
	a, b := newPipe()
	clk := clock.NewFake(t0)
	rec := &recorder{}
	opts := SessionOptions{Clock: clk, Handler: rec.handle, Log: quietLog()}
	if mutate != nil {
		mutate(&opts, a)
	}
	s := newSession(a, Peer{DeviceID: "dev1", RemoteAddr: "10.0.0.2:5000", Role: agent.RoleAgent}, opts)
	t.Cleanup(func() { s.Close("") })
	f := &fixture{t: t, clk: clk, s: s, peer: peerEnd{t: t, c: b}, conn: a, rec: rec}
	f.waitWaiters(2)
	return f
}

func (f *fixture) waitWaiters(n int) {
	f.t.Helper()
	waitFor(f.t, "假时钟等待者数量", func() bool { return f.clk.Waiters() == n })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitDone(t *testing.T, s *Session) CloseReason {
	t.Helper()
	select {
	case <-s.Done():
		return s.Err()
	case <-time.After(5 * time.Second):
		t.Fatal("会话未关闭")
		return CloseReason{}
	}
}

func isDone(s *Session) bool {
	select {
	case <-s.Done():
		return true
	default:
		return false
	}
}

func TestSession_构造时读上限为认证后上限(t *testing.T) {
	f := newFixture(t, nil)
	if got := f.conn.readLimit.Load(); got != agent.MaxFrame {
		t.Fatalf("读上限 = %d, 期望 %d", got, agent.MaxFrame)
	}
	if p := f.s.Peer(); p.DeviceID != "dev1" || p.RemoteAddr != "10.0.0.2:5000" || p.Role != agent.RoleAgent || p.ProtocolVersion != 0 {
		t.Fatalf("Peer = %+v", p)
	}
	f.s.SetProtocolVersion(1)
	if f.s.Peer().ProtocolVersion != 1 {
		t.Fatal("SetProtocolVersion 未生效")
	}
}

func TestSession_入站消息交给Handler(t *testing.T) {
	f := newFixture(t, nil)
	f.peer.send(&agent.Ack{IDs: []string{"e1"}})
	waitFor(t, "handler 收到消息", func() bool { return len(f.rec.snapshot()) == 1 })
	if ack, ok := f.rec.snapshot()[0].(*agent.Ack); !ok || ack.IDs[0] != "e1" {
		t.Fatalf("收到 %#v", f.rec.snapshot()[0])
	}
}

func TestSession_Send写出帧(t *testing.T) {
	f := newFixture(t, nil)
	if err := f.s.Send(&agent.Ack{IDs: []string{"x"}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if m, ok := f.peer.recv().(*agent.Ack); !ok || m.IDs[0] != "x" {
		t.Fatalf("对端收到 %#v", m)
	}
}

func TestSession_Send校验失败不入队(t *testing.T) {
	f := newFixture(t, nil)
	var ime *agent.InvalidMessageError
	if err := f.s.Send(&agent.Ack{}); !errors.As(err, &ime) {
		t.Fatalf("err = %v, 期望 InvalidMessageError", err)
	}
	if isDone(f.s) {
		t.Fatal("校验失败不应断开会话")
	}
}

func TestSession_未知类型记录并忽略(t *testing.T) {
	f := newFixture(t, nil)
	f.peer.sendRaw([]byte(`{"type":"from_future","x":1}`))
	f.peer.send(&agent.Ack{IDs: []string{"after"}})
	waitFor(t, "handler 收到后续消息", func() bool { return len(f.rec.snapshot()) == 1 })
	if isDone(f.s) {
		t.Fatal("未知类型不应断开")
	}
}

func TestSession_非法消息断开(t *testing.T) {
	f := newFixture(t, nil)
	f.peer.sendRaw([]byte(`not json`))
	r := waitDone(t, f.s)
	if r.Kind != CloseProtocolError {
		t.Fatalf("Kind = %q", r.Kind)
	}
	var ime *agent.InvalidMessageError
	if !errors.As(r.Err, &ime) {
		t.Fatalf("Err = %v", r.Err)
	}
	if !r.At.Equal(t0) {
		t.Fatalf("At = %v", r.At)
	}
}

func TestSession_读错误断开(t *testing.T) {
	f := newFixture(t, nil)
	_ = f.peer.c.Close()
	r := waitDone(t, f.s)
	if r.Kind != CloseReadError || !errors.Is(r.Err, errPipeClosed) {
		t.Fatalf("CloseReason = %+v", r)
	}
}

func TestSession_请求响应按请求号关联且不进Handler(t *testing.T) {
	f := newFixture(t, nil)
	type result struct {
		m   agent.Message
		err error
	}
	req := &agent.RunNow{InstanceID: "i1"}
	done := make(chan result, 1)
	go func() {
		m, err := f.s.Request(context.Background(), req, 10*time.Second)
		done <- result{m, err}
	}()
	got, ok := f.peer.recv().(*agent.RunNow)
	if !ok {
		t.Fatal("对端未收到 run_now")
	}
	if len(got.RequestID) != 32 {
		t.Fatalf("请求号 = %q, 期望 32 位十六进制", got.RequestID)
	}
	// 先回一个无关请求号的响应：没有等待者，会话层丢弃，不进 Handler。
	f.peer.send(&agent.Logs{RequestID: "other"})
	// 再发一条请求类消息，用来确认前面的帧都已处理过。
	f.peer.send(&agent.RunNow{RequestID: "marker", InstanceID: "m"})
	f.peer.send(&agent.RunResult{RequestID: got.RequestID, InstanceID: "i1",
		RunOutcome: agent.RunOutcome{Error: "boom", ErrorKind: agent.ErrorKindFailed}})
	r := <-done
	if r.err != nil {
		t.Fatalf("Request: %v", r.err)
	}
	if rr, ok := r.m.(*agent.RunResult); !ok || rr.RequestID != got.RequestID {
		t.Fatalf("响应 = %#v", r.m)
	}
	if req.RequestID != got.RequestID {
		t.Fatal("生成的请求号应写回调用方的消息")
	}
	waitFor(t, "标记消息进入 Handler", func() bool { return len(f.rec.snapshot()) == 1 })
	if m, ok := f.rec.snapshot()[0].(*agent.RunNow); !ok || m.RequestID != "marker" {
		t.Fatalf("Handler 收到 %#v（无主响应不应进入 Handler）", f.rec.snapshot()[0])
	}
}

func TestSession_入站请求即使请求号与等待者相同也进Handler(t *testing.T) {
	f := newFixture(t, nil)
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Request(context.Background(), &agent.LogsGet{RequestID: "same"}, 5*time.Second)
		done <- err
	}()
	f.peer.recv()
	// 对端发来的请求恰好用了同一个请求号：它是请求不是响应，必须进 Handler，不能交给等待者。
	f.peer.send(&agent.LogsGet{RequestID: "same"})
	waitFor(t, "入站请求进入 Handler", func() bool { return len(f.rec.snapshot()) == 1 })
	if _, ok := f.rec.snapshot()[0].(*agent.LogsGet); !ok {
		t.Fatalf("Handler 收到 %#v", f.rec.snapshot()[0])
	}
	select {
	case err := <-done:
		t.Fatalf("等待者不应被入站请求唤醒: %v", err)
	default:
	}
	f.peer.send(&agent.Logs{RequestID: "same"})
	if err := <-done; err != nil {
		t.Fatalf("Request: %v", err)
	}
}

func TestSession_出站帧超过上限返回错误且不断开(t *testing.T) {
	f := newFixture(t, nil)
	big := json.RawMessage(`"` + strings.Repeat("a", agent.MaxFrame) + `"`)
	err := f.s.Send(&agent.Event{ID: "e", Kind: "k", Data: big, At: t0})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if isDone(f.s) {
		t.Fatal("超长帧不应断开会话")
	}
	if err := f.s.Send(&agent.Ack{IDs: []string{"ok"}}); err != nil {
		t.Fatalf("随后的 Send: %v", err)
	}
	if m, ok := f.peer.recv().(*agent.Ack); !ok || m.IDs[0] != "ok" {
		t.Fatalf("对端收到 %#v（超长帧不应入队）", m)
	}
}

func TestSession_Close返回时Done已关闭(t *testing.T) {
	f := newFixture(t, func(_ *SessionOptions, c *fakeConn) {
		c.closeEntered = make(chan struct{}, 1)
		c.closeRelease = make(chan struct{})
	})
	// 对端关闭 → 读错误 → finish 卡在关闭连接上，done 尚未关闭。
	_ = f.peer.c.Close()
	select {
	case <-f.conn.closeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("finish 未进入关闭连接")
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(f.conn.closeRelease) }) }
	t.Cleanup(release) // 测试失败时也要放行，否则清理阶段的 Close 会一直等
	doneAtReturn := make(chan bool, 1)
	go func() {
		f.s.Close(agent.ReasonShutdown)
		doneAtReturn <- isDone(f.s)
	}()
	select {
	case <-doneAtReturn:
		t.Fatal("finish 未完成时 Close 不应返回")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if !<-doneAtReturn {
		t.Fatal("Close 返回时 Done 应已关闭")
	}
	if r := f.s.Err(); r.Kind != CloseReadError {
		t.Fatalf("已因读错误关闭，原因应保留: %+v", r)
	}
}

func TestSession_请求号已给定时原样使用(t *testing.T) {
	f := newFixture(t, nil)
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Request(context.Background(), agent.LogsGet{RequestID: "fixed"}, time.Second)
		done <- err
	}()
	if m := f.peer.recv().(*agent.LogsGet); m.RequestID != "fixed" {
		t.Fatalf("请求号 = %q", m.RequestID)
	}
	f.peer.send(&agent.Logs{RequestID: "fixed"})
	if err := <-done; err != nil {
		t.Fatalf("Request: %v", err)
	}
}

func TestSession_值类型且请求号为空时报错(t *testing.T) {
	f := newFixture(t, nil)
	if _, err := f.s.Request(context.Background(), agent.RunNow{InstanceID: "i1"}, time.Second); !errors.Is(err, ErrNoRequestID) {
		t.Fatalf("err = %v", err)
	}
}

func TestSession_请求超时走假时钟(t *testing.T) {
	f := newFixture(t, nil)
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Request(context.Background(), &agent.RunNow{InstanceID: "i1"}, 5*time.Second)
		done <- err
	}()
	f.peer.recv()
	f.waitWaiters(3)
	f.clk.Advance(5*time.Second - time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("未到超时就返回: %v", err)
	default:
	}
	f.clk.Advance(time.Millisecond)
	select {
	case err := <-done:
		if !errors.Is(err, ErrRequestTimeout) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("超时未返回")
	}
	if isDone(f.s) {
		t.Fatal("请求超时不应断开会话")
	}
}

func TestSession_关闭时等待者立即失败(t *testing.T) {
	f := newFixture(t, nil)
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Request(context.Background(), &agent.RunNow{InstanceID: "i1"}, time.Hour)
		done <- err
	}()
	f.peer.recv()
	_ = f.peer.c.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待者未立即失败")
	}
	if _, err := f.s.Request(context.Background(), &agent.RunNow{InstanceID: "i1"}, time.Hour); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("关闭后再请求 err = %v", err)
	}
	if err := f.s.Send(&agent.Ack{IDs: []string{"x"}}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("关闭后 Send err = %v", err)
	}
}

func TestSession_请求随ctx取消返回(t *testing.T) {
	f := newFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Request(ctx, &agent.RunNow{InstanceID: "i1"}, time.Hour)
		done <- err
	}()
	f.peer.recv()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestSession_发送队列满即断开(t *testing.T) {
	f := newFixture(t, func(o *SessionOptions, c *fakeConn) {
		o.SendQueueSize = 4
		c.blockWrite = true
	})
	var err error
	for i := 0; i < 10 && err == nil; i++ {
		err = f.s.Send(&agent.Ack{IDs: []string{"x"}})
	}
	if !errors.Is(err, ErrSendQueueFull) {
		t.Fatalf("err = %v", err)
	}
	r := waitDone(t, f.s)
	if r.Kind != CloseQueueFull || !errors.Is(r.Err, ErrSendQueueFull) {
		t.Fatalf("CloseReason = %+v", r)
	}
}

func TestSession_收件队列满即断开(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f := newFixture(t, func(o *SessionOptions, _ *fakeConn) {
		o.RecvQueueSize = 2
		o.Handler = func(*Session, agent.Message) { <-release }
	})
	for i := 0; i < 10 && !isDone(f.s); i++ {
		select {
		case f.peer.c.out <- mustEncode(t, &agent.Ack{IDs: []string{"x"}}):
		case <-f.s.Done():
		}
	}
	r := waitDone(t, f.s)
	if r.Kind != CloseQueueFull || !errors.Is(r.Err, ErrRecvQueueFull) {
		t.Fatalf("CloseReason = %+v", r)
	}
}

func mustEncode(t *testing.T, m agent.Message) []byte {
	t.Helper()
	b, err := agent.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSession_每15秒发ping(t *testing.T) {
	f := newFixture(t, nil)
	f.clk.Advance(HeartbeatInterval)
	p, ok := f.peer.recv().(*agent.Ping)
	if !ok || !p.SentAt.Equal(t0.Add(HeartbeatInterval)) {
		t.Fatalf("收到 %#v", p)
	}
}

func TestSession_收到ping立即回pong(t *testing.T) {
	f := newFixture(t, nil)
	sent := t0.Add(-time.Hour)
	f.peer.send(&agent.Ping{SentAt: sent})
	pong, ok := f.peer.recv().(*agent.Pong)
	if !ok || !pong.Echo.Equal(sent) || !pong.SentAt.Equal(t0) {
		t.Fatalf("pong = %#v", pong)
	}
	if len(f.rec.snapshot()) != 0 {
		t.Fatal("ping 不应进入 Handler")
	}
}

func TestSession_pong计算时钟偏移(t *testing.T) {
	f := newFixture(t, nil)
	if _, ok := f.s.ClockSkew(); ok {
		t.Fatal("未收到 pong 前不应有偏移")
	}
	f.clk.Advance(HeartbeatInterval)
	ping := f.peer.recv().(*agent.Ping)
	f.waitWaiters(2)
	// 往返 4 秒：本端收到时刻 = 发送 + 4s，中点 = 发送 + 2s；对端时钟比本端快 100s。
	f.clk.Advance(4 * time.Second)
	f.peer.send(&agent.Pong{Echo: ping.SentAt, SentAt: ping.SentAt.Add(102 * time.Second)})
	waitFor(t, "时钟偏移可用", func() bool { _, ok := f.s.ClockSkew(); return ok })
	if skew, _ := f.s.ClockSkew(); skew != 100*time.Second {
		t.Fatalf("偏移 = %v", skew)
	}
}

func TestSession_心跳34秒不断35秒断(t *testing.T) {
	f := newFixture(t, nil)
	f.clk.Advance(34 * time.Second)
	f.peer.recv() // 15 秒的 ping，说明心跳协程已处理到期计时器
	f.waitWaiters(2)
	if isDone(f.s) {
		t.Fatal("34 秒不应断开")
	}
	f.clk.Advance(time.Second)
	r := waitDone(t, f.s)
	if r.Kind != CloseHeartbeat {
		t.Fatalf("Kind = %q", r.Kind)
	}
	if !r.At.Equal(t0.Add(HeartbeatTimeout)) {
		t.Fatalf("At = %v", r.At)
	}
}

func TestSession_任何入站帧重置判活计时(t *testing.T) {
	f := newFixture(t, nil)
	f.clk.Advance(20 * time.Second)
	f.peer.recv() // 15 秒的 ping
	f.waitWaiters(2)
	f.peer.send(&agent.Ack{IDs: []string{"alive"}}) // t=20 收到帧
	waitFor(t, "帧已处理", func() bool { return len(f.rec.snapshot()) == 1 })
	f.clk.Advance(15 * time.Second) // t=35：原判活计时器到期，应按剩余时长顺延到 55
	f.peer.recv()                   // 35 秒的 ping
	f.waitWaiters(2)                // ping@50 与 alive@55 都已重新登记，判活计时器始终只有一个
	if isDone(f.s) {
		t.Fatal("最后一帧后 15 秒不应断开")
	}
	f.clk.Advance(19 * time.Second) // t=54
	f.peer.recv()                   // 50 秒的 ping，说明心跳协程已处理到此刻
	f.waitWaiters(2)
	if isDone(f.s) {
		t.Fatal("最后一帧后 34 秒不应断开")
	}
	f.clk.Advance(time.Second) // t=55
	if r := waitDone(t, f.s); r.Kind != CloseHeartbeat {
		t.Fatalf("Kind = %q", r.Kind)
	}
}

func TestSession_bye原因传给对端(t *testing.T) {
	a, b := newPipe()
	clk := clock.NewFake(t0)
	sa := newSession(a, Peer{DeviceID: "d"}, SessionOptions{Clock: clk, Log: quietLog()})
	sb := newSession(b, Peer{DeviceID: "h"}, SessionOptions{Clock: clk, Log: quietLog()})
	sa.Close(agent.ReasonShutdown)
	ra := waitDone(t, sa)
	if ra.Kind != CloseLocal || ra.Bye != agent.ReasonShutdown {
		t.Fatalf("本端 CloseReason = %+v", ra)
	}
	rb := waitDone(t, sb)
	if rb.Kind != ClosePeerBye || rb.Bye != agent.ReasonShutdown {
		t.Fatalf("对端 CloseReason = %+v", rb)
	}
	sb.Close("")
}

func TestSession_bye写不出时最多等1秒(t *testing.T) {
	f := newFixture(t, func(_ *SessionOptions, c *fakeConn) { c.blockWrite = true })
	closed := make(chan struct{})
	go func() {
		f.s.Close(agent.ReasonRevoked)
		close(closed)
	}()
	f.waitWaiters(3)
	f.clk.Advance(ByeFlushTimeout - time.Millisecond)
	select {
	case <-closed:
		t.Fatal("未到 1 秒就关闭")
	default:
	}
	f.clk.Advance(time.Millisecond)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("1 秒后仍未关闭")
	}
	if r := f.s.Err(); r.Kind != CloseLocal || r.Bye != agent.ReasonRevoked {
		t.Fatalf("CloseReason = %+v", r)
	}
}

func TestSession_重复与并发Close安全(t *testing.T) {
	f := newFixture(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.s.Close(agent.ReasonShutdown)
		}()
	}
	wg.Wait()
	f.s.Close("")
	if r := f.s.Err(); r.Kind != CloseLocal || r.Bye != agent.ReasonShutdown {
		t.Fatalf("CloseReason = %+v", r)
	}
	if _, ok := f.peer.recv().(*agent.Bye); !ok {
		t.Fatal("对端应收到 bye")
	}
	select {
	case b := <-f.peer.c.in:
		t.Fatalf("只应发出一个 bye，又收到: %s", b)
	default:
	}
}

func TestSession_空原因Close不发bye(t *testing.T) {
	f := newFixture(t, nil)
	f.s.Close("")
	if r := f.s.Err(); r.Kind != CloseLocal || r.Bye != "" {
		t.Fatalf("CloseReason = %+v", r)
	}
	select {
	case b := <-f.peer.c.in:
		t.Fatalf("不应写出帧: %s", b)
	default:
	}
}

func TestSession_写错误断开(t *testing.T) {
	f := newFixture(t, func(_ *SessionOptions, c *fakeConn) { c.blockWrite = true })
	if err := f.s.Send(&agent.Ack{IDs: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	// 对端关闭：读协程与写协程都会出错，先到者决定原因，二者都属于连接错误。
	_ = f.peer.c.Close()
	r := waitDone(t, f.s)
	if r.Kind != CloseReadError && r.Kind != CloseWriteError {
		t.Fatalf("Kind = %q", r.Kind)
	}
}

func TestSession_Handler里调用Close不死锁(t *testing.T) {
	var s *Session
	ready := make(chan struct{})
	f := newFixture(t, func(o *SessionOptions, _ *fakeConn) {
		o.Handler = func(sess *Session, _ agent.Message) {
			<-ready
			sess.Close(agent.ReasonReplaced)
		}
	})
	s = f.s
	close(ready)
	f.peer.send(&agent.Ack{IDs: []string{"x"}})
	if _, ok := f.peer.recv().(*agent.Bye); !ok {
		t.Fatal("对端应收到 bye")
	}
	if r := waitDone(t, s); r.Kind != CloseLocal || r.Bye != agent.ReasonReplaced {
		t.Fatalf("CloseReason = %+v", r)
	}
}

func TestUnknownType_截断到64字节且保持UTF8(t *testing.T) {
	if got := unknownType([]byte(`{"type":"from_future"}`)); got != "from_future" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("a", 63) + "汉字"
	got := unknownType([]byte(`{"type":"` + long + `"}`))
	if got != strings.Repeat("a", 63)+"…" {
		t.Fatalf("got %q", got)
	}
}

// 发件字节预算：条数远未满，但积压字节超出预算即按 queue_full 断开。
func TestSession_发件超出字节预算即断开(t *testing.T) {
	f := newFixture(t, func(o *SessionOptions, c *fakeConn) {
		o.MaxQueuedBytes = 1000
		c.blockWrite = true
	})
	big := &agent.Ack{IDs: []string{strings.Repeat("x", 400)}}
	var err error
	for i := 0; i < 10 && err == nil; i++ {
		err = f.s.Send(big)
	}
	if !errors.Is(err, ErrSendQueueFull) {
		t.Fatalf("err = %v", err)
	}
	r := waitDone(t, f.s)
	if r.Kind != CloseQueueFull || !errors.Is(r.Err, ErrSendQueueFull) {
		t.Fatalf("CloseReason = %+v", r)
	}
}

// 收件字节预算：Handler 卡住时积压字节超出预算即按 queue_full 断开。
func TestSession_收件超出字节预算即断开(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f := newFixture(t, func(o *SessionOptions, _ *fakeConn) {
		o.MaxQueuedBytes = 1000
		o.Handler = func(*Session, agent.Message) { <-release }
	})
	big := mustEncode(t, &agent.Ack{IDs: []string{strings.Repeat("x", 400)}})
	for i := 0; i < 10 && !isDone(f.s); i++ {
		select {
		case f.peer.c.out <- big:
		case <-f.s.Done():
		}
	}
	r := waitDone(t, f.s)
	if r.Kind != CloseQueueFull || !errors.Is(r.Err, ErrRecvQueueFull) {
		t.Fatalf("CloseReason = %+v", r)
	}
}

// 预算内的大帧（接近单帧上限的一半）能正常收发，且写出、取出后预算归还。
func TestSession_预算内大帧正常收发(t *testing.T) {
	f := newFixture(t, nil)
	big := strings.Repeat("x", 4<<20)
	for i := 0; i < 3; i++ {
		if err := f.s.Send(&agent.Ack{IDs: []string{big}}); err != nil {
			t.Fatalf("第 %d 次 Send: %v", i, err)
		}
		if a, ok := f.peer.recv().(*agent.Ack); !ok || len(a.IDs[0]) != len(big) {
			t.Fatalf("对端收到 %#v", a)
		}
		f.peer.send(&agent.Ack{IDs: []string{big}})
	}
	waitFor(t, "Handler 收到 3 个大帧", func() bool { return len(f.rec.snapshot()) == 3 })
	waitFor(t, "预算归还", func() bool { return f.s.sendBytes.Load() == 0 && f.s.recvBytes.Load() == 0 })
}
