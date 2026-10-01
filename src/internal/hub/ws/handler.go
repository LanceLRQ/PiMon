package ws

import (
	"context"
	"encoding/json"
	"net/http"

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
	if !httpx.CheckOrigin(r, httpx.Info(r)) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeOriginMismatch, nil)
		return
	}
	kind, ok, err := h.session(r)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, nil)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeAuthRequired, nil)
		return
	}
	if h.isClosed() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	// Origin 已按反代解析出的协议与 Host 校验过，库自带的按 r.Host 比较在反代后会误判。
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxClientMessage)

	ctx := context.WithoutCancel(r.Context())
	c, err := h.attach(ctx, wsSink{conn}, kind)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	h.run(c, conn)
}

func (h *Hub) session(r *http.Request) (auth.SessionKind, bool, error) {
	ck, err := r.Cookie(auth.CookieName)
	if err != nil || ck.Value == "" {
		return "", false, nil
	}
	return h.cfg.Sessions.Lookup(r.Context(), ck.Value)
}

// run 是连接的读循环：任何客户端消息都让空闲计时重新开始，超过 idleTimeout 没有消息即断开。
// 计时器在处理消息之前登记，因此客户端收到应答时它一定已经重新开始。
func (h *Hub) run(c *client, conn *websocket.Conn) {
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

	idle := h.clk.After(idleTimeout)
	for {
		select {
		case raw := <-in:
			idle = h.clk.After(idleTimeout)
			h.handle(c, raw)
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
		// M1d 的屏幕视口上报；本期只算作活动。
	default:
		c.sendError(ui.ErrBadMessage, map[string]any{})
	}
}
