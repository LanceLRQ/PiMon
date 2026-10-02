// Package agentlinktest 提供 agent 通道的测试辅助：临时证书、在本机回环上运行的真实监听方、
// 固定凭据与配对码的 Authenticator，以及可"拔线"的 TCP 中继。仅用于测试。
package agentlinktest

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// NewCert 在 t.TempDir() 下生成自签名证书（certs.LoadOrCreate），返回证书与其 SPKI 指纹。
func NewCert(t testing.TB) (tls.Certificate, certs.SPKI) {
	t.Helper()
	dir := t.TempDir()
	cert, err := certs.LoadOrCreate(filepath.Join(dir, "test.crt"), filepath.Join(dir, "test.key"), time.Now(),
		certs.Options{CommonName: "PiMon Test"})
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}
	pin, err := certs.SPKIOf(cert)
	if err != nil {
		t.Fatalf("计算测试证书指纹失败: %v", err)
	}
	return cert, pin
}

// QuietLog 返回丢弃所有输出的日志器，避免测试输出被连接层日志刷屏。
func QuietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// StaticAuth 是固定凭据与配对码的 agentlink.Authenticator，与真实实现一样只存凭据哈希、
// 设备 ID 用 agent.DeviceID 推导（与拨号方的设备 ID 校验一致）。并发安全。
//
// 由输入决定的拒绝一律返回 *agentlink.AuthError；ctx 已取消时返回 ctx.Err()。
type StaticAuth struct {
	mu     sync.Mutex
	hashes map[string]struct{} // 凭据哈希
	codes  map[string]struct{} // 未使用的配对码
	calls  atomic.Int64
}

// NewStaticAuth 创建空的 StaticAuth，用 AddCredential、AddPairCode 登记。
func NewStaticAuth() *StaticAuth {
	return &StaticAuth{hashes: make(map[string]struct{}), codes: make(map[string]struct{})}
}

// AddCredential 登记一个有效凭据，返回其设备 ID。
func (a *StaticAuth) AddCredential(credential string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hashes[agent.CredentialHash(credential)] = struct{}{}
	return agent.DeviceID(credential)
}

// RevokeCredential 删除一个凭据，之后用它认证得到 bad_credential。
func (a *StaticAuth) RevokeCredential(credential string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.hashes, agent.CredentialHash(credential))
}

// AddPairCode 登记一个一次性配对码。
func (a *StaticAuth) AddPairCode(code string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codes[code] = struct{}{}
}

// Calls 返回 Authenticate 与 Pair 被调用的总次数，用于断言监听方是否收到过认证帧。
func (a *StaticAuth) Calls() int64 { return a.calls.Load() }

// Authenticate 实现 agentlink.Authenticator：凭据已登记返回设备 ID，否则 bad_credential。
func (a *StaticAuth) Authenticate(ctx context.Context, msg agent.Auth) (string, error) {
	a.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	_, ok := a.hashes[agent.CredentialHash(msg.Credential)]
	a.mu.Unlock()
	if !ok {
		return "", &agentlink.AuthError{Reason: agent.ReasonBadCredential}
	}
	return agent.DeviceID(msg.Credential), nil
}

// Pair 实现 agentlink.Authenticator：配对码已登记时消耗它、签发并登记新凭据，否则 bad_code。
func (a *StaticAuth) Pair(ctx context.Context, msg agent.Pair) (agent.PairOK, error) {
	a.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return agent.PairOK{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.codes[msg.Code]; !ok {
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonBadCode}
	}
	cred, err := agent.NewCredential()
	if err != nil {
		return agent.PairOK{}, err
	}
	delete(a.codes, msg.Code)
	a.hashes[agent.CredentialHash(cred)] = struct{}{}
	return agent.PairOK{
		Credential:   cred,
		DeviceID:     agent.DeviceID(cred),
		ListenerInfo: agent.ListenerInfo{Hostname: "agentlinktest", Version: "test"},
	}, nil
}

// Server 是 StartListener 启动的监听方。
type Server struct {
	// URL 形如 wss://127.0.0.1:端口/agent，可直接作为 DialerConfig.URL。
	URL string
	// Addr 是监听地址 host:port。
	Addr string
	// Pin 是监听方证书的 SPKI 指纹，作为 DialerConfig.Pin。
	Pin certs.SPKI
	// Auth 是实际使用的 Authenticator（cfg.Auth 为空时为新建的 *StaticAuth）。
	Auth agentlink.Authenticator

	once   sync.Once
	cancel context.CancelFunc
	errc   chan error
	err    error
}

// StartListener 在 127.0.0.1 的随机端口上按 cfg 启动监听方并返回。cfg 的空字段取测试默认值：
// Cert 用 NewCert，Role 用 agent.RoleHub，Auth 用 NewStaticAuth()，Log 用 QuietLog()。
// t.Cleanup 时自动 Stop，Serve 返回错误则报测试失败。
func StartListener(t testing.TB, cfg agentlink.ListenerConfig) *Server {
	t.Helper()
	var pin certs.SPKI
	if len(cfg.Cert.Certificate) == 0 {
		cfg.Cert, pin = NewCert(t)
	} else {
		var err error
		if pin, err = certs.SPKIOf(cfg.Cert); err != nil {
			t.Fatalf("计算监听方证书指纹失败: %v", err)
		}
	}
	if cfg.Role == "" {
		cfg.Role = agent.RoleHub
	}
	if cfg.Auth == nil {
		cfg.Auth = NewStaticAuth()
	}
	if cfg.Log == nil {
		cfg.Log = QuietLog()
	}
	l, err := agentlink.NewListener(cfg)
	if err != nil {
		t.Fatalf("创建监听方失败: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		URL:    "wss://" + ln.Addr().String() + agentlink.AgentPath,
		Addr:   ln.Addr().String(),
		Pin:    pin,
		Auth:   cfg.Auth,
		cancel: cancel,
		errc:   make(chan error, 1),
	}
	go func() { s.errc <- l.Serve(ctx, ln) }()
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Errorf("监听方 Serve 返回错误: %v", err)
		}
	})
	return s
}

// Stop 关停监听方（已建立的会话收到 bye{shutdown}）并等待 Serve 返回，返回 Serve 的错误。可重复调用。
func (s *Server) Stop() error {
	s.once.Do(func() {
		s.cancel()
		s.err = <-s.errc
	})
	return s.err
}

// Relay 是本机 TCP 中继：把每个入站连接原样转发到目标地址。Freeze 让现有连接变成"黑洞"
// （双向数据静默丢弃、连接不断开），用来模拟对端失去响应、触发心跳超时；之后的新连接照常转发。
type Relay struct {
	// Addr 是中继监听地址 host:port，拨号方连它代替目标。
	Addr string

	target string
	ln     net.Listener
	mu     sync.Mutex
	closed bool
	pipes  map[*relayPipe]struct{}
	wg     sync.WaitGroup
}

type relayPipe struct {
	a, b   net.Conn
	frozen atomic.Bool
}

// NewRelay 在 127.0.0.1 的随机端口上启动到 target 的中继，t.Cleanup 时关闭监听与所有连接并等待协程退出。
func NewRelay(t testing.TB, target string) *Relay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("中继监听失败: %v", err)
	}
	r := &Relay{Addr: ln.Addr().String(), target: target, ln: ln, pipes: make(map[*relayPipe]struct{})}
	r.wg.Add(1)
	go r.accept()
	t.Cleanup(r.close)
	return r
}

// Freeze 让当前所有连接进入黑洞状态，返回受影响的连接数。
func (r *Relay) Freeze() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for p := range r.pipes {
		p.frozen.Store(true)
	}
	return len(r.pipes)
}

func (r *Relay) accept() {
	defer r.wg.Done()
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", r.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p := &relayPipe{a: c, b: up}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			_ = c.Close()
			_ = up.Close()
			return
		}
		r.pipes[p] = struct{}{}
		r.mu.Unlock()
		r.wg.Add(2)
		go r.copy(p, up, c)
		go r.copy(p, c, up)
	}
}

// copy 把 src 的数据写到 dst，冻结后丢弃；任一方向结束即关闭两端。
func (r *Relay) copy(p *relayPipe, dst, src net.Conn) {
	defer r.wg.Done()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.frozen.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				err = werr
			}
		}
		if err != nil {
			break
		}
	}
	_ = p.a.Close()
	_ = p.b.Close()
	r.mu.Lock()
	delete(r.pipes, p)
	r.mu.Unlock()
}

func (r *Relay) close() {
	_ = r.ln.Close()
	r.mu.Lock()
	r.closed = true
	for p := range r.pipes {
		_ = p.a.Close()
		_ = p.b.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// 编译期确认 StaticAuth 满足接口。
var _ agentlink.Authenticator = (*StaticAuth)(nil)
