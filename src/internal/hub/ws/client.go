package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// sink 是连接的底层写端；生产环境是 websocket.Conn，测试可换成会阻塞的假实现。
type sink interface {
	write(ctx context.Context, b []byte) error
	close(code websocket.StatusCode, reason string)
}

type wsSink struct{ conn *websocket.Conn }

func (s wsSink) write(ctx context.Context, b []byte) error {
	return s.conn.Write(ctx, websocket.MessageText, b)
}

func (s wsSink) close(code websocket.StatusCode, reason string) { _ = s.conn.Close(code, reason) }

// client 是一个已登记的连接。topics 只在持有 hub.bmu 时读写。
type client struct {
	hub    *Hub
	kind   auth.SessionKind
	sink   sink
	queue  chan []byte
	topics map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
	// done 在连接被判定为死亡的那一刻关闭（之后入队的消息一律丢弃）；
	// finished 在关闭动作（含关闭握手）做完后关闭。
	done     chan struct{}
	finished chan struct{}
	once     sync.Once
}

func newClient(h *Hub, parent context.Context, s sink, kind auth.SessionKind) *client {
	ctx, cancel := context.WithCancel(parent)
	return &client{
		hub: h, kind: kind, sink: s, queue: make(chan []byte, h.cfg.QueueSize),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{}),
	}
}

// enqueue 非阻塞入队；队列已满说明这是慢连接，立即断开，让客户端重连后拿 snapshot。
func (c *client) enqueue(b []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.queue <- b:
	default:
		slog.Warn("WebSocket 连接发送队列已满，断开慢连接")
		go c.kill(websocket.StatusPolicyViolation, "slow consumer", true)
	}
}

func (c *client) sendError(code string, details map[string]any) {
	raw, err := json.Marshal(ui.ErrorMessage{Type: ui.TypeError, Error: ui.ErrorBody{Code: code, Details: details}})
	if err != nil {
		return
	}
	c.enqueue(raw)
}

// writeLoop 把队列里的消息依次写出，直到连接结束。
func (c *client) writeLoop() {
	for {
		select {
		case b := <-c.queue:
			if err := c.sink.write(c.ctx, b); err != nil {
				c.kill(websocket.StatusInternalError, "write failed", true)
				return
			}
		case <-c.done:
			return
		}
	}
}

// kill 结束连接，只有第一次调用生效。abrupt 为真时不做关闭握手（慢连接的写可能卡死，
// 握手也会卡住），直接取消上下文让底层连接关闭。
func (c *client) kill(code websocket.StatusCode, reason string, abrupt bool) {
	c.once.Do(func() {
		close(c.done)
		c.hub.detach(c)
		if abrupt {
			c.cancel()
		} else {
			c.sink.close(code, reason)
			c.cancel()
		}
		close(c.finished)
	})
	<-c.finished
}
