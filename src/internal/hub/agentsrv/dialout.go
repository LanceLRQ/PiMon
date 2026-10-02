package agentsrv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// PairTimeout 是一次拨出配对（拨号、配对、等待 hello 结果）的总时限，用真实时间。
const PairTimeout = 20 * time.Second

// pairing.failed 的 details.reason 取值（另有 agent.ReasonBadCode、ReasonCodeExpired、ReasonRateLimited 原样使用）。
const (
	ReasonUnreachable  = "unreachable"
	ReasonPinMismatch  = "pin_mismatch"
	ReasonIncompatible = "incompatible"
	ReasonTimeout      = "timeout"
	ReasonUnknown      = "unknown"
)

// 拨出设备 ConnError 的取值：除下面两个外，拨号失败时取 pairing.failed 同一套原因
// （unreachable、pin_mismatch、timeout、rate_limited、unknown），连上后清空。
const (
	// ConnErrProxyRemoved 表示设备配置的代理已被删除：不拨号，也不退回直连。
	ConnErrProxyRemoved = "proxy_removed"
	// ConnErrBadCredential 表示对端不再认可凭据（多半是 VPS 本地重新 pair 过），需重新配对；循环已停止。
	ConnErrBadCredential = agent.ReasonBadCredential
	// ConnErrRevoked 表示对端吊销了本 hub，需重新配对；循环已停止。
	ConnErrRevoked = agent.ReasonRevoked
)

// ErrDialOutClosed 表示拨出管理器已关闭。
var ErrDialOutClosed = errors.New("拨出管理器已关闭")

// maxPairCodeInput 与 maxNameRunes 是拨出配对请求的长度上限。
const (
	maxPairCodeInput = 64
	maxNameRunes     = 64
)

// PairingError 是拨出配对失败（API 返回 502 pairing.failed）。Reason 只取本包 Reason* 与
// agent.ReasonBadCode/ReasonCodeExpired/ReasonRateLimited；凭据已保存时 DeviceID 非空（设备保留）；
// 不兼容时另带 RequiredMin 与 HubVersion。
type PairingError struct {
	Reason      string
	DeviceID    string
	RequiredMin int
	HubVersion  string
	// Err 是底层错误，只用于日志，不对外展示。
	Err error
}

func (e *PairingError) Error() string { return "拨出配对失败：" + e.Reason }

func (e *PairingError) Unwrap() error { return e.Err }

// pairingReason 把拨出配对的错误映射为 pairing.failed 的原因码（按顺序判断）；
// 对端给的 auth_fail 原因按白名单取值，其余一律 unknown。
func pairingReason(err error) string {
	var pin *certs.PinMismatchError
	var af *agentlink.AuthFailError
	var de *agentlink.DialError
	var inc *IncompatibleError
	switch {
	case errors.As(err, &pin):
		return ReasonPinMismatch
	case errors.As(err, &af):
		switch af.Reason {
		case agent.ReasonBadCode, agent.ReasonCodeExpired, agent.ReasonRateLimited:
			return af.Reason
		}
		return ReasonUnknown
	case errors.Is(err, agentlink.ErrHandshakeTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrHelloTimeout):
		return ReasonTimeout
	case errors.As(err, &de):
		return ReasonUnreachable
	case errors.As(err, &inc):
		return ReasonIncompatible
	}
	return ReasonUnknown
}

// ProxyResolver 按代理 id 取解析好的代理（proxies.Store 实现）；代理不存在返回 proxies.ErrNotFound。
type ProxyResolver interface {
	Resolve(ctx context.Context, id string) (*proxy.Proxy, error)
}

// DialOutConfig 是拨出管理器的依赖。
type DialOutConfig struct {
	Server  *Server
	Proxies ProxyResolver
	// Clock 用于重连退避、握手应答超时与拨出会话心跳，默认取 Server 的 Clock。
	Clock clock.Clock
	// Log 默认取 Server 的 Log。
	Log *slog.Logger
}

// dialKey 是决定是否需要重启循环的拨号参数；凭据与代理认证只在内存中比较，不写日志。
type dialKey struct {
	url, pin, proxy, credential string
}

// dialLoop 是一台设备当前的连接方：重连循环（rc），或配对成功后接管的配对会话（sess，rc 为空）。
// 配对会话结束或参数变化后由重连循环接替。
type dialLoop struct {
	key  dialKey
	rc   *agentlink.Reconnector
	sess *agentlink.Session
}

// stop 停止连接方并等它结束：重连循环 Stop（会话以 bye{shutdown} 关闭）；配对会话直接以 bye{shutdown} 关闭。
func (l *dialLoop) stop() {
	if l.rc != nil {
		l.rc.Stop()
		return
	}
	l.sess.Close(agent.ReasonShutdown)
	<-l.sess.Done()
}

// stale 表示接管的配对会话已结束，需要由重连循环接替。
func (l *dialLoop) stale() bool { return l.rc == nil && isDone(l.sess) }

// DialOut 是拨出管理器：拨出配对（一次性 Dial + pair），以及为每台拨出设备运行一个凭据模式的重连循环
// （钉扎 SPKI、经设备配置的代理），连上后交给 Server 走与拨入相同的会话处理。
//
// 设备的地址、代理或凭据变化时先停旧循环、再起新循环（在独立协程里依次进行，不在持锁时等待）；
// 吊销时停止；代理被删除时停止并记 proxy_removed；循环因凭据失效或被对端吊销而停止时记下原因，
// 配置不变则不再自动重启。ConnError 只在内存中。
//
// 锁的约定：ops 串行化「读库并调整循环」的过程（reconcile），mu 保护内存表；
// 持有任何锁时都不等待循环退出。
type DialOut struct {
	srv         *Server
	prox        ProxyResolver
	clk         clock.Clock
	log         *slog.Logger
	pairTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup

	ops sync.Mutex

	mu      sync.Mutex
	started bool
	closed  bool
	runCtx  context.Context
	loops   map[string]*dialLoop
	// tails 是每台设备最近一个循环协程的退出信号：新循环先等它退出，保证同一设备不会同时有两个循环在拨号。
	tails   map[string]chan struct{}
	pairing map[string]bool // 配对会话仍在进行，循环暂不接手
	connErr map[string]string
}

// NewDialOut 创建拨出管理器并订阅设备仓库的变更；调用 Start 后才开始拨号，用 Close 停止。
func NewDialOut(cfg DialOutConfig) (*DialOut, error) {
	if cfg.Server == nil || cfg.Proxies == nil {
		return nil, errors.New("拨出管理器缺少 agent 通道服务或代理仓库")
	}
	if cfg.Clock == nil {
		cfg.Clock = cfg.Server.clk
	}
	if cfg.Log == nil {
		cfg.Log = cfg.Server.log
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &DialOut{
		srv: cfg.Server, prox: cfg.Proxies, clk: cfg.Clock, log: cfg.Log, pairTimeout: PairTimeout,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		loops:   make(map[string]*dialLoop),
		tails:   make(map[string]chan struct{}),
		pairing: make(map[string]bool),
		connErr: make(map[string]string),
	}
	d.srv.devs.OnChange(d.onDeviceChange)
	return d, nil
}

// Start 为每台拨出设备启动重连循环；循环随 ctx 结束而退出。只有第一次调用生效。
func (d *DialOut) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrDialOutClosed
	}
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started, d.runCtx = true, ctx
	d.mu.Unlock()
	return d.reconcileAll()
}

// Refresh 在后台按当前设备与代理配置重新核对全部拨出设备（代理变化时由 app 调用），不阻塞。
func (d *DialOut) Refresh() {
	d.spawn(func() {
		if err := d.reconcileAll(); err != nil && d.ctx.Err() == nil {
			d.log.Warn("刷新拨出设备失败", "err", err)
		}
	})
}

// Remove 在后台停止设备的重连循环并清除其连接错误（吊销时调用），不阻塞。
func (d *DialOut) Remove(id string) {
	d.spawn(func() {
		d.ops.Lock()
		defer d.ops.Unlock()
		d.replace(id, nil)
		d.mu.Lock()
		delete(d.connErr, id)
		d.mu.Unlock()
	})
}

// ConnError 返回拨出设备最近的连接错误（见 ConnErr* 与拨号失败原因），没有时为空串。
func (d *DialOut) ConnError(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connErr[id]
}

// Close 停止全部重连循环（当前会话以 bye{shutdown} 关闭）、中止进行中的配对，并等待后台协程退出。
// 须在 Server.Close 之前调用；可重复调用。
func (d *DialOut) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.wg.Wait()
		return
	}
	d.closed = true
	close(d.done)
	loops := make([]*dialLoop, 0, len(d.loops))
	for _, l := range d.loops {
		loops = append(loops, l)
	}
	clear(d.loops)
	d.mu.Unlock()
	d.cancel()
	for _, l := range loops {
		l.stop()
	}
	d.wg.Wait()
}

// spawn 在未关闭时登记并启动后台协程，已关闭返回假。
func (d *DialOut) spawn(fn func()) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		fn()
	}()
	return true
}

// onDeviceChange 是设备仓库的变更回调，可能在调用方持锁时同步触发，只起协程。
func (d *DialOut) onDeviceChange(id string, removed bool) {
	if removed {
		d.Remove(id)
		return
	}
	d.spawn(func() { d.reconcile(id) })
}

// reconcileAll 核对全部设备：拨出设备按配置调整循环，已不存在的设备停掉残留循环。
func (d *DialOut) reconcileAll() error {
	list, err := d.srv.devs.List(d.ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(list))
	for _, dev := range list {
		if dev.Direction == model.DeviceDialOut {
			seen[dev.ID] = true
			d.reconcile(dev.ID)
		}
	}
	d.mu.Lock()
	var stale []string
	for id := range d.loops {
		if !seen[id] {
			stale = append(stale, id)
		}
	}
	d.mu.Unlock()
	for _, id := range stale {
		d.reconcile(id)
	}
	return nil
}

// dialTarget 是按设备行解析出的拨号参数。
type dialTarget struct {
	key   dialKey
	pin   certs.SPKI
	proxy *proxy.Proxy
}

// errNotDialOut 表示设备不存在或不是拨出设备。
var errNotDialOut = errors.New("不是拨出设备")

// target 读取设备行、凭据与代理，得到拨号参数。设备不存在或不是拨出设备返回 errNotDialOut；
// 代理已删除返回 proxies.ErrNotFound。
func (d *DialOut) target(id string) (dialTarget, error) {
	ctx := d.ctx
	dev, err := d.srv.devs.Get(ctx, id)
	if errors.Is(err, devices.ErrNotFound) || (err == nil && dev.Direction != model.DeviceDialOut) {
		return dialTarget{}, errNotDialOut
	}
	if err != nil {
		return dialTarget{}, err
	}
	det, err := d.srv.devs.Detail(ctx, id)
	if err != nil {
		return dialTarget{}, err
	}
	pin, err := certs.ParseSPKI(det.Pin)
	if err != nil {
		return dialTarget{}, fmt.Errorf("设备保存的指纹不合法: %w", err)
	}
	cred, err := d.srv.devs.Credential(ctx, id)
	if errors.Is(err, devices.ErrNotFound) {
		return dialTarget{}, errNotDialOut
	}
	if err != nil {
		return dialTarget{}, err
	}
	p, err := d.prox.Resolve(ctx, dev.ProxyID)
	if err != nil {
		return dialTarget{}, err
	}
	key := dialKey{url: dev.Address, pin: pin.String(), credential: cred}
	if !p.IsDirect() {
		key.proxy = p.URL()
	}
	return dialTarget{key: key, pin: pin, proxy: p}, nil
}

// reconcile 按设备当前配置调整它的重连循环：未启动、已关闭或配对握手仍在进行时不动；
// 设备已不存在则停止；代理已删除则停止并记 proxy_removed；参数未变则保持现状
// （含因凭据失效已停止的循环与仍在进行的配对会话）；参数变化或配对会话已结束则以新循环替换。
func (d *DialOut) reconcile(id string) {
	d.ops.Lock()
	defer d.ops.Unlock()
	d.mu.Lock()
	skip := !d.started || d.closed || d.pairing[id]
	d.mu.Unlock()
	if skip {
		return
	}
	t, err := d.target(id)
	switch {
	case errors.Is(err, errNotDialOut):
		d.replace(id, nil)
		d.mu.Lock()
		delete(d.connErr, id)
		d.mu.Unlock()
		return
	case errors.Is(err, proxies.ErrNotFound):
		d.replace(id, nil)
		d.mu.Lock()
		d.connErr[id] = ConnErrProxyRemoved
		d.mu.Unlock()
		d.log.Warn("拨出设备的代理已被删除，停止拨号（不退回直连）", "device_id", id)
		return
	case err != nil:
		if d.ctx.Err() == nil {
			d.log.Warn("读取拨出设备配置失败，保持现状", "device_id", id, "err", err)
		}
		return
	}
	d.mu.Lock()
	cur := d.loops[id]
	d.mu.Unlock()
	if cur != nil && cur.key == t.key && !cur.stale() {
		return
	}
	d.mu.Lock()
	delete(d.connErr, id)
	d.mu.Unlock()
	d.replace(id, d.newLoop(id, t))
}

// newLoop 构造设备的重连循环（尚未运行）。
func (d *DialOut) newLoop(id string, t dialTarget) *dialLoop {
	l := &dialLoop{key: t.key}
	log := d.log.With("device_id", id)
	l.rc = agentlink.NewReconnector(agentlink.ReconnectorConfig{
		Dialer: agentlink.DialerConfig{
			URL: t.key.url, Pin: t.pin, Role: agent.RoleHub, Credential: t.key.credential, Proxy: t.proxy,
			Clock: d.clk, Log: log, Handler: d.srv.HandlerFor,
		},
		Policy: func(err error, reason *agentlink.CloseReason) agentlink.Decision {
			if err != nil {
				d.setConnErr(id, l, pairingReason(err))
			}
			return agentlink.DefaultPolicy(err, reason)
		},
		// OnSession 须快速返回：Attach 只登记计时与善后协程。
		OnSession: func(s *agentlink.Session) {
			d.setConnErr(id, l, "")
			d.srv.Attach(s)
		},
		Clock: d.clk,
		Log:   log,
	})
	return l
}

// setConnErr 在 l 仍是设备当前循环时记录连接错误（空串表示清除）。
func (d *DialOut) setConnErr(id string, l *dialLoop, v string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loops[id] != l {
		return
	}
	if v == "" {
		delete(d.connErr, id)
	} else {
		d.connErr[id] = v
	}
}

// replace 把设备的循环换成 next（nil 表示只停止），并在后台协程里依次：停止旧循环、等前一个循环协程退出、
// 运行 next。调用方须持有 ops；本方法不等待任何循环退出。已关闭时不做任何事（Close 会停掉全部循环）。
func (d *DialOut) replace(id string, next *dialLoop) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	prev := d.loops[id]
	if next == nil {
		if prev == nil {
			d.mu.Unlock()
			return
		}
		delete(d.loops, id)
	} else {
		d.loops[id] = next
	}
	prevTail := d.tails[id]
	tail := make(chan struct{})
	d.tails[id] = tail
	runCtx := d.runCtx
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			if d.tails[id] == tail {
				delete(d.tails, id)
			}
			d.mu.Unlock()
			close(tail)
		}()
		if prev != nil {
			prev.stop()
		}
		if prevTail != nil {
			<-prevTail
		}
		if next == nil {
			return
		}
		err := next.rc.Run(runCtx)
		var se *agentlink.StopError
		if errors.As(err, &se) {
			reason := stopReason(se)
			d.setConnErr(id, next, reason)
			d.log.Warn("拨出设备的重连已停止，需在 VPS 上重新配对后再在中枢重新配对", "device_id", id, "reason", reason)
		}
	}()
}

// stopReason 把重连循环的停止原因映射为 ConnError。
func stopReason(se *agentlink.StopError) string {
	var af *agentlink.AuthFailError
	if errors.As(se.Err, &af) {
		switch af.Reason {
		case agent.ReasonBadCredential:
			return ConnErrBadCredential
		case agent.ReasonRevoked:
			return ConnErrRevoked
		}
	}
	if se.Close != nil && se.Close.Bye == agent.ReasonRevoked {
		return ConnErrRevoked
	}
	return ReasonUnknown
}

// dialOutInput 是校验并规范化后的拨出配对请求。
type dialOutInput struct {
	address, code, name, role, proxyID string
	pin                                certs.SPKI
	allowOffline                       *bool
	autoHostMetrics                    bool
}

// validateDialOut 校验拨出配对请求，失败返回 model.FieldErrors（代理是否存在另查）。
func validateDialOut(req model.DialOutRequest) (dialOutInput, error) {
	in := dialOutInput{
		code: strings.TrimSpace(req.Code), name: strings.TrimSpace(req.Name), role: strings.TrimSpace(req.Role),
		proxyID: strings.TrimSpace(req.ProxyID), allowOffline: req.AllowOffline,
		autoHostMetrics: req.AutoHostMetrics == nil || *req.AutoHostMetrics,
	}
	fe := model.FieldErrors{}
	if addr := strings.TrimSpace(req.Address); addr == "" {
		fe["address"] = model.FieldRequired
	} else if u, err := agentlink.NormalizeDialURL(addr); err != nil {
		fe["address"] = model.FieldInvalid
	} else {
		in.address = u
	}
	if fp := strings.TrimSpace(req.Fingerprint); fp == "" {
		fe["fingerprint"] = model.FieldRequired
	} else if pin, err := certs.ParseSPKI(fp); err != nil {
		fe["fingerprint"] = model.FieldInvalid
	} else {
		in.pin = pin
	}
	switch {
	case in.code == "":
		fe["code"] = model.FieldRequired
	case len(in.code) > maxPairCodeInput:
		fe["code"] = model.FieldOutOfRange
	}
	if utf8.RuneCountInString(in.name) > maxNameRunes {
		fe["name"] = model.FieldOutOfRange
	}
	if in.role == "" {
		in.role = model.DeviceRoleVPS
	}
	switch in.role {
	case model.DeviceRoleServer, model.DeviceRoleNAS, model.DeviceRoleDev, model.DeviceRoleVPS:
	default:
		fe["role"] = model.FieldInvalid
	}
	if len(fe) > 0 {
		return dialOutInput{}, fe
	}
	return in, nil
}

// Pair 执行拨出配对：用请求里的地址、指纹、配对码与代理拨号并发送 pair；拿到凭据时立即保存设备
// （凭据加密、SPKI、未确认），再把会话交给 Server 等待 hello 结果，总时限 PairTimeout。
//
// 成功返回已上线的设备；请求不合法返回 model.FieldErrors；配对失败返回 *PairingError
// （凭据已保存时设备保留，DeviceID 非空：不兼容的 agent 升级后可自动接入）；已关闭返回 ErrDialOutClosed。
// 配对会话结束后由重连循环用凭据接手。
func (d *DialOut) Pair(ctx context.Context, req model.DialOutRequest) (model.Device, error) {
	in, err := validateDialOut(req)
	if err != nil {
		return model.Device{}, err
	}
	p, err := d.prox.Resolve(ctx, in.proxyID)
	if errors.Is(err, proxies.ErrNotFound) {
		return model.Device{}, model.FieldErrors{"proxy_id": model.FieldInvalid}
	}
	if err != nil {
		return model.Device{}, err
	}
	if !d.enter() {
		return model.Device{}, ErrDialOutClosed
	}
	defer d.wg.Done()

	ctx, cancel := context.WithTimeout(ctx, d.pairTimeout)
	defer cancel()
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()

	log := d.log.With("address", in.address)
	key := dialKey{url: in.address, pin: in.pin.String()}
	if !p.IsDirect() {
		key.proxy = p.URL()
	}
	sess, err := agentlink.Dial(ctx, agentlink.DialerConfig{
		URL: in.address, Pin: in.pin, Role: agent.RoleHub, PairCode: in.code,
		PairInfo: agent.Pair{Name: "pimon-hub", Hostname: d.srv.hostname(), OS: runtime.GOOS, Arch: runtime.GOARCH},
		Proxy:    p, Clock: d.clk, Log: log, Handler: d.srv.HandlerFor,
		OnCredential: func(ok agent.PairOK) error {
			if err := d.saveDialOut(ctx, in, ok); err != nil {
				return err
			}
			key.credential = ok.Credential
			return nil
		},
	})
	if err != nil {
		if key.credential != "" {
			// 凭据已保存（Dial 在交出凭据后不应再失败，这里兜底）：设备保留，交给重连循环。
			id := agent.DeviceID(key.credential)
			d.release(id)
			return model.Device{}, d.pairFailure(log, err, id)
		}
		return model.Device{}, d.pairFailure(log, err, "")
	}
	id := sess.Peer().DeviceID
	log = log.With("device_id", id)
	if err := d.srv.AttachReady(ctx, sess); err != nil {
		var inc *IncompatibleError
		if !errors.As(err, &inc) {
			// 超时等情况下结束配对会话（不兼容时 Server 已断开），由重连循环接手（设备已保存）。
			sess.Close("")
		}
		d.release(id)
		return model.Device{}, d.pairFailure(log, err, id)
	}
	d.adopt(id, &dialLoop{key: key, sess: sess})
	dev, err := d.srv.devs.Get(d.ctx, id)
	if err != nil {
		return model.Device{}, err
	}
	dev.Presence, _ = d.srv.pres.State(id)
	dev.ConnError = d.ConnError(id)
	log.Info("拨出配对成功")
	return dev, nil
}

// enter 在未关闭时登记一个进行中的操作（调用方负责 wg.Done），已关闭返回假。
func (d *DialOut) enter() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	d.wg.Add(1)
	return true
}

// saveDialOut 在配对成功（OnCredential）时保存拨出设备。先标记配对进行中，避免设备变更回调让重连循环抢先接手。
func (d *DialOut) saveDialOut(ctx context.Context, in dialOutInput, ok agent.PairOK) error {
	d.mu.Lock()
	d.pairing[ok.DeviceID] = true
	d.mu.Unlock()
	name := in.name
	if name == "" {
		name = ok.ListenerInfo.Hostname
	}
	_, err := d.srv.devs.CreateDialOut(ctx, devices.DialOutDevice{
		Name: name, Role: in.role, Address: in.address, Pin: in.pin.String(), ProxyID: in.proxyID,
		Credential: ok.Credential, AllowOffline: in.allowOffline, AutoHostMetrics: in.autoHostMetrics,
	})
	if err != nil {
		d.mu.Lock()
		delete(d.pairing, ok.DeviceID)
		d.mu.Unlock()
		return err
	}
	return nil
}

// release 解除「配对握手进行中」标记，并在后台让重连循环按设备配置接手。
func (d *DialOut) release(id string) {
	d.mu.Lock()
	delete(d.pairing, id)
	d.mu.Unlock()
	d.spawn(func() { d.reconcile(id) })
}

// adopt 把 hello 已被接受的配对会话登记为设备当前的连接方，解除「配对握手进行中」标记；
// 会话结束后由重连循环接替，期间设备参数变化则关闭它并换成重连循环。
func (d *DialOut) adopt(id string, l *dialLoop) {
	d.ops.Lock()
	d.mu.Lock()
	delete(d.pairing, id)
	adopted := !d.closed && d.loops[id] == nil
	if adopted {
		d.loops[id] = l
	}
	d.mu.Unlock()
	d.ops.Unlock()
	if !adopted {
		// 已关闭（Server.Close 会断开会话），或配对期间已有循环（不应发生）：不接管。
		return
	}
	d.spawn(func() {
		// 握手期间可能错过了设备变更，先核对一次。
		d.reconcile(id)
		select {
		case <-l.sess.Done():
		case <-d.done:
			return
		}
		d.reconcile(id)
	})
}

// pairFailure 把配对错误包装为 *PairingError；原因为 unknown 时记 Error 日志（错误文本不含凭据与配对码）。
func (d *DialOut) pairFailure(log *slog.Logger, err error, deviceID string) error {
	pe := &PairingError{Reason: pairingReason(err), DeviceID: deviceID, Err: err}
	var inc *IncompatibleError
	if errors.As(err, &inc) {
		pe.RequiredMin, pe.HubVersion = inc.RequiredMin, inc.HubVersion
	}
	if pe.Reason == ReasonUnknown {
		log.Error("拨出配对失败", "err", err)
	} else {
		log.Info("拨出配对失败", "reason", pe.Reason, "err", err)
	}
	return pe
}
