package agentlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 重连退避参数：序列 1、2、4、8、16、30 秒，每次乘以 1 + (Rand()×0.4 − 0.2) 的抖动；
// 会话存活满 StableAfter 后断开，退避复位到序列开头。
// hub 启动宽限需 ≥ 最大退避（30×1.2=36 秒）+ 心跳超时（35 秒）= 71 秒。
const (
	// MaxBackoffDelay 是退避序列的上限，也是 MaxBackoff 判定使用的基准间隔。
	MaxBackoffDelay = 30 * time.Second
	// StableAfter 是会话被视为稳定的存活时长，满足后断开时退避复位。
	StableAfter = 60 * time.Second
	// backoffJitter 是抖动幅度（±20%）。
	backoffJitter = 0.2
)

// backoffSteps 是普通退避的基准间隔序列，越过末尾后保持最大值。
var backoffSteps = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, MaxBackoffDelay,
}

// ErrReconnectorRunning 表示 Run 被重复调用。
var ErrReconnectorRunning = errors.New("重连循环已在运行或已结束")

// Decision 是 Policy 对一次失败（拨号失败或会话断开）的处置。
type Decision int

const (
	// Backoff 按退避序列等待后重连。
	Backoff Decision = iota
	// MaxBackoff 按最大退避间隔（30 秒乘抖动）等待后重连，用于短期内重试无望但可能自行恢复的情况。
	MaxBackoff
	// Stop 停止重连，Run 返回 *StopError。
	Stop
)

func (d Decision) String() string {
	switch d {
	case Backoff:
		return "backoff"
	case MaxBackoff:
		return "max_backoff"
	case Stop:
		return "stop"
	}
	return fmt.Sprintf("decision(%d)", int(d))
}

// Policy 决定失败后如何处置：拨号失败时 err 非空、reason 为 nil；会话断开时 err 为 nil、reason 为关闭原因。
// 重连循环自身的 ctx 结束（Stop 或调用方取消）不交给 Policy。
// Policy 在重连循环的协程里同步调用，不得在其中调用 Reconnector.Stop（会死锁），需要停止时另起协程或取消 ctx。
type Policy func(err error, reason *CloseReason) Decision

// DefaultPolicy 是默认处置：
//   - 停止：配置错误 ErrDialConfig；auth_fail{bad_credential|revoked}；对端 bye{revoked}。
//     凭据失效后继续重试只会让监听方按来源 IP 锁定，必须由用户重新配对。
//   - 最大退避：钉扎失败；auth_fail{rate_limited}；bye{version_too_old|version_too_new}，对端发来或本端关闭均可。
//     hub 发 incompatible 后直接断开、不发 bye，约定上层收到 incompatible 时调用 s.Close(m.Reason)，
//     使关闭原因为 CloseLocal + 版本原因，从而按最大退避而不是密集重连。
//   - revoked 只认对端 bye（本端 Close 带 revoked 不代表自己被吊销）。
//   - 其余（连接失败、握手超时或协议错误、其他 auth_fail 原因、会话断开、bye{replaced|shutdown}）按退避序列。
func DefaultPolicy(err error, reason *CloseReason) Decision {
	if err != nil {
		var pin *certs.PinMismatchError
		var af *AuthFailError
		switch {
		case errors.Is(err, ErrDialConfig):
			return Stop
		case errors.As(err, &pin):
			return MaxBackoff
		case errors.As(err, &af):
			switch af.Reason {
			case agent.ReasonBadCredential, agent.ReasonRevoked:
				return Stop
			case agent.ReasonRateLimited:
				return MaxBackoff
			}
		}
		return Backoff
	}
	if reason == nil {
		return Backoff
	}
	switch reason.Bye {
	case agent.ReasonRevoked:
		if reason.Kind == ClosePeerBye {
			return Stop
		}
	case agent.ReasonVersionTooOld, agent.ReasonVersionTooNew:
		if reason.Kind == ClosePeerBye || reason.Kind == CloseLocal {
			return MaxBackoff
		}
	}
	return Backoff
}

// StopError 表示 Policy 判定停止重连，由 Run 返回。
// 拨号失败导致时 Err 为拨号错误（可 errors.As 到 *AuthFailError 等）；会话断开导致时 Close 为关闭原因。
type StopError struct {
	Err   error
	Close *CloseReason
}

func (e *StopError) Error() string {
	if e.Err != nil {
		return "停止重连：" + e.Err.Error()
	}
	if e.Close != nil {
		if e.Close.Bye != "" {
			return fmt.Sprintf("停止重连：会话断开（%s，bye %s）", e.Close.Kind, e.Close.Bye)
		}
		return fmt.Sprintf("停止重连：会话断开（%s）", e.Close.Kind)
	}
	return "停止重连"
}

func (e *StopError) Unwrap() error { return e.Err }

// ReconnectorConfig 是重连循环的配置。
type ReconnectorConfig struct {
	// Dialer 是每次拨号使用的配置，原样传给 Dial。只支持凭据模式：带 PairCode 时 Run 立即返回
	// *StopError{ErrDialConfig}。一次性配对码重连必然 bad_code，配对须由上层用一次性 Dial 完成，
	// 拿到凭据（OnCredential）后再用凭据构造 Reconnector。
	Dialer DialerConfig
	// Dial 为空时用 agentlink.Dial；测试可注入桩函数。
	Dial func(ctx context.Context, cfg DialerConfig) (*Session, error)
	// Policy 为空时用 DefaultPolicy。
	Policy Policy
	// OnSession 在每次连上后同步调用，应快速返回；会话断开后重连循环继续。可为空。
	// 上层收到 incompatible 时应调用 s.Close(m.Reason)，让 DefaultPolicy 按最大退避处置。
	// 不得在 OnSession 内调用 Reconnector.Stop（会死锁），需要停止时另起协程或取消 ctx。
	OnSession func(*Session)
	// Clock 为空时用 Dialer.Clock，再为空用 clock.Real{}。
	Clock clock.Clock
	// Rand 返回 [0,1) 的随机数，用于抖动；为空时用 math/rand/v2。
	Rand func() float64
	// Log 为空时用 Dialer.Log，再为空用 slog.Default()。
	Log *slog.Logger
}

// Reconnector 包住拨号方：失败按 Policy 退避重连，连上后把会话交给 OnSession 并等它断开，
// 存活满 StableAfter 的会话断开后退避复位。
//
// 退出方式与 Run 的返回值：
//   - Stop()：返回 nil；
//   - 调用方 ctx 结束：返回 ctx.Err()（不算失败，不经 Policy）；
//   - Policy 判定 Stop：返回 *StopError，上层据此显示原因（如凭据失效需重新配对）。
//
// 前两种情况下当前会话以 bye{shutdown} 关闭，Run 在会话关闭后才返回。
type Reconnector struct {
	cfg ReconnectorConfig

	stopCtx    context.Context
	stopCancel context.CancelFunc
	exited     chan struct{}

	mu      sync.Mutex
	started bool
}

// NewReconnector 按 cfg 创建重连循环，调用 Run 后开始拨号。
func NewReconnector(cfg ReconnectorConfig) *Reconnector {
	if cfg.Dial == nil {
		cfg.Dial = Dial
	}
	if cfg.Policy == nil {
		cfg.Policy = DefaultPolicy
	}
	if cfg.Clock == nil {
		cfg.Clock = cfg.Dialer.Clock
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	if cfg.Log == nil {
		cfg.Log = cfg.Dialer.Log
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &Reconnector{cfg: cfg, stopCtx: stopCtx, stopCancel: stopCancel, exited: make(chan struct{})}
}

// Stop 停止重连循环：取消进行中的拨号或退避等待，关闭当前会话，并等 Run 返回。
// 可重复调用；在 Run 之前调用时 Run 立即返回 nil。
// 不得在 OnSession 或 Policy 内调用（Run 的协程在等它们返回，会死锁），需要停止时另起协程或取消 ctx。
func (r *Reconnector) Stop() {
	r.stopCancel()
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if started {
		<-r.exited
	}
}

// Run 运行重连循环直到退出，返回值见 Reconnector 的说明。只能调用一次，重复调用返回 ErrReconnectorRunning。
func (r *Reconnector) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return ErrReconnectorRunning
	}
	r.started = true
	r.mu.Unlock()
	defer close(r.exited)
	// AfterFunc 在独立协程里取消，Run 之前已 Stop 时先行返回，避免多拨一次。
	if r.stopCtx.Err() != nil {
		return nil
	}
	if r.cfg.Dialer.PairCode != "" {
		return &StopError{Err: fmt.Errorf("%w：重连循环只支持凭据模式，配对须先用 Dial 完成", ErrDialConfig)}
	}

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopAfter := context.AfterFunc(r.stopCtx, cancel)
	defer stopAfter()

	err := r.loop(loopCtx)
	if err == nil && r.stopCtx.Err() == nil {
		// 循环因调用方 ctx 结束而退出。
		return ctx.Err()
	}
	return err
}

// loop 返回 nil 表示 ctx 结束退出，否则返回 *StopError。
func (r *Reconnector) loop(ctx context.Context) error {
	clk, log := r.cfg.Clock, r.cfg.Log
	step := 0
	last := Decision(-1) // 首次决策总是按 Info 记录
	logDecision := func(msg string, d Decision, attrs ...any) {
		lvl := slog.LevelDebug
		if d != last || d == Stop {
			lvl = slog.LevelInfo
		}
		last = d
		log.Log(context.Background(), lvl, msg, append(attrs, "decision", d.String())...)
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		var decision Decision
		s, err := r.cfg.Dial(ctx, r.cfg.Dialer)
		if err == nil && s == nil {
			err = fmt.Errorf("%w：拨号返回空会话", ErrHandshakeProtocol)
		}
		switch {
		case ctx.Err() != nil:
			// 停止与拨通同时发生时，拨通的会话不交给上层，直接关闭。
			if s != nil {
				s.Close(agent.ReasonShutdown)
			}
			return nil
		case err != nil:
			decision = r.cfg.Policy(err, nil)
			logDecision("agent 通道重连：拨号失败", decision, "err", err)
			if decision == Stop {
				return &StopError{Err: err}
			}
		default:
			start := clk.Now()
			if r.cfg.OnSession != nil {
				r.cfg.OnSession(s)
			}
			select {
			case <-s.Done():
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				s.Close(agent.ReasonShutdown)
				return nil
			}
			reason := s.Err()
			if clk.Now().Sub(start) >= StableAfter {
				step = 0
			}
			decision = r.cfg.Policy(nil, &reason)
			logDecision("agent 通道重连：会话断开", decision, "kind", string(reason.Kind), "bye", reason.Bye)
			if decision == Stop {
				return &StopError{Close: &reason}
			}
		}

		var base time.Duration
		if decision == MaxBackoff {
			// 进入最大退避后，后续普通失败也保持最大间隔，直到出现稳定会话才复位。
			step = len(backoffSteps) - 1
			base = MaxBackoffDelay
		} else {
			base = backoffSteps[min(step, len(backoffSteps)-1)]
			step = min(step+1, len(backoffSteps)-1)
		}
		delay := r.jitter(base)
		select {
		case <-clk.After(delay):
		case <-ctx.Done():
			return nil
		}
	}
}

// jitter 返回 base × (1 + (Rand()×0.4 − 0.2))，四舍五入到纳秒。
func (r *Reconnector) jitter(base time.Duration) time.Duration {
	factor := 1 + (r.cfg.Rand()*2*backoffJitter - backoffJitter)
	return time.Duration(math.Round(float64(base) * factor))
}
