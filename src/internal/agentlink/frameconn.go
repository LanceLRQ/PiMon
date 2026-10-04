package agentlink

import (
	"context"
	"fmt"

	"github.com/coder/websocket"
)

// frameConn 是会话与握手所需的最小帧连接：每次读写一帧 JSON 文本。
// 握手（A5）与会话都在它之上进行；测试用 channel 实现替身。
type frameConn interface {
	// Read 读取一帧；ctx 取消时返回错误（coder/websocket 会顺带关闭连接）。
	Read(ctx context.Context) ([]byte, error)
	// Write 写出一帧文本。
	Write(ctx context.Context, b []byte) error
	// Close 立即关闭底层连接，不做关闭握手，可重复调用。
	Close() error
	// SetReadLimit 设置单帧读取上限（字节）。
	SetReadLimit(n int64)
}

// wsFrameConn 把 coder/websocket 连接适配为 frameConn，只收发文本帧。
type wsFrameConn struct {
	c *websocket.Conn
}

func newWSFrameConn(c *websocket.Conn) frameConn { return wsFrameConn{c: c} }

func (w wsFrameConn) Read(ctx context.Context) ([]byte, error) {
	typ, b, err := w.c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, fmt.Errorf("收到非文本帧（类型 %v）", typ)
	}
	return b, nil
}

func (w wsFrameConn) Write(ctx context.Context, b []byte) error {
	return w.c.Write(ctx, websocket.MessageText, b)
}

// Close 不做关闭握手：对端可能已不响应，握手会卡住；断开原因已经由 bye 传达。
func (w wsFrameConn) Close() error { return w.c.CloseNow() }

func (w wsFrameConn) SetReadLimit(n int64) { w.c.SetReadLimit(n) }
