package agentlink

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// dialCall 是桩拨号函数的一次调用，测试决定它返回什么。
type dialCall struct {
	ctx  context.Context
	resp chan dialResp
}

type dialResp struct {
	s   *Session
	err error
}

// reconnHarness 用桩拨号函数驱动 Reconnector：重连循环用 clk，会话用各自独立的假时钟，
// 这样 clk.Waiters() 只反映重连循环自己的退避等待。
type reconnHarness struct {
	t     *testing.T
	clk   *clock.Fake
	calls chan dialCall
	r     *Reconnector

	mu       sync.Mutex
	sessions []*Session

	runErr chan error
	cancel context.CancelFunc
}

func newReconnHarness(t *testing.T, mutate func(*ReconnectorConfig)) *reconnHarness {
	t.Helper()
	h := &reconnHarness{t: t, clk: clock.NewFake(t0), calls: make(chan dialCall), runErr: make(chan error, 1)}
	cfg := ReconnectorConfig{
		Clock: h.clk,
		Log:   quietLog(),
		Dial: func(ctx context.Context, _ DialerConfig) (*Session, error) {
			c := dialCall{ctx: ctx, resp: make(chan dialResp, 1)}
			select {
			case h.calls <- c:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			select {
			case r := <-c.resp:
				return r.s, r.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		OnSession: func(s *Session) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.sessions = append(h.sessions, s)
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h.r = NewReconnector(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.runErr <- h.r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		h.r.Stop()
	})
	return h
}

// expectDial 等待下一次拨号调用。
func (h *reconnHarness) expectDial() dialCall {
	h.t.Helper()
	select {
	case c := <-h.calls:
		return c
	case <-time.After(5 * time.Second):
		h.t.Fatal("等待拨号超时")
		return dialCall{}
	}
}

// fail 让下一次拨号返回 err。
func (h *reconnHarness) fail(err error) {
	h.t.Helper()
	h.expectDial().resp <- dialResp{err: err}
}

// succeed 让下一次拨号成功，返回会话与测试扮演的对端。
func (h *reconnHarness) succeed() (*Session, peerEnd) {
	h.t.Helper()
	c := h.expectDial()
	a, b := newPipe()
	s := newSession(a, Peer{DeviceID: "dev1", RemoteAddr: "10.0.0.1:31418", Role: agent.RoleHub},
		SessionOptions{Clock: clock.NewFake(t0), Log: quietLog()})
	h.t.Cleanup(func() { s.Close("") })
	c.resp <- dialResp{s: s}
	waitFor(h.t, "OnSession 收到会话", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.sessions) > 0 && h.sessions[len(h.sessions)-1] == s
	})
	return s, peerEnd{t: h.t, c: b}
}

// expectDelay 断言重连循环正好等待 d 后才发起下一次拨号。
func (h *reconnHarness) expectDelay(d time.Duration) dialCall {
	h.t.Helper()
	h.expectDelayRange(d, d)
	return h.expectDial()
}

// expectDelayRange 断言退避等待落在 [lo, hi] 内：lo 前一刻未触发，推进到 hi 时已触发。
func (h *reconnHarness) expectDelayRange(lo, hi time.Duration) {
	h.t.Helper()
	waitFor(h.t, "重连循环登记退避等待", func() bool { return h.clk.Waiters() == 1 })
	h.clk.Advance(lo - time.Nanosecond)
	if h.clk.Waiters() != 1 {
		h.t.Fatalf("退避在 %v 之前就触发了", lo)
	}
	h.clk.Advance(hi - lo + time.Nanosecond)
	if h.clk.Waiters() != 0 {
		h.t.Fatalf("退避在 %v 时仍未触发", hi)
	}
}

func (h *reconnHarness) waitRun() error {
	h.t.Helper()
	select {
	case err := <-h.runErr:
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatal("Run 未退出")
		return nil
	}
}

func fixedRand(v float64) func() float64 { return func() float64 { return v } }

var errNet = &DialError{Addr: "10.0.0.1:31418", Err: errors.New("connection refused")}

func TestReconnector_退避序列与精确抖动(t *testing.T) {
	// Rand 固定 0.75：系数 1 + (0.75×0.4 − 0.2) = 1.1。
	h := newReconnHarness(t, func(c *ReconnectorConfig) { c.Rand = fixedRand(0.75) })
	h.fail(errNet)
	for _, sec := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		d := sec * 1100 * time.Millisecond
		h.expectDelayRange(d, d)
		h.fail(errNet)
	}
}

func TestReconnector_抖动边界(t *testing.T) {
	for _, tc := range []struct {
		rand float64
		want time.Duration
	}{{0, 800 * time.Millisecond}, {0.5, time.Second}} {
		t.Run(fmt.Sprint(tc.rand), func(t *testing.T) {
			h := newReconnHarness(t, func(c *ReconnectorConfig) { c.Rand = fixedRand(tc.rand) })
			h.fail(errNet)
			h.expectDelayRange(tc.want, tc.want)
			h.fail(errNet)
		})
	}
}

func TestReconnector_默认随机抖动在正负20以内(t *testing.T) {
	h := newReconnHarness(t, nil)
	h.fail(errNet)
	for _, sec := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		base := sec * time.Second
		h.expectDelayRange(base*8/10, base*12/10)
		h.fail(errNet)
	}
}

func TestReconnector_连上后交给OnSession断开后继续(t *testing.T) {
	h := newReconnHarness(t, func(c *ReconnectorConfig) { c.Rand = fixedRand(0.5) })
	s, p := h.succeed()
	if h.clk.Waiters() != 0 {
		t.Fatal("会话存活期间不应有退避等待")
	}
	p.c.Close()
	if r := waitDone(t, s); r.Kind != CloseReadError {
		t.Fatalf("关闭原因 = %v", r.Kind)
	}
	h.expectDelay(time.Second).resp <- dialResp{err: errNet}
	h.expectDelayRange(2*time.Second, 2*time.Second)
}

func TestReconnector_存活满60秒复位退避(t *testing.T) {
	h := newReconnHarness(t, func(c *ReconnectorConfig) { c.Rand = fixedRand(0.5) })
	h.fail(errNet)
	h.expectDelayRange(time.Second, time.Second)
	h.fail(errNet)
	h.expectDelayRange(2*time.Second, 2*time.Second)
	h.fail(errNet)
	h.expectDelayRange(4*time.Second, 4*time.Second)
	_, p := h.succeed()
	h.clk.Advance(60 * time.Second)
	p.send(&agent.Bye{Reason: agent.ReasonShutdown})
	h.expectDelay(time.Second).resp <- dialResp{err: errNet}
	h.expectDelayRange(2*time.Second, 2*time.Second)
}

func TestReconnector_存活不足60秒不复位(t *testing.T) {
	h := newReconnHarness(t, func(c *ReconnectorConfig) { c.Rand = fixedRand(0.5) })
	h.fail(errNet)
	h.expectDelayRange(time.Second, time.Second)
	h.fail(errNet)
	h.expectDelayRange(2*time.Second, 2*time.Second)
	_, p := h.succeed()
	h.clk.Advance(60*time.Second - time.Nanosecond)
	p.c.Close()
	h.expectDelayRange(4*time.Second, 4*time.Second)
	h.fail(errNet)
}

func TestReconnector_MaxBackoff取30秒乘抖动(t *testing.T) {
	pin := &certs.PinMismatchError{}
	for name, mk := range map[string]func(h *reconnHarness){
		"钉扎失败": func(h *reconnHarness) { h.fail(fmt.Errorf("包装：%w", pin)) },
		"限流":   func(h *reconnHarness) { h.fail(&AuthFailError{Reason: agent.ReasonRateLimited}) },
		"版本过旧": func(h *reconnHarness) {
			_, p := h.succeed()
			p.send(&agent.Bye{Reason: agent.ReasonVersionTooOld})
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newReconnHarness(t, func(c *ReconnectorConfig) { c.Rand = fixedRand(0) })
			mk(h)
			h.expectDelayRange(24*time.Second, 24*time.Second)
			h.fail(errNet)
		})
	}
}

func TestReconnector_Policy判定停止时Run返回StopError(t *testing.T) {
	t.Run("拨号错误", func(t *testing.T) {
		h := newReconnHarness(t, nil)
		h.fail(&AuthFailError{Reason: agent.ReasonBadCredential})
		err := h.waitRun()
		var se *StopError
		if !errors.As(err, &se) || se.Close != nil {
			t.Fatalf("Run 返回 %v，期望拨号错误导致的 *StopError", err)
		}
		var af *AuthFailError
		if !errors.As(err, &af) || af.Reason != agent.ReasonBadCredential {
			t.Fatalf("StopError 应可 errors.As 到原错误: %v", err)
		}
	})
	t.Run("配置错误", func(t *testing.T) {
		h := newReconnHarness(t, nil)
		h.fail(fmt.Errorf("%w：缺少监听方证书指纹", ErrDialConfig))
		if err := h.waitRun(); !errors.Is(err, ErrDialConfig) {
			t.Fatalf("Run 返回 %v", err)
		}
	})
	t.Run("会话收到bye revoked", func(t *testing.T) {
		h := newReconnHarness(t, nil)
		_, p := h.succeed()
		p.send(&agent.Bye{Reason: agent.ReasonRevoked})
		err := h.waitRun()
		var se *StopError
		if !errors.As(err, &se) || se.Close == nil || se.Close.Kind != ClosePeerBye || se.Close.Bye != agent.ReasonRevoked {
			t.Fatalf("Run 返回 %v，期望带关闭原因的 *StopError", err)
		}
		if se.Err != nil {
			t.Fatalf("会话关闭导致的停止不应带拨号错误: %v", se.Err)
		}
	})
}

func TestReconnector_自定义Policy(t *testing.T) {
	var gotErr error
	var gotReason *CloseReason
	h := newReconnHarness(t, func(c *ReconnectorConfig) {
		c.Rand = fixedRand(0.5)
		c.Policy = func(err error, r *CloseReason) Decision {
			gotErr, gotReason = err, r
			return Stop
		}
	})
	_, p := h.succeed()
	p.c.Close()
	var se *StopError
	if err := h.waitRun(); !errors.As(err, &se) {
		t.Fatalf("Run 返回 %v", err)
	}
	if gotErr != nil || gotReason == nil || gotReason.Kind != CloseReadError {
		t.Fatalf("Policy 收到 err=%v reason=%+v", gotErr, gotReason)
	}
}

func TestReconnector_Stop关闭当前会话并等待退出(t *testing.T) {
	h := newReconnHarness(t, nil)
	s, p := h.succeed()
	h.r.Stop()
	if !isDone(s) {
		t.Fatal("Stop 返回时当前会话应已关闭")
	}
	if r := s.Err(); r.Kind != CloseLocal || r.Bye != agent.ReasonShutdown {
		t.Fatalf("关闭原因 = %+v，期望本端 bye{shutdown}", r)
	}
	if m, ok := p.recv().(*agent.Bye); !ok || m.Reason != agent.ReasonShutdown {
		t.Fatalf("对端应收到 bye{shutdown}，得到 %v", m)
	}
	if err := h.waitRun(); err != nil {
		t.Fatalf("Stop 后 Run 应返回 nil，得到 %v", err)
	}
	h.r.Stop() // 重复调用安全
}

func TestReconnector_Stop打断退避等待与拨号(t *testing.T) {
	t.Run("退避中", func(t *testing.T) {
		h := newReconnHarness(t, nil)
		h.fail(errNet)
		waitFor(t, "退避等待", func() bool { return h.clk.Waiters() == 1 })
		h.r.Stop()
		if err := h.waitRun(); err != nil {
			t.Fatalf("Run 返回 %v", err)
		}
	})
	t.Run("拨号中", func(t *testing.T) {
		h := newReconnHarness(t, nil)
		c := h.expectDial()
		h.r.Stop()
		if c.ctx.Err() == nil {
			t.Fatal("Stop 应取消进行中的拨号")
		}
		if err := h.waitRun(); err != nil {
			t.Fatalf("Run 返回 %v", err)
		}
	})
}

func TestReconnector_ctx取消退出且关闭当前会话(t *testing.T) {
	t.Run("会话中", func(t *testing.T) {
		h := newReconnHarness(t, nil)
		s, _ := h.succeed()
		h.cancel()
		if err := h.waitRun(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run 返回 %v，期望 context.Canceled", err)
		}
		if !isDone(s) {
			t.Fatal("Run 返回时当前会话应已关闭")
		}
	})
	t.Run("拨号中不算失败", func(t *testing.T) {
		called := false
		h := newReconnHarness(t, func(c *ReconnectorConfig) {
			c.Policy = func(error, *CloseReason) Decision { called = true; return Backoff }
		})
		h.expectDial()
		h.cancel()
		if err := h.waitRun(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run 返回 %v", err)
		}
		if called {
			t.Fatal("ctx 取消不应交给 Policy")
		}
	})
}

func TestReconnector_拨号成功时已停止则关闭会话(t *testing.T) {
	calls := make(chan dialCall, 1)
	// 这个桩拨号不理会 ctx，模拟取消与拨通恰好同时发生。
	h := newReconnHarness(t, func(c *ReconnectorConfig) {
		c.Dial = func(ctx context.Context, _ DialerConfig) (*Session, error) {
			call := dialCall{ctx: ctx, resp: make(chan dialResp, 1)}
			calls <- call
			r := <-call.resp
			return r.s, r.err
		}
	})
	c := <-calls
	a, _ := newPipe()
	s := newSession(a, Peer{DeviceID: "dev1"}, SessionOptions{Clock: clock.NewFake(t0), Log: quietLog()})
	t.Cleanup(func() { s.Close("") })
	h.cancel()
	waitFor(t, "拨号 ctx 取消", func() bool { return c.ctx.Err() != nil })
	c.resp <- dialResp{s: s}
	_ = h.waitRun()
	if !isDone(s) {
		t.Fatal("停止后才拨通的会话应被关闭")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sessions) != 0 {
		t.Fatal("停止后才拨通的会话不应交给 OnSession")
	}
}

func TestReconnector_Run只能调用一次且Stop先于Run(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{Dial: func(context.Context, DialerConfig) (*Session, error) {
		t.Error("Stop 之后不应再拨号")
		return nil, errNet
	}, Clock: clock.NewFake(t0), Log: quietLog()})
	r.Stop()
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Stop 之后 Run 应立即返回 nil，得到 %v", err)
	}
	if err := r.Run(context.Background()); !errors.Is(err, ErrReconnectorRunning) {
		t.Fatalf("第二次 Run 返回 %v", err)
	}
}

func TestDefaultPolicy(t *testing.T) {
	bye := func(reason string) *CloseReason { return &CloseReason{Kind: ClosePeerBye, Bye: reason} }
	for _, tc := range []struct {
		name   string
		err    error
		reason *CloseReason
		want   Decision
	}{
		{"配置错误", fmt.Errorf("%w：x", ErrDialConfig), nil, Stop},
		{"凭据错误", &AuthFailError{Reason: agent.ReasonBadCredential}, nil, Stop},
		{"已吊销", &AuthFailError{Reason: agent.ReasonRevoked}, nil, Stop},
		{"钉扎失败", fmt.Errorf("w: %w", &certs.PinMismatchError{}), nil, MaxBackoff},
		{"限流", &AuthFailError{Reason: agent.ReasonRateLimited}, nil, MaxBackoff},
		{"配对码错误", &AuthFailError{Reason: agent.ReasonBadCode}, nil, Backoff},
		{"配对码过期", &AuthFailError{Reason: agent.ReasonCodeExpired}, nil, Backoff},
		{"连接失败", errNet, nil, Backoff},
		{"握手超时", ErrHandshakeTimeout, nil, Backoff},
		{"握手协议错误", fmt.Errorf("%w：x", ErrHandshakeProtocol), nil, Backoff},
		{"其他错误", errors.New("x"), nil, Backoff},
		{"bye revoked", nil, bye(agent.ReasonRevoked), Stop},
		{"bye version_too_old", nil, bye(agent.ReasonVersionTooOld), MaxBackoff},
		{"bye version_too_new", nil, bye(agent.ReasonVersionTooNew), MaxBackoff},
		{"bye replaced", nil, bye(agent.ReasonReplaced), Backoff},
		{"bye shutdown", nil, bye(agent.ReasonShutdown), Backoff},
		{"心跳超时", nil, &CloseReason{Kind: CloseHeartbeat}, Backoff},
		{"读错误", nil, &CloseReason{Kind: CloseReadError}, Backoff},
		{"写错误", nil, &CloseReason{Kind: CloseWriteError}, Backoff},
		{"协议错误", nil, &CloseReason{Kind: CloseProtocolError}, Backoff},
		{"队列满", nil, &CloseReason{Kind: CloseQueueFull}, Backoff},
		{"本端关闭带 revoked 不算对端吊销", nil, &CloseReason{Kind: CloseLocal, Bye: agent.ReasonRevoked}, Backoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultPolicy(tc.err, tc.reason); got != tc.want {
				t.Fatalf("DefaultPolicy = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestStopError_文案(t *testing.T) {
	e1 := &StopError{Err: &AuthFailError{Reason: agent.ReasonRevoked}}
	if e1.Error() == "" || errors.Unwrap(e1) == nil {
		t.Fatal("拨号错误导致的 StopError 应有文案并可展开")
	}
	e2 := &StopError{Close: &CloseReason{Kind: ClosePeerBye, Bye: agent.ReasonRevoked}}
	if e2.Error() == "" || errors.Unwrap(e2) != nil {
		t.Fatalf("会话关闭导致的 StopError 文案 = %q", e2.Error())
	}
}
