// Package link 是 agent 侧的连接原语：拨入一次性配对（PairDialIn）、hello 组装与 mDNS 查询名。
// 守护进程与 install（C5）共用。
package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const (
	// HelloTimeout 是配对会话上发出 hello 后等待 hello_ok 或 incompatible 的上限（走 Clock），与 hub 的 hello 超时一致。
	HelloTimeout = 10 * time.Second
	// PairDialTimeout 是配对拨号（TCP、TLS、WebSocket 与 pair 往返）的上限（网络操作，用真实时间）。
	PairDialTimeout = 20 * time.Second
	// MaxNameBytes 是 pair 帧里 name 与 hostname 的长度上限（认证前单帧 ≤ 4 KiB）。
	MaxNameBytes = 256
	// DefaultMDNSName 是 hub 主机名未知时的 mDNS 查询名。
	DefaultMDNSName = "pimon.local"
)

// ErrHelloTimeout 表示配对已成功（凭据已落盘），但 hub 在 HelloTimeout 内没有回应 hello。
var ErrHelloTimeout = errors.New("hub 未在限定时间内回应 hello")

// IncompatibleError 表示配对已成功（凭据已落盘），但 hub 判定协议版本不兼容。
// hub 已记录原因并确认占位，agent 升级后用凭据连上即可接入。
type IncompatibleError struct {
	Reason      string
	RequiredMin int
	HubVersion  string
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("协议版本不兼容（%s，需要协议版本 ≥ %d，hub 版本 %s）", e.Reason, e.RequiredMin, e.HubVersion)
}

// HelloRejectedError 表示 hub 在回应 hello 之前断开了配对会话（如设备已被删除时为 bye{revoked}）。
type HelloRejectedError struct {
	Kind agentlink.CloseKind
	Bye  string
}

func (e *HelloRejectedError) Error() string {
	return fmt.Sprintf("hub 在回应 hello 前断开了连接（%s，bye %q）", e.Kind, e.Bye)
}

// PairRequest 是拨入一次性配对的参数。
type PairRequest struct {
	// HubURL 形如 wss://<IP>:31418；Pin 是 hub 证书的 SPKI 指纹。
	HubURL string
	Pin    certs.SPKI
	// Code 是网页上生成的一次性配对码。
	Code string
	// Name 是设备名，写进 pair 帧（截到 256 字节）。
	Name string
	// Role 是设备角色（server/nas/dev/vps）。pair 帧没有对应字段，hub 以配对码生成时预设的角色为准；
	// 这里只供调用方一并记录，不发送。
	Role string
	// Save 在收到 pair_ok 时调用，调用方在这里原子落盘凭据与 hub 主机名（ok.ListenerInfo.Hostname）；
	// 返回错误时配对失败，不再发 hello。必填。
	Save func(agent.PairOK) error
	// Hello 返回要在配对会话上发出的 hello（含可用插件 manifest）；其 Hostname 也作为 pair 帧的 hostname。必填。
	Hello func() agent.Hello
	// Clock 默认 clock.Real{}；Log 默认 slog.Default()。
	Clock clock.Clock
	Log   *slog.Logger
}

// PairResult 是配对的结果。凭据已经由 Save 落盘，这里不再返回凭据原文。
type PairResult struct {
	DeviceID string
	// HubHostname 是 hub 在 pair_ok 里告知的主机名（mDNS 兜底用）。
	HubHostname string
	// HubVersion 来自 hello_ok（未收到时取 pair_ok 里的版本）。
	HubVersion string
}

// PairDialIn 执行拨入一次性配对：一次性 agentlink.Dial（带配对码），在 OnCredential 里经 Save 落盘凭据，
// 接着在同一会话上发 hello，等 hello_ok 或 incompatible（HelloTimeout），最后关闭会话。
// 这样 hub 的占位当场就确认了，不会因占位清理留下孤儿凭据。
//
// 返回：成功时 err 为空；拨号阶段失败返回 agentlink.Dial 的错误（*agentlink.AuthFailError{bad_code|code_expired}、
// *certs.PinMismatchError、*agentlink.DialError 等），此时凭据未落盘；Save 失败返回包装其错误的错误；
// 凭据已落盘之后的失败（*IncompatibleError、ErrHelloTimeout、*HelloRejectedError、ctx 结束）同时返回带设备 ID 的结果。
func PairDialIn(ctx context.Context, req PairRequest) (PairResult, error) {
	if req.Save == nil || req.Hello == nil {
		return PairResult{}, fmt.Errorf("%w：配对缺少 Save 或 Hello", agentlink.ErrDialConfig)
	}
	clk, log := req.Clock, req.Log
	if clk == nil {
		clk = clock.Real{}
	}
	if log == nil {
		log = slog.Default()
	}
	hello := req.Hello()
	replies := make(chan agent.Message, 1)
	var ok agent.PairOK
	dctx, cancel := context.WithTimeout(ctx, PairDialTimeout)
	defer cancel()
	s, err := agentlink.Dial(dctx, agentlink.DialerConfig{
		URL: req.HubURL, Pin: req.Pin, Role: agent.RoleAgent, PairCode: req.Code,
		PairInfo: agent.Pair{
			Name: Clip(req.Name, MaxNameBytes), Hostname: Clip(hello.Hostname, MaxNameBytes),
			OS: goruntime.GOOS, Arch: goruntime.GOARCH,
		},
		Clock: clk, Log: log,
		Handler: func(agentlink.Peer) agentlink.Handler {
			return func(_ *agentlink.Session, m agent.Message) {
				switch m.(type) {
				case *agent.HelloOK, *agent.Incompatible:
					select {
					case replies <- m:
					default:
					}
				}
			}
		},
		OnCredential: func(p agent.PairOK) error {
			if err := req.Save(p); err != nil {
				return fmt.Errorf("保存凭据: %w", err)
			}
			ok = p
			return nil
		},
	})
	if err != nil {
		return PairResult{}, err
	}
	res := PairResult{DeviceID: ok.DeviceID, HubHostname: ok.ListenerInfo.Hostname, HubVersion: ok.ListenerInfo.Version}
	log = log.With("device_id", ok.DeviceID)

	// 计时器先于发送登记：hub 可能立刻应答，测试也据此同步假时钟。
	timeout := clk.After(HelloTimeout)
	if err := s.Send(&hello); err != nil {
		s.Close("")
		return res, fmt.Errorf("发送 hello: %w", err)
	}
	handle := func(m agent.Message) (PairResult, error) {
		switch m := m.(type) {
		case *agent.HelloOK:
			res.HubVersion = m.HubVersion
			s.Close(agent.ReasonShutdown)
			log.Info("拨入配对完成，hub 已接受 hello")
			return res, nil
		case *agent.Incompatible:
			s.Close(m.Reason)
			log.Warn("拨入配对完成，但 hub 判定协议版本不兼容", "reason", m.Reason,
				"required_min", m.RequiredMin, "hub_version", Clip(m.HubVersion, 64))
			return res, &IncompatibleError{Reason: m.Reason, RequiredMin: m.RequiredMin, HubVersion: Clip(m.HubVersion, 64)}
		}
		return res, nil // 不会到达：replies 只收这两种
	}
	select {
	case m := <-replies:
		return handle(m)
	case <-s.Done():
		select {
		case m := <-replies:
			return handle(m)
		default:
		}
		r := s.Err()
		return res, &HelloRejectedError{Kind: r.Kind, Bye: Clip(r.Bye, 64)}
	case <-timeout:
		s.Close(agent.ReasonShutdown)
		return res, ErrHelloTimeout
	case <-ctx.Done():
		s.Close(agent.ReasonShutdown)
		return res, ctx.Err()
	}
}

// NewHello 组装 hello：本端协议版本、软件版本、平台、主机名（截到 256 字节）与可用插件 manifest。
func NewHello(hostname string, plugins []agent.PluginManifest) agent.Hello {
	if plugins == nil {
		plugins = []agent.PluginManifest{}
	}
	return agent.Hello{
		ProtocolVersion: agent.ProtocolVersion, MinCompatibleVersion: agent.MinCompatibleVersion,
		AgentVersion: version.Version, OS: goruntime.GOOS, Arch: goruntime.GOARCH,
		Hostname: Clip(hostname, MaxNameBytes), Plugins: plugins,
	}
}

var labelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// MDNSName 返回 mDNS 兜底的查询名：hub 主机名的第一段加 .local；为空或第一段不是合法的 DNS 标签时用 pimon.local。
func MDNSName(hubHostname string) string {
	first, _, _ := strings.Cut(hubHostname, ".")
	if !labelRe.MatchString(first) {
		return DefaultMDNSName
	}
	return first + ".local"
}

// Clip 把 s 截到不超过 n 字节，并丢掉被截断的不完整 UTF-8 字符（对端可控字符串进日志、状态前使用）。
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
