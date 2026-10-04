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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// DefaultPort 是 agent 通道的默认端口，URL 未写端口时使用。
const DefaultPort = "31418"

// dialTimeout 是单次连接尝试（TCP/代理、TLS、HTTP 升级）的上限，网络操作用真实时间；
// 握手应答的等待另由 HandshakeTimeout 走 Clock。
var dialTimeout = 10 * time.Second

var (
	// ErrDialConfig 表示拨号配置不合法（URL、角色、指纹、凭据与配对码的组合），重试无意义。
	ErrDialConfig = errors.New("拨号配置不合法")
	// ErrHandshakeTimeout 表示升级成功后 HandshakeTimeout 内没有收到监听方的认证应答。
	ErrHandshakeTimeout = errors.New("等待认证应答超时")
	// ErrHandshakeProtocol 表示监听方的握手应答不合法：类型不对、无法解码或设备 ID 与凭据不符。
	ErrHandshakeProtocol = errors.New("握手应答不合法")
)

// maxPeerReason 是 AuthFailError.Reason 的最大字节数，超出部分按 UTF-8 合法截断。
const maxPeerReason = 64

// AuthFailError 表示监听方回了 auth_fail，Reason 取 agent.Reason*（对端可控，截到 maxPeerReason 字节）。
type AuthFailError struct {
	Reason string
}

func (e *AuthFailError) Error() string { return "监听方拒绝认证：" + e.Reason }

// DialError 表示建立连接失败：TCP/代理拨号、TLS（钉扎失败除外）、HTTP 升级，或握手阶段连接中断。
// 钉扎失败不归此类，返回可 errors.As 到 *certs.PinMismatchError 的错误。
type DialError struct {
	// Addr 是本次尝试拨的 host:port（Fallback 时为备用地址）。
	Addr string
	Err  error
}

func (e *DialError) Error() string { return fmt.Sprintf("连接 %s 失败：%v", e.Addr, e.Err) }

func (e *DialError) Unwrap() error { return e.Err }

// DialerConfig 是拨号方的配置。Credential 与 PairCode 必须且只能给一个。
type DialerConfig struct {
	// URL 形如 wss://host[:port][/agent]；端口为空时用 DefaultPort，路径为空时补 /agent，其他 scheme 或路径拒绝。
	URL string
	// Pin 是监听方证书的 SPKI 指纹，必填（零值视为配置错误）；只比对它，不看主机名与有效期。
	Pin certs.SPKI
	// Role 是本端业务角色（agent.RoleHub 或 agent.RoleAgent）。
	Role string
	// Credential 是长期凭据，用 auth 认证。
	Credential string
	// PairCode 与 PairInfo 用 pair 配对换凭据；PairInfo 的 Code 与 Role 由拨号方填写。
	PairCode string
	PairInfo agent.Pair
	// Proxy 为空或直连时直接拨号；配置了代理时不使用 Fallback。
	Proxy *proxy.Proxy
	// Fallback 在直拨的 TCP/TLS 阶段失败且未配置代理时调用，返回备用地址（端口沿用 URL 的端口）。
	Fallback func(ctx context.Context) ([]netip.Addr, error)
	// Clock 默认 clock.Real{}；握手应答超时与会话心跳都走它。
	Clock clock.Clock
	// Log 默认 slog.Default()。
	Log *slog.Logger
	// Handler 为会话构造入站消息处理器，在会话开始读之前调用；为空或返回空时入站业务消息被丢弃。
	Handler func(Peer) Handler
	// OnCredential 在配对成功后、交出会话前调用，上层在这里落盘凭据；返回错误时关闭连接，
	// Dial 返回包装该错误的错误。配对模式下必填（否则签发的凭据无处保存，配对码却已被消耗）。
	OnCredential func(agent.PairOK) error
}

// dialURL 是校验后的拨号目标。
type dialURL struct {
	hostport string // URL 中的 host:port
	host     string // 不含端口的主机名或 IP
	port     string
	path     string
}

// Dial 按 cfg 拨号：钉扎 TLS + WebSocket 升级，发 auth 或 pair 并等待应答，成功后返回已启动的会话。
//
// 错误分类：配置错误 ErrDialConfig；钉扎失败 *certs.PinMismatchError；auth_fail → *AuthFailError；
// 应答超时 ErrHandshakeTimeout；应答不合法 ErrHandshakeProtocol；配对后 OnCredential 失败包装其错误；
// 其余连接失败 *DialError。调用方 ctx 取消时（任何阶段）直接返回 ctx.Err()。
func Dial(ctx context.Context, cfg DialerConfig) (*Session, error) {
	tgt, err := validateDialConfig(cfg)
	if err != nil {
		return nil, err
	}
	cfg = withDialDefaults(cfg)
	log := cfg.Log.With("url", tgt.display())

	c, remote, err := connect(ctx, cfg, tgt, tgt.hostport)
	if err != nil && cfg.Fallback != nil && cfg.Proxy.IsDirect() && isConnectStageError(err) {
		c, remote, err = dialFallback(ctx, cfg, tgt, log, err)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Debug("agent 通道拨号失败", "err", err)
		return nil, err
	}

	fc := newWSFrameConn(c)
	peer, err := dialHandshake(ctx, cfg, fc, remote)
	if err != nil {
		_ = fc.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Info("agent 通道握手失败", "remote_addr", remote, "err", err)
		return nil, err
	}
	var h Handler
	if cfg.Handler != nil {
		h = cfg.Handler(peer)
	}
	s := newSession(fc, peer, SessionOptions{Clock: cfg.Clock, Handler: h, Log: cfg.Log})
	log.Info("agent 通道拨号成功", "device_id", peer.DeviceID, "remote_addr", peer.RemoteAddr, "role", peer.Role)
	return s, nil
}

// display 返回规范化后的 URL，用于日志。
func (u dialURL) display() string { return "wss://" + u.hostport + u.path }

func validateDialConfig(cfg DialerConfig) (dialURL, error) {
	if cfg.Role != agent.RoleHub && cfg.Role != agent.RoleAgent {
		return dialURL{}, fmt.Errorf("%w：角色非法 %q", ErrDialConfig, cfg.Role)
	}
	if (cfg.Credential == "") == (cfg.PairCode == "") {
		return dialURL{}, fmt.Errorf("%w：凭据与配对码必须且只能给一个", ErrDialConfig)
	}
	if cfg.PairCode != "" && cfg.OnCredential == nil {
		return dialURL{}, fmt.Errorf("%w：配对模式必须提供 OnCredential", ErrDialConfig)
	}
	if cfg.Pin == (certs.SPKI{}) {
		return dialURL{}, fmt.Errorf("%w：缺少监听方证书指纹", ErrDialConfig)
	}
	return parseDialURL(cfg.URL)
}

// parseDialURL 校验 wss://host[:port][/agent]，端口为空时用 DefaultPort，路径为空或 "/" 时补 /agent。
func parseDialURL(raw string) (dialURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return dialURL{}, fmt.Errorf("%w：URL 无法解析", ErrDialConfig)
	}
	if u.Scheme != "wss" {
		return dialURL{}, fmt.Errorf("%w：URL 必须是 wss://，得到 %q", ErrDialConfig, u.Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return dialURL{}, fmt.Errorf("%w：URL 不能带认证信息、查询或片段", ErrDialConfig)
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		return dialURL{}, fmt.Errorf("%w：URL 必须包含主机", ErrDialConfig)
	}
	if port == "" {
		port = DefaultPort
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return dialURL{}, fmt.Errorf("%w：端口必须在 1-65535 之间", ErrDialConfig)
	}
	switch u.Path {
	case "", "/", AgentPath:
	default:
		return dialURL{}, fmt.Errorf("%w：URL 路径只能是 %s", ErrDialConfig, AgentPath)
	}
	return dialURL{hostport: net.JoinHostPort(host, port), host: host, port: port, path: AgentPath}, nil
}

// NormalizeDialURL 按 DialerConfig.URL 的规则校验 raw，返回规范形式 wss://host:port（省略路径，拨号时补 /agent）。
// 输入不含 "://" 时视为裸地址（host、host:port、[ipv6]:port）并先补 wss://；ws://、http:// 等其他 scheme 仍然拒绝。
// 不合法时返回包装 ErrDialConfig 的错误。
func NormalizeDialURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, ":") {
		return "", fmt.Errorf("%w：端口为空", ErrDialConfig)
	}
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}
	u, err := parseDialURL(raw)
	if err != nil {
		return "", err
	}
	return "wss://" + u.hostport, nil
}

func withDialDefaults(cfg DialerConfig) DialerConfig {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return cfg
}

// connectStageError 标记 TCP/代理或 TLS（非钉扎）阶段的失败（含该阶段超时），只有这类失败才走 Fallback。
type connectStageError struct{ err error }

func (e *connectStageError) Error() string { return e.err.Error() }
func (e *connectStageError) Unwrap() error { return e.err }

func isConnectStageError(err error) bool {
	var ce *connectStageError
	return errors.As(err, &ce)
}

// dialFallback 依次用 Fallback 给出的地址（端口沿用原地址）重试；全部失败返回最后一个错误，
// Fallback 自身失败或无地址时返回直拨的错误。
func dialFallback(ctx context.Context, cfg DialerConfig, tgt dialURL, log *slog.Logger, first error) (*websocket.Conn, string, error) {
	addrs, ferr := cfg.Fallback(ctx)
	if ferr != nil || len(addrs) == 0 {
		log.Debug("agent 通道备用解析无结果", "err", ferr)
		return nil, "", first
	}
	err := first
	for _, a := range addrs {
		if ctx.Err() != nil {
			break
		}
		addr := net.JoinHostPort(a.Unmap().WithZone("").String(), tgt.port)
		log.Debug("agent 通道直拨失败，尝试备用地址", "addr", addr)
		var c *websocket.Conn
		var remote string
		c, remote, err = connect(ctx, cfg, tgt, addr)
		if err == nil {
			return c, remote, nil
		}
		if !isConnectStageError(err) {
			// 已连上但升级或钉扎失败：换地址意义不大，按该错误返回。
			return nil, "", err
		}
	}
	return nil, "", err
}

// connect 拨 addr（TCP 经 cfg.Proxy），做钉扎 TLS 与 WebSocket 升级；HTTP 请求的 Host 保持 URL 的主机。
// 返回连接与实际拨的地址。错误：钉扎失败原样包装 *certs.PinMismatchError；TCP/TLS 失败为包着
// connectStageError 的 *DialError；其余（升级失败等）为 *DialError；调用方 ctx 取消时返回 ctx.Err()。
func connect(ctx context.Context, cfg DialerConfig, tgt dialURL, addr string) (*websocket.Conn, string, error) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var (
		mu       sync.Mutex
		pinErr   error
		stageErr error
		// tlsDone 表示 TLS 握手已成功。Transport 的拨号与请求 ctx 解耦：dialTimeout 到期时
		// roundTrip 可能先于拨号回调返回，此时 stageErr 尚未写入，靠 tlsDone 判断失败发生在 TCP/TLS 阶段。
		tlsDone atomic.Bool
	)
	dialTCP := cfg.Proxy.DialContext()
	serverName := ""
	if _, err := netip.ParseAddr(tgt.host); err != nil {
		serverName = tgt.host
	}
	tr := &http.Transport{
		Proxy: nil, // 不读环境变量里的代理
		DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			raw, err := dialTCP(ctx, network, addr)
			if err != nil {
				mu.Lock()
				stageErr = err
				mu.Unlock()
				return nil, err
			}
			tc := tls.Client(raw, &tls.Config{
				ServerName:         serverName,
				InsecureSkipVerify: true, // 标准校验让位给 SPKI 钉扎
				VerifyConnection:   certs.PinVerifier(cfg.Pin),
				NextProtos:         []string{"http/1.1"},
				MinVersion:         tls.VersionTLS12,
			})
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				mu.Lock()
				var pm *certs.PinMismatchError
				if errors.As(err, &pm) {
					pinErr = err
				} else {
					stageErr = err
				}
				mu.Unlock()
				return nil, err
			}
			tlsDone.Store(true)
			return tc, nil
		},
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: maxHeaderBytes,
	}
	// 关闭空闲连接，同时取消 Transport 仍在后台进行的拨号，防止请求先返回后拨号协程泄漏。
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	c, resp, err := websocket.Dial(dctx, "wss://"+tgt.hostport+tgt.path, &websocket.DialOptions{HTTPClient: client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		return c, addr, nil
	}
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	mu.Lock()
	defer mu.Unlock()
	switch {
	case pinErr != nil:
		return nil, "", fmt.Errorf("连接 %s 时证书指纹校验失败：%w", addr, pinErr)
	case stageErr != nil:
		return nil, "", &DialError{Addr: addr, Err: &connectStageError{err: stageErr}}
	case !tlsDone.Load():
		return nil, "", &DialError{Addr: addr, Err: &connectStageError{err: err}}
	}
	return nil, "", &DialError{Addr: addr, Err: err}
}

// dialHandshake 发 auth 或 pair 并等待应答（HandshakeTimeout，走 Clock），成功返回对端信息。
// 配对成功时在返回前调用 OnCredential。
func dialHandshake(ctx context.Context, cfg DialerConfig, fc frameConn, remote string) (Peer, error) {
	fc.SetReadLimit(agent.MaxPreAuthFrame)
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := cfg.Clock.After(HandshakeTimeout)
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

	reply, err := exchangeAuth(hctx, cfg, fc, remote)
	close(stop)
	<-watchDone
	if timedOut.Load() {
		return Peer{}, ErrHandshakeTimeout
	}
	if err != nil {
		return Peer{}, err
	}

	peer := Peer{RemoteAddr: remote, Role: peerRole(cfg.Role)}
	switch m := reply.(type) {
	case *agent.AuthOK:
		if cfg.Credential == "" {
			return Peer{}, fmt.Errorf("%w：配对请求收到了 auth_ok", ErrHandshakeProtocol)
		}
		if m.DeviceID != agent.DeviceID(cfg.Credential) {
			return Peer{}, fmt.Errorf("%w：设备 ID 与凭据不符", ErrHandshakeProtocol)
		}
		peer.DeviceID = m.DeviceID
	case *agent.PairOK:
		if cfg.PairCode == "" {
			return Peer{}, fmt.Errorf("%w：认证请求收到了 pair_ok", ErrHandshakeProtocol)
		}
		if m.DeviceID != agent.DeviceID(m.Credential) {
			return Peer{}, fmt.Errorf("%w：设备 ID 与签发的凭据不符", ErrHandshakeProtocol)
		}
		peer.DeviceID = m.DeviceID
		if cfg.OnCredential != nil {
			if err := cfg.OnCredential(*m); err != nil {
				return Peer{}, fmt.Errorf("保存配对凭据失败：%w", err)
			}
		}
	case *agent.AuthFail:
		// 原因来自对端，会进入错误文本与重连日志，截断防止刷长日志。
		return Peer{}, &AuthFailError{Reason: truncateUTF8(m.Reason, maxPeerReason)}
	default:
		return Peer{}, fmt.Errorf("%w：意外的应答类型 %s", ErrHandshakeProtocol, m.MsgType())
	}
	return peer, nil
}

// exchangeAuth 写出认证或配对帧并读回一帧应答，解码失败按协议错误返回。
func exchangeAuth(ctx context.Context, cfg DialerConfig, fc frameConn, remote string) (agent.Message, error) {
	var first agent.Message
	if cfg.Credential != "" {
		first = &agent.Auth{Credential: cfg.Credential, Role: cfg.Role}
	} else {
		p := cfg.PairInfo
		p.Code = cfg.PairCode
		p.Role = cfg.Role
		first = &p
	}
	b, err := agent.Encode(first)
	if err != nil {
		// 编码错误来自 Validate，可能是 PairInfo 字段不合法；文本不含凭据与配对码。
		return nil, fmt.Errorf("%w：%v", ErrDialConfig, err)
	}
	wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
	err = fc.Write(wctx, b)
	wcancel()
	if err != nil {
		return nil, &DialError{Addr: remote, Err: fmt.Errorf("发送认证帧失败：%w", err)}
	}
	rb, err := fc.Read(ctx)
	if err != nil {
		return nil, &DialError{Addr: remote, Err: fmt.Errorf("读取认证应答失败：%w", err)}
	}
	msg, err := agent.Decode(rb)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrHandshakeProtocol, err)
	}
	return msg, nil
}
