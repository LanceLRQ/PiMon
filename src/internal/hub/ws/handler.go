package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// maxClientMessage 是客户端单条消息的大小上限（客户端消息都很小）。
const maxClientMessage = 16 << 10

// Handler 返回 /ws 的握手处理器。依赖外层的 httpx.WithRequestInfo。
// 握手是 GET，RequireSameOrigin 不会拦它，所以这里显式校验 Origin。
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(h.serve)
}

func (h *Hub) serve(w http.ResponseWriter, r *http.Request) {
	if token, ok := bearerToken(r); ok {
		h.serveKiosk(w, r, token)
		return
	}
	if !httpx.CheckOrigin(r, httpx.Info(r)) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeOriginMismatch, nil)
		return
	}
	token, kind, ok, err := h.session(r)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, nil)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
		return
	}
	h.upgrade(w, r, kind, token)
}

// bearerToken 取出 Authorization: Bearer 令牌；带了 Bearer 方案但令牌为空时返回空串与 ok=true。
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// serveKiosk 处理 kiosk 的握手：Bearer 屏幕令牌，非浏览器客户端不做 Origin 校验，也不落会话行。
// 令牌校验与失败限流由 KioskAuth 完成（与 /screen/auth 共用限流）。
func (h *Hub) serveKiosk(w http.ResponseWriter, r *http.Request, token string) {
	if h.cfg.KioskAuth == nil || h.cfg.Kiosk == nil {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
		return
	}
	ok, locked, err := h.cfg.KioskAuth.Authenticate(r, token)
	switch {
	case err != nil:
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, nil)
	case locked > 0:
		httpx.WriteLocked(w, r, h.clk.Now(), locked)
	case !ok:
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
	default:
		h.upgrade(w, r, kindKiosk, token)
	}
}

// upgrade 升级为 WebSocket 并进入读循环；鉴权已在调用方完成。
func (h *Hub) upgrade(w http.ResponseWriter, r *http.Request, kind auth.SessionKind, token string) {
	if h.isClosed() {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeShuttingDown, nil)
		return
	}
	// Origin 已按反代解析出的协议与 Host 校验过，库自带的按 r.Host 比较在反代后会误判。
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxClientMessage)

	// 计时器先于 attach 登记：客户端收到 snapshot 时它们一定已经开始计时。
	idle := h.clk.After(idleTimeout)
	recheck := h.clk.After(sessionRecheckInterval)
	ctx := context.WithoutCancel(r.Context())
	c, err := h.attach(ctx, wsSink{conn}, kind, token)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	h.run(c, conn, idle, recheck)
}

func (h *Hub) session(r *http.Request) (token string, kind auth.SessionKind, ok bool, err error) {
	ck, cerr := r.Cookie(auth.CookieName)
	if cerr != nil || ck.Value == "" {
		return "", "", false, nil
	}
	kind, ok, err = h.cfg.Sessions.Lookup(r.Context(), ck.Value)
	return ck.Value, kind, ok, err
}

// run 是连接的读循环：任何客户端消息都让空闲计时重新开始，超过 idleTimeout 没有消息即断开。
// 计时器在处理消息之前登记，因此客户端收到应答时它一定已经重新开始。
// 另外每 sessionRecheckInterval 复核一次会话，登出、吊销或过期的会话即使一直在 ping 也会被断开。
func (h *Hub) run(c *client, conn *websocket.Conn, idle, recheck <-chan time.Time) {
	in := make(chan []byte)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, b, err := conn.Read(c.ctx)
			if err != nil {
				return
			}
			select {
			case in <- b:
			case <-c.done:
				return
			}
		}
	}()

	for {
		select {
		case raw := <-in:
			idle = h.clk.After(idleTimeout)
			h.handle(c, raw)
		case <-recheck:
			recheck = h.clk.After(sessionRecheckInterval)
			h.recheck(c)
		case <-idle:
			c.kill(websocket.StatusPolicyViolation, "idle timeout", false)
		case <-readDone:
			c.kill(websocket.StatusNormalClosure, "bye", true)
		case <-c.done:
			<-c.finished
			_ = conn.CloseNow()
			return
		}
	}
}

func (h *Hub) handle(c *client, raw []byte) {
	var msg ui.ClientMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		c.sendError(ui.ErrBadMessage, map[string]any{})
		return
	}
	switch msg.Type {
	case ui.TypePing:
		pong, err := json.Marshal(ui.Pong{Type: ui.TypePong, ServerTime: h.clk.Now().UTC()})
		if err == nil {
			c.enqueue(pong)
		}
	case ui.TypeSubscribe:
		h.subscribe(c.ctx, c, msg.Topics)
	case ui.TypeViewportReport:
		h.handleReport(c, msg)
	case ui.TypeKioskReport, ui.TypeKioskWake:
		h.handleKiosk(c, msg)
	default:
		c.sendError(ui.ErrBadMessage, map[string]any{})
	}
}
