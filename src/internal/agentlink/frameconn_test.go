package agentlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 冒烟：真实 coder/websocket 连接上跑一次请求/响应与 bye。
func TestWSFrameConn_真实websocket冒烟(t *testing.T) {
	serverSess := make(chan *Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s := newSession(newWSFrameConn(c), Peer{DeviceID: "agent1", RemoteAddr: r.RemoteAddr, Role: agent.RoleAgent},
			SessionOptions{Log: quietLog(), Handler: func(s *Session, m agent.Message) {
				if rn, ok := m.(*agent.RunNow); ok {
					_ = s.Send(&agent.RunResult{RequestID: rn.RequestID, InstanceID: rn.InstanceID,
						RunOutcome: agent.RunOutcome{Error: "no", ErrorKind: agent.ErrorKindNotAssigned}})
				}
			}})
		serverSess <- s
		<-s.Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	client := newSession(newWSFrameConn(c), Peer{DeviceID: "hub", Role: agent.RoleHub}, SessionOptions{Log: quietLog()})
	defer client.Close("")

	resp, err := client.Request(ctx, &agent.RunNow{InstanceID: "i1"}, 5*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if rr, ok := resp.(*agent.RunResult); !ok || rr.ErrorKind != agent.ErrorKindNotAssigned {
		t.Fatalf("响应 = %#v", resp)
	}

	s := <-serverSess
	client.Close(agent.ReasonShutdown)
	if r := waitDone(t, s); r.Kind != ClosePeerBye || r.Bye != agent.ReasonShutdown {
		t.Fatalf("服务端 CloseReason = %+v", r)
	}
	if r := client.Err(); r.Kind != CloseLocal {
		t.Fatalf("客户端 CloseReason = %+v", r)
	}
}

func TestWSFrameConn_二进制帧报错(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		_ = c.Write(r.Context(), websocket.MessageBinary, []byte("{}"))
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	fc := newWSFrameConn(c)
	defer fc.Close()
	if _, err := fc.Read(ctx); err == nil || !strings.Contains(err.Error(), "非文本帧") {
		t.Fatalf("err = %v", err)
	}
}
