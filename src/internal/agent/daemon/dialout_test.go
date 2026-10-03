package daemon

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func dialOutHarness(t *testing.T, opts ...option) (*harness, string, certs.SPKI) {
	t.Helper()
	h := newHarness(t, &config.Config{Mode: config.ModeDialOut}, opts...).start()
	addr := h.listenAddr()
	cert, err := certs.LoadOrCreate(h.paths.CertFile, h.paths.KeyFile, t0, certs.Options{})
	must(t, err)
	pin, err := certs.SPKIOf(cert)
	must(t, err)
	return h, addr, pin
}

// pairFake 打开配对窗口并用替身 hub 配对，返回已配对的替身（hello_ok 是否立即回由 hold 决定）。
func pairFake(t *testing.T, h *harness, addr string, pin certs.SPKI, hold chan struct{}) *fakeHub {
	t.Helper()
	w, err := h.d.OpenPairWindow(context.Background())
	must(t, err)
	f, err := dialFake(t, addr, pin, fakeOpts{Code: w.Code, Hold: hold})
	if err != nil {
		t.Fatalf("窗口内配对失败: %v", err)
	}
	return f
}

func authFail(err error) string {
	var af *agentlink.AuthFailError
	if errors.As(err, &af) {
		return af.Reason
	}
	return ""
}

func TestDialOut_窗口外配对被拒(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	if _, err := dialFake(t, addr, pin, fakeOpts{Code: "AAAA-BBBB"}); authFail(err) != agent.ReasonBadCode {
		t.Fatalf("窗口外配对应被拒（bad_code），得到 %v", err)
	}
	if !h.status().PairWindowExpiresAt.IsZero() {
		t.Fatal("没有打开窗口")
	}
}

func TestDialOut_窗口内配对成功且只能用一次(t *testing.T) {
	h, addr, pin := dialOutHarness(t, func(c *Config) {
		c.Hostname = func() (string, error) { return strings.Repeat("h", 300), nil }
	})
	if st := h.status(); !strings.Contains(st.LastError, "尚未") {
		t.Fatalf("未配对时应提示配对: %+v", st)
	}
	w, err := h.d.OpenPairWindow(context.Background())
	must(t, err)
	_, port, _ := net.SplitHostPort(addr)
	if w.Code == "" || strconv.Itoa(w.Port) != port || w.Fingerprint != pin.String() || !w.ExpiresAt.Equal(t0.Add(PairWindowTTL)) {
		t.Fatalf("窗口 = %+v", w)
	}
	if st := h.status(); !st.PairWindowExpiresAt.Equal(w.ExpiresAt) {
		t.Fatalf("状态应显示窗口到期时间: %+v", st)
	}
	var info agent.ListenerInfo
	f, err := dialFake(t, addr, pin, fakeOpts{Code: w.Code, OnCred: func(ok agent.PairOK) error { info = ok.ListenerInfo; return nil }})
	if err != nil {
		t.Fatalf("窗口内配对失败: %v", err)
	}
	if len(info.Hostname) != 256 || info.Version != version.Version {
		t.Fatalf("listener_info = %d 字节 %q", len(info.Hostname), info.Version)
	}
	if c := h.diskConfig(); c.CredentialHash != agent.CredentialHash(f.cred) || c.PrevCredentialHash != "" {
		t.Fatalf("配对应落盘新哈希: %v", c)
	}
	if m, ok := f.next().(*agent.Hello); !ok || len(m.Plugins) != 1 {
		t.Fatalf("应收到带 manifest 的 hello，得到 %#v", m)
	}
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	st := h.status()
	if !st.PairWindowExpiresAt.IsZero() || st.State != StateRunning || st.LastError != "" || st.Listen != addr {
		t.Fatalf("配对后窗口应关闭: %+v", st)
	}
	if _, err := dialFake(t, addr, pin, fakeOpts{Code: w.Code}); authFail(err) != agent.ReasonBadCode {
		t.Fatalf("配对码只能用一次，得到 %v", err)
	}
	h.assertNoSecret(f.cred, w.Code, agent.CredentialHash(f.cred))
	if ringContains(h, f.cred) || ringContains(h, w.Code) {
		t.Fatal("日志里出现了凭据或配对码")
	}
}

func TestDialOut_窗口到期后配对被拒(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	w, err := h.d.OpenPairWindow(context.Background())
	must(t, err)
	h.clk.Advance(PairWindowTTL)
	if _, err := dialFake(t, addr, pin, fakeOpts{Code: w.Code}); authFail(err) != agent.ReasonCodeExpired {
		t.Fatalf("应返回 code_expired，得到 %v", err)
	}
	if !h.status().PairWindowExpiresAt.IsZero() {
		t.Fatal("过期窗口不应再显示")
	}
}

func TestDialOut_并发配对同一窗口只有一个成功(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	w, err := h.d.OpenPairWindow(context.Background())
	must(t, err)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = dialFake(t, addr, pin, fakeOpts{Code: w.Code})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case authFail(err) != agent.ReasonBadCode:
			t.Fatalf("其余配对应得到 bad_code，得到 %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("成功次数 = %d", ok)
	}
}

func TestDialOut_第二次配对后旧hub凭据失效(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })

	hold := make(chan struct{})
	b := pairFake(t, h, addr, pin, hold)
	if c := h.diskConfig(); c.CredentialHash != agent.CredentialHash(b.cred) || c.PrevCredentialHash != agent.CredentialHash(a.cred) {
		t.Fatalf("新 hub hello_ok 之前旧哈希应移到 prev: %v", c)
	}
	// 新会话确认前，旧 hub 仍可用旧凭据重连。
	a2, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred})
	if err != nil {
		t.Fatalf("新会话确认前旧凭据应仍可用: %v", err)
	}
	waitUntil(t, "a 被 a2 顶替", func() bool { return isClosed(a.sess) })

	close(hold)
	waitUntil(t, "prev 被清空", func() bool { return h.diskConfig().PrevCredentialHash == "" })
	waitUntil(t, "a2 被 b 顶替", func() bool { return isClosed(a2.sess) })
	if r := a2.sess.Err(); r.Kind != agentlink.ClosePeerBye || r.Bye != agent.ReasonReplaced {
		t.Fatalf("旧会话应以 replaced 关闭: %+v", r)
	}
	if _, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred}); authFail(err) != agent.ReasonBadCredential {
		t.Fatalf("新 hub 确认后旧凭据应失效，得到 %v", err)
	}
	if !h.status().Connected || isClosed(b.sess) {
		t.Fatal("新 hub 会话应保持")
	}
}

func TestDialOut_新hub入库失败时旧哈希仍可用(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })
	w, err := h.d.OpenPairWindow(context.Background())
	must(t, err)
	_, err = dialFake(t, addr, pin, fakeOpts{Code: w.Code, OnCred: func(agent.PairOK) error { return errors.New("写库失败") }})
	if err == nil {
		t.Fatal("hub 入库失败时配对应失败")
	}
	c := h.diskConfig()
	if c.PrevCredentialHash != agent.CredentialHash(a.cred) || c.CredentialHash == agent.CredentialHash(a.cred) {
		t.Fatalf("配对哈希已替换、旧哈希应在 prev: %v", c)
	}
	a2, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred})
	if err != nil {
		t.Fatalf("旧 hub 应仍可用旧凭据连上: %v", err)
	}
	waitUntil(t, "a2 就绪", func() bool { return isClosed(a.sess) && h.status().Connected })
	never(t, "旧凭据的 hello_ok 清空 prev", func() bool { return h.diskConfig().PrevCredentialHash == "" })
	if isClosed(a2.sess) {
		t.Fatal("a2 应保持连接")
	}
}

func TestDialOut_顶替只保留一个会话(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })
	a2, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred})
	must(t, err)
	waitUntil(t, "旧会话被顶替", func() bool { return isClosed(a.sess) })
	if r := a.sess.Err(); r.Bye != agent.ReasonReplaced {
		t.Fatalf("旧会话应以 replaced 关闭: %+v", r)
	}
	waitUntil(t, "只剩一个会话", func() bool {
		h.d.mu.Lock()
		defer h.d.mu.Unlock()
		return len(h.d.conns) == 1 && h.d.cur != nil && h.d.cur.s.Peer().DeviceID == a2.sess.Peer().DeviceID
	})
}

func TestDialOut_吊销清空哈希且继续监听可重新配对(t *testing.T) {
	h, addr, pin := dialOutHarness(t)
	a := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "A 连上", func() bool { return h.status().Connected })
	a.sess.Close(agent.ReasonRevoked)
	waitUntil(t, "已吊销", func() bool { return h.status().State == StateRevoked })
	if c := h.diskConfig(); c.CredentialHash != "" || c.PrevCredentialHash != "" {
		t.Fatalf("吊销应清空两个哈希: %v", c)
	}
	if st := h.status(); st.Connected || !strings.Contains(st.LastError, "pair") {
		t.Fatalf("状态 = %+v", st)
	}
	if _, err := dialFake(t, addr, pin, fakeOpts{Cred: a.cred}); authFail(err) != agent.ReasonBadCredential {
		t.Fatalf("吊销后旧凭据应失效，得到 %v", err)
	}
	if !h.running() {
		t.Fatal("守护进程不应退出")
	}
	pairFake(t, h, addr, pin, nil)
	waitUntil(t, "重新配对后连上", func() bool { st := h.status(); return st.Connected && st.State == StateRunning })
}

func TestDialOut_拨入模式不能开配对窗口(t *testing.T) {
	hub := startStubHub(t, nil)
	h := newHarness(t, hub.config()).start()
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	if _, err := h.d.OpenPairWindow(context.Background()); !errors.Is(err, ErrNotDialOut) {
		t.Fatalf("应返回 ErrNotDialOut，得到 %v", err)
	}
}

func isClosed(s *agentlink.Session) bool {
	select {
	case <-s.Done():
		return true
	case <-time.After(0):
		return false
	}
}
