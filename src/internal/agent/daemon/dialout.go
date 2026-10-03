package daemon

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/netutil"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/netaddr"
	"github.com/LanceLRQ/PiMon/src/internal/paircode"
	"github.com/LanceLRQ/PiMon/src/internal/ratelimit"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const (
	// DefaultListen 是拨出模式的缺省监听地址。
	DefaultListen = ":31418"
	// PairWindowTTL 是配对窗口的有效期（走 Clock）。
	PairWindowTTL = 15 * time.Minute
	// MaxListenConns 是拨出监听的总连接上限（含已认证会话）。
	MaxListenConns = 32
	// IdleMaxFailures 与 IdleLockDuration：同一来源握手超时或首帧读取失败 30 次锁 15 分钟。
	// 比认证失败（10 次）宽松，避免 NAT 共享出口的多台机器网络抖动时误锁。
	IdleMaxFailures  = 30
	IdleLockDuration = 15 * time.Minute
	// listenRetry 是监听失败后的重试间隔（走 Clock）。
	listenRetry = 30 * time.Second
	// credentialLen 是凭据（32 字节 base64url 无填充）的长度。
	credentialLen = 43
	// AgentCommonName 是拨出监听证书的 CN。
	AgentCommonName = "PiMon Agent"
)

// PairWindow 是打开的拨出配对窗口，供 pair 命令打印（ctl 协议类型的别名）。Code 只在这里出现一次，不写日志、不进状态。
type PairWindow = ctl.PairWindow

// pairWindow 是内存中的配对窗口：只存配对码哈希。
type pairWindow struct {
	hash    string
	expires time.Time
}

// listenInfo 是拨出监听的实际信息。
type listenInfo struct {
	addr string
	port int
	fp   string
}

// runDialOut 在监听地址上提供 agent 通道（证书首次运行时生成），直到 ctx 结束；返回 true 表示已停止。
// 监听失败按 listenRetry 重试（端口可能被短暂占用），不进入空闲。
func (d *Daemon) runDialOut(ctx context.Context, conf config.Config) bool {
	cert, err := certs.LoadOrCreate(d.cfg.Paths.CertFile, d.cfg.Paths.KeyFile, d.clk.Now(), certs.Options{CommonName: AgentCommonName})
	if err != nil {
		d.setStopped(StateConfigError, "读取或生成证书失败："+err.Error())
		d.log.Error("读取或生成拨出监听证书失败", "err", err)
		return true
	}
	spki, err := certs.SPKIOf(cert)
	if err != nil {
		d.setStopped(StateConfigError, "计算证书指纹失败："+err.Error())
		return true
	}
	addr := d.cfg.Listen
	if addr == "" {
		addr = conf.Listen
	}
	if addr == "" {
		addr = DefaultListen
	}
	if conf.CredentialHash == "" && conf.PrevCredentialHash == "" {
		d.setLastErr(msgUnpaired)
	}
	for ctx.Err() == nil {
		l, err := agentlink.NewListener(agentlink.ListenerConfig{
			Cert: cert, Role: agent.RoleAgent, Auth: outAuth{d}, Clock: d.clk, Log: d.log,
			TimeoutLimiter: ratelimit.NewLimiter(d.clk, IdleMaxFailures, IdleLockDuration),
			Handler:        d.handlerFor, OnSession: d.onSession,
		})
		if err != nil {
			d.setStopped(StateConfigError, "创建监听方失败："+err.Error())
			return true
		}
		raw, err := net.Listen("tcp", addr)
		if err != nil {
			d.setLastErr(fmt.Sprintf("监听 %s 失败：%v（%s 后重试）", addr, err, listenRetry))
			d.log.Warn("拨出监听失败，稍后重试", "addr", addr, "err", err)
			select {
			case <-d.clk.After(listenRetry):
				continue
			case <-ctx.Done():
				return false
			}
		}
		info := &listenInfo{addr: raw.Addr().String(), fp: spki.String()}
		if ta, ok := raw.Addr().(*net.TCPAddr); ok {
			info.port = ta.Port
		}
		d.mu.Lock()
		d.listen = info
		d.mu.Unlock()
		d.log.Info("agent 拨出监听已就绪", "addr", info.addr, "fingerprint", info.fp)
		if d.cfg.OnListen != nil {
			d.cfg.OnListen(info.addr)
		}
		err = l.Serve(ctx, netutil.LimitListener(raw, MaxListenConns))
		d.mu.Lock()
		d.listen = nil
		d.mu.Unlock()
		if err == nil || ctx.Err() != nil {
			return false
		}
		d.setLastErr("拨出监听中断：" + err.Error())
		d.log.Warn("拨出监听中断，稍后重试", "err", err)
		select {
		case <-d.clk.After(listenRetry):
		case <-ctx.Done():
			return false
		}
	}
	return false
}

// OpenPairWindow 在拨出模式下打开（或重开）15 分钟的配对窗口，返回 pair 命令要打印的内容。
// 新窗口替换旧窗口；配对成功一次即关闭。
func (d *Daemon) OpenPairWindow(ctx context.Context) (PairWindow, error) {
	if err := ctx.Err(); err != nil {
		return PairWindow{}, err
	}
	code, err := paircode.New()
	if err != nil {
		return PairWindow{}, err
	}
	d.mu.Lock()
	if d.conf.Mode == config.ModeDialIn {
		d.mu.Unlock()
		return PairWindow{}, fmt.Errorf("%w：拨入模式请用 install --code 重新配对", ErrNotDialOut)
	}
	if d.conf.Mode != config.ModeDialOut || d.listen == nil { // 刚启动尚未读配置，或监听未就绪
		d.mu.Unlock()
		return PairWindow{}, ErrNotListening
	}
	w := PairWindow{Code: code, Fingerprint: d.listen.fp, Port: d.listen.port, ExpiresAt: d.clk.Now().Add(PairWindowTTL)}
	d.window = &pairWindow{hash: paircode.Hash(code), expires: w.ExpiresAt}
	d.mu.Unlock()
	w.IPs = []string{}
	for _, ip := range netaddr.LANIPv4() {
		w.IPs = append(w.IPs, ip.String())
	}
	d.log.Info("已打开拨出配对窗口", "window", w)
	return w, nil
}

// outAuth 是拨出监听方的 Authenticator：凭据与当前、上一个凭据哈希比对；配对只在窗口内接受。
type outAuth struct{ d *Daemon }

// Authenticate 实现 agentlink.Authenticator：CredentialHash 与 PrevCredentialHash 都接受（P10）。
func (a outAuth) Authenticate(ctx context.Context, m agent.Auth) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(m.Credential) != credentialLen {
		return "", &agentlink.AuthError{Reason: agent.ReasonBadCredential}
	}
	h := []byte(agent.CredentialHash(m.Credential))
	a.d.mu.Lock()
	cur, prev := a.d.conf.CredentialHash, a.d.conf.PrevCredentialHash
	a.d.mu.Unlock()
	ok := 0
	for _, x := range []string{cur, prev} {
		if x != "" {
			ok |= subtle.ConstantTimeCompare(h, []byte(x))
		}
	}
	if ok != 1 {
		return "", &agentlink.AuthError{Reason: agent.ReasonBadCredential}
	}
	return agent.DeviceID(m.Credential), nil
}

// Pair 实现 agentlink.Authenticator：没有窗口 bad_code，窗口已过期 code_expired，配对码不符 bad_code；
// 匹配时消耗窗口、签发凭据，把旧哈希移到 PrevCredentialHash、新哈希写入 CredentialHash，原子落盘后才返回（P10）。
func (a outAuth) Pair(ctx context.Context, m agent.Pair) (agent.PairOK, error) {
	d := a.d
	d.mu.Lock()
	w := d.window
	switch {
	case w == nil:
		d.mu.Unlock()
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonBadCode}
	case !d.clk.Now().Before(w.expires):
		d.window = nil
		d.mu.Unlock()
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonCodeExpired}
	case subtle.ConstantTimeCompare([]byte(paircode.Hash(m.Code)), []byte(w.hash)) != 1:
		d.mu.Unlock()
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonBadCode}
	}
	d.window = nil // 一次性：并发的第二个配对看到的是没有窗口
	d.mu.Unlock()
	restore := func() {
		d.mu.Lock()
		if d.window == nil {
			d.window = w
		}
		d.mu.Unlock()
	}

	if err := ctx.Err(); err != nil {
		restore()
		return agent.PairOK{}, err
	}
	cred, err := agent.NewCredential()
	if err != nil {
		restore()
		return agent.PairOK{}, err
	}
	h := agent.CredentialHash(cred)
	notDialOut := false
	err = d.updateConfig(func(c *config.Config) bool {
		if c.Mode != config.ModeDialOut { // 配置已被改成拨入（如重新 install），不得写入哈希
			notDialOut = true
			return false
		}
		c.PrevCredentialHash, c.CredentialHash = c.CredentialHash, h
		return true
	})
	if err == nil && notDialOut {
		err = fmt.Errorf("配置已不是拨出模式，拒绝配对：%w", ErrNotDialOut)
	}
	if err != nil {
		restore()
		d.log.Error("拨出配对：保存凭据哈希失败", "err", err)
		return agent.PairOK{}, err
	}
	id := agent.DeviceID(cred)
	d.log.Info("拨出配对成功，等待 hub 的 hello_ok 确认", "device_id", id,
		"hub_hostname", link.Clip(m.Hostname, 64))
	return agent.PairOK{
		Credential: cred, DeviceID: id,
		ListenerInfo: agent.ListenerInfo{Hostname: link.Clip(d.hostname(), link.MaxNameBytes), Version: version.Version},
	}, nil
}

// dialOutRevoked 处理拨出方向对端的 bye{revoked}：清除该 hub 对应的哈希（当前哈希被吊销时两个一起清空），
// 之后只能在本机重新 pair。
func (d *Daemon) dialOutRevoked(deviceID string) {
	cleared := false
	err := d.updateConfig(func(c *config.Config) bool {
		if c.Mode != config.ModeDialOut {
			return false
		}
		switch {
		case c.CredentialHash != "" && agent.DeviceIDFromHash(c.CredentialHash) == deviceID:
			c.CredentialHash, c.PrevCredentialHash = "", ""
			cleared = true
		case c.PrevCredentialHash != "" && agent.DeviceIDFromHash(c.PrevCredentialHash) == deviceID:
			c.PrevCredentialHash = ""
		default:
			return false
		}
		return true
	})
	if err != nil {
		d.log.Error("清除已吊销 hub 的凭据哈希失败", "err", err)
	}
	if cleared {
		d.setStopped(StateRevoked, msgRevokedDialOut)
		d.log.Warn("agent 已被 hub 吊销，已清除凭据哈希，需要在本机重新 pair", "device_id", deviceID)
	}
}

// clearPrevHash 在用当前凭据认证的会话收到 hello_ok 后清空上一个哈希（只认最新配对的 hub）。
func (d *Daemon) clearPrevHash(curHash string) {
	changed := false
	err := d.updateConfig(func(c *config.Config) bool {
		if c.CredentialHash != curHash || c.PrevCredentialHash == "" {
			return false
		}
		c.PrevCredentialHash, changed = "", true
		return true
	})
	switch {
	case err != nil:
		d.log.Error("清除上一个 hub 的凭据哈希失败", "err", err)
	case changed:
		d.log.Info("新配对的 hub 已确认，旧 hub 的凭据已失效")
	}
}

// hostname 返回本机主机名（读取失败为空）。
func (d *Daemon) hostname() string {
	h, err := d.cfg.Hostname()
	if err != nil {
		return ""
	}
	return h
}
