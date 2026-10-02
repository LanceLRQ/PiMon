package kiosk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

const (
	wsBackoffMax = 30 * time.Second
	wsPingEvery  = 30 * time.Second
	// wsReadTimeout 是读取的静默上限：hub 对每个 ping 都回 pong，超过它仍无任何消息说明连接已死。
	wsReadTimeout = 3 * wsPingEvery
	wsDialTimeout = 10 * time.Second
	wsWriteLimit  = 10 * time.Second
	wsReadLimit   = 1 << 20
	// wsTokenPoll 是握手被 401 拒绝后检查令牌文件是否变化的间隔。
	wsTokenPoll = 10 * time.Second
	// wsLockedWait 是握手被 429 拒绝且没有 Retry-After 时的等待时长，与 hub 的登录锁定时长一致。
	wsLockedWait = 15 * time.Minute
)

// wsURL 把 hub 的 http(s) 地址换成 ws(s) 并拼上 /ws。
func wsURL(hubURL string) (string, error) {
	u, err := url.Parse(hubURL)
	if err != nil {
		return "", fmt.Errorf("无效的 hub 地址 %q: %w", hubURL, err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("hub 地址 %q 必须以 http:// 或 https:// 开头", hubURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("hub 地址 %q 缺少主机", hubURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// WSConfig 是 WebSocket 链路的配置。
type WSConfig struct {
	// HubURL 是 hub 网页地址（http/https）。
	HubURL string
	Clock  clock.Clock
	Log    *slog.Logger
	// OnScreenState 收到屏幕状态（snapshot 或 patch）时调用；由独立 goroutine 以只保留最新值的方式交付。
	OnScreenState func(model.ScreenState)
	// OnConnected 在每次连上后、通知守护进程之前调用。
	OnConnected func()
}

// WSLink 是 HubLink 的真实实现：用屏幕令牌（Bearer）连 hub 的 /ws，断线退避重连。
// 读循环只解码并投递到信箱，回调由独立 goroutine 交付，慢回调不会拖住读循环与 ping。
type WSLink struct {
	cfg WSConfig
	url string

	mu  sync.Mutex
	cur *wsSession
}

// NewWSLink 创建链路并校验 hub 地址。
func NewWSLink(cfg WSConfig) (*WSLink, error) {
	u, err := wsURL(cfg.HubURL)
	if err != nil {
		return nil, err
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &WSLink{cfg: cfg, url: u}, nil
}

// wsSession 是一次连接的发送侧：上报只留最新，唤醒请求合并。
type wsSession struct {
	reports chan model.KioskReport
	wake    chan struct{}
}

func (s *wsSession) putReport(r model.KioskReport) {
	for {
		select {
		case s.reports <- r:
			return
		default:
			select {
			case <-s.reports:
			default:
			}
		}
	}
}

func (l *WSLink) setCur(s *wsSession) {
	l.mu.Lock()
	l.cur = s
	l.mu.Unlock()
}

// Report 上报 kiosk 状态；不阻塞，未连接时丢弃。
func (l *WSLink) Report(r model.KioskReport) {
	l.mu.Lock()
	s := l.cur
	l.mu.Unlock()
	if s != nil {
		s.putReport(r)
	}
}

// Wake 请求 hub 唤醒屏幕（kiosk_wake，时长用 hub 默认）；不阻塞，未连接时丢弃。
func (l *WSLink) Wake() {
	l.mu.Lock()
	s := l.cur
	l.mu.Unlock()
	if s != nil {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// Run 连接 hub 并维持连接，直到 ctx 结束。每次拨号前通过 sink.ReadToken 重读令牌；
// 拨号失败或断开后按 1s 起翻倍、上限 30s 退避，连上后清零。
// 握手被 401 拒绝后，令牌文件内容不变就不再拨号（每 10 秒检查，变化后立即重拨），
// 避免反复失败触发 hub 的登录锁定、连带挡住 Chromium 的 /screen/auth；
// 被 429 拒绝则按 Retry-After（缺省 15 分钟）等待。
func (l *WSLink) Run(ctx context.Context, sink LinkSink) {
	settings := newMailbox[ui.ScreenSettings]()
	builds := newMailbox[string]()
	states := newMailbox[model.ScreenState]()
	var wg sync.WaitGroup
	spawn := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	spawn(func() { settings.run(ctx, sink.OnSettings) })
	spawn(func() { builds.run(ctx, sink.OnBuild) })
	if l.cfg.OnScreenState != nil {
		spawn(func() { states.run(ctx, l.cfg.OnScreenState) })
	}
	defer wg.Wait()

	in := wsInbox{settings: settings, builds: builds, states: states}
	bo := backoff{max: wsBackoffMax}
	rejected := "" // 上次被 401 拒绝的令牌哈希
	for ctx.Err() == nil {
		wait := time.Duration(0)
		token, err := sink.ReadToken()
		switch {
		case err != nil:
			l.cfg.Log.Warn("读取屏幕令牌失败，稍后重试", "err", err)
			wait = bo.next()
		case rejected != "" && tokenHash(token) == rejected:
			wait = wsTokenPoll
		default:
			res := l.session(ctx, sink, token, in)
			switch {
			case res.connected:
				rejected = ""
				bo.reset()
				wait = bo.next()
			case res.status == http.StatusUnauthorized:
				rejected = tokenHash(token)
				l.cfg.Log.Warn("屏幕令牌被 hub 拒绝（401），令牌文件变化前不再重试", "check_every", wsTokenPoll)
				wait = wsTokenPoll
			case res.status == http.StatusTooManyRequests:
				wait = res.retryAfter
				if wait <= 0 {
					wait = wsLockedWait
				}
				l.cfg.Log.Warn("hub 限流了屏幕令牌握手（429），暂停重试", "wait", wait)
			default:
				wait = bo.next()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-l.cfg.Clock.After(wait):
		}
	}
}

type wsInbox struct {
	settings *mailbox[ui.ScreenSettings]
	builds   *mailbox[string]
	states   *mailbox[model.ScreenState]
}

// sessionResult 是一次拨号的结果：connected 表示握手成功过；否则 status 为被拒绝时的 HTTP 状态码（0 表示未拿到响应）。
type sessionResult struct {
	connected  bool
	status     int
	retryAfter time.Duration
}

// parseRetryAfter 解析秒数形式的 Retry-After；无法解析返回 0。
func parseRetryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// session 拨号并服务一次连接。
func (l *WSLink) session(ctx context.Context, sink LinkSink, token string, in wsInbox) sessionResult {
	dctx, cancelDial := context.WithTimeout(ctx, wsDialTimeout)
	conn, resp, err := websocket.Dial(dctx, l.url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	cancelDial()
	if err != nil {
		if resp != nil {
			l.cfg.Log.Warn("连接 hub 被拒绝", "status", resp.StatusCode)
			return sessionResult{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
		}
		l.cfg.Log.Warn("连接 hub 失败", "err", err)
		return sessionResult{}
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(wsReadLimit)

	sctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait() // defer 后进先出：先 cancel 再等写协程
	defer cancel()
	s := &wsSession{reports: make(chan model.KioskReport, 1), wake: make(chan struct{}, 1)}
	l.setCur(s)
	defer l.setCur(nil)

	wg.Add(1)
	go func() { defer wg.Done(); l.writeLoop(sctx, cancel, conn, s) }()

	l.cfg.Log.Info("已连接 hub")
	if l.cfg.OnConnected != nil {
		l.cfg.OnConnected()
	}
	sink.OnConnected()

	for {
		rctx, cancelRead := context.WithTimeout(sctx, wsReadTimeout)
		_, data, err := conn.Read(rctx)
		cancelRead()
		if err != nil {
			if ctx.Err() == nil {
				l.cfg.Log.Warn("与 hub 的连接断开", "err", err)
			}
			return sessionResult{connected: true}
		}
		l.dispatch(data, in)
	}
}

// serverMessage 是 snapshot 与 patch 的并集，只含 kiosk 关心的字段。
type serverMessage struct {
	Type           ui.ServerMessageType `json:"type"`
	Build          string               `json:"build"`
	Entity         string               `json:"entity"`
	ScreenSettings *ui.ScreenSettings   `json:"screen_settings"`
	ScreenState    *model.ScreenState   `json:"screen_state"`
}

func (l *WSLink) dispatch(data []byte, in wsInbox) {
	var m serverMessage
	if err := json.Unmarshal(data, &m); err != nil {
		l.cfg.Log.Warn("无法解析 hub 消息", "err", err)
		return
	}
	switch m.Type {
	case ui.TypeSnapshot:
		if m.Build != "" {
			in.builds.put(m.Build)
		}
		if m.ScreenSettings != nil {
			in.settings.put(*m.ScreenSettings)
		}
		if m.ScreenState != nil {
			in.states.put(*m.ScreenState)
		}
	case ui.TypePatch:
		switch m.Entity {
		case ui.EntitySettings:
			if m.ScreenSettings != nil {
				in.settings.put(*m.ScreenSettings)
			}
		case ui.EntityScreenState:
			if m.ScreenState != nil {
				in.states.put(*m.ScreenState)
			}
		}
	case ui.TypeError:
		l.cfg.Log.Warn("hub 返回协议错误", "msg", string(data))
	}
}

// writeLoop 串行发送上报、唤醒与 ping；写失败即结束本次连接。
func (l *WSLink) writeLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, s *wsSession) {
	defer cancel()
	ping := l.cfg.Clock.After(wsPingEvery)
	for {
		var msg ui.ClientMessage
		select {
		case <-ctx.Done():
			return
		case r := <-s.reports:
			msg = ui.ClientMessage{Type: ui.TypeKioskReport, Kiosk: &r}
		case <-s.wake:
			msg = ui.ClientMessage{Type: ui.TypeKioskWake}
		case <-ping:
			msg = ui.ClientMessage{Type: ui.TypePing}
			ping = l.cfg.Clock.After(wsPingEvery)
		}
		data, err := json.Marshal(msg)
		if err != nil {
			l.cfg.Log.Error("序列化消息失败", "err", err)
			continue
		}
		wctx, cancelWrite := context.WithTimeout(ctx, wsWriteLimit)
		err = conn.Write(wctx, websocket.MessageText, data)
		cancelWrite()
		if err != nil {
			if ctx.Err() == nil {
				l.cfg.Log.Warn("发送消息失败", "type", msg.Type, "err", err)
			}
			return
		}
	}
}
