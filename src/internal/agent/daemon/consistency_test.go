package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// 连接循环读完配置、还没开始本轮连接时 repin 落盘并请求重建：重建请求不能被本轮清掉，
// 否则会一直用旧指纹按最大退避重试（假时钟不推进就永远连不上）。
func TestConnLoop_读配置后到达的repin不丢失(t *testing.T) {
	hub := startStubHub(t, nil)
	conf := hub.config()
	_, wrong := agentlinktest.NewCert(t)
	conf.HubPin = wrong.String()
	h := newHarness(t, conf)
	var once sync.Once
	h.d.testHookLoaded = func() {
		once.Do(func() {
			if err := h.d.Repin(context.Background(), hub.srv.Pin); err != nil {
				t.Errorf("repin: %v", err)
			}
		})
	}
	h.start()
	waitUntil(t, "按 repin 后的指纹连上", func() bool { return h.status().Connected })
	if st := h.status(); st.Pin != hub.srv.Pin.String() {
		t.Fatalf("状态 = %+v", st)
	}
}

// 配对过程中配置被改成拨入模式（例如重新 install）：不得把凭据哈希写进拨入配置，配对失败且窗口恢复。
func TestDialOut_配对时配置已不是拨出模式则失败并恢复窗口(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	w, err := h.d.OpenPairWindow(context.Background())
	must(t, err)
	dialIn := config.Config{Mode: config.ModeDialIn, HubURL: "wss://127.0.0.1:1", HubPin: pin.String(), Credential: "c"}
	must(t, config.Save(h.paths.Config, dialIn))
	if _, err := dialFake(t, addr, pin, fakeOpts{Code: w.Code}); err == nil {
		t.Fatal("配置已不是拨出模式时配对应失败")
	}
	if c := h.diskConfig(); c.CredentialHash != "" || c.PrevCredentialHash != "" || c.Mode != config.ModeDialIn {
		t.Fatalf("不应改动拨入配置: %v", c)
	}
	if h.status().PairWindowExpiresAt.IsZero() {
		t.Fatal("配对失败后窗口应恢复")
	}
}

// 非当前会话（未收到 hello_ok）收到 incompatible：只关闭该会话，不覆盖当前会话的全局状态。
func TestSession_非当前会话的不兼容不改全局状态(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	b, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred, Hold: hold})
	must(t, err)
	if _, ok := b.next().(*agent.Hello); !ok {
		t.Fatal("B 应先收到 hello")
	}
	must(t, b.sess.Send(&agent.Incompatible{Reason: agent.ReasonVersionTooOld, RequiredMin: 2, HubVersion: "0.9.0"}))
	waitUntil(t, "B 被关闭", func() bool { return isClosed(b.sess) })
	if r := b.sess.Err(); r.Bye != agent.ReasonVersionTooOld {
		t.Fatalf("B 应以版本原因关闭: %+v", r)
	}
	st := h.status()
	if st.State != StateRunning || st.Incompatible != nil || !st.Connected || st.LastError != "" {
		t.Fatalf("当前会话的状态不应被覆盖: %+v", st)
	}
	if isClosed(a.sess) {
		t.Fatal("当前会话 A 不应受影响")
	}
}
