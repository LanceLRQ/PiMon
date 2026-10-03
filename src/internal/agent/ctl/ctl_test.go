package ctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// fakeBackend 记录调用；block 非空时 UpgradeInfo 阻塞到 ctx 结束（测试关闭时取消在途请求）。
type fakeBackend struct {
	calls   atomic.Int64
	pinMu   sync.Mutex
	pin     certs.SPKI
	pairErr error
	block   chan struct{}
}

func (b *fakeBackend) Status() Status {
	b.calls.Add(1)
	return Status{Mode: "dial_in", HubURL: "wss://10.0.0.2:31418", Pin: "SHA256:abc", Connected: true, Since: t0,
		State: "running", Instances: 3, Version: "0.2.0", ProtocolVersion: 1, MinCompatibleVersion: 1,
		Incompatible: &Incompatible{Reason: "x", RequiredMin: 2, HubVersion: "0.9.0"}}
}

func (b *fakeBackend) OpenPairWindow(context.Context) (PairWindow, error) {
	b.calls.Add(1)
	if b.pairErr != nil {
		return PairWindow{}, b.pairErr
	}
	return PairWindow{Code: "AAAA-BBBB", Fingerprint: "SHA256:fp", Port: 31418, IPs: []string{"192.168.1.5"}, ExpiresAt: t0}, nil
}

func (b *fakeBackend) Repin(_ context.Context, pin certs.SPKI) error {
	b.calls.Add(1)
	b.pinMu.Lock()
	b.pin = pin
	b.pinMu.Unlock()
	return nil
}

func (b *fakeBackend) Reload(context.Context) error {
	b.calls.Add(1)
	return nil
}

func (b *fakeBackend) UpgradeInfo(ctx context.Context) (UpgradeInfo, error) {
	b.calls.Add(1)
	if b.block != nil {
		close(b.block)
		<-ctx.Done()
		return UpgradeInfo{}, ctx.Err()
	}
	return UpgradeInfo{HubVersion: "0.3.0", WebPort: 31415, SHA256: "deadbeef", HubHost: "10.0.0.2"}, nil
}

// shortDir 返回短路径临时目录（macOS sun_path 上限 104 字节，t.TempDir 带测试名会超）。
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "pctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

type served struct {
	path string
	srv  *Server
	stop func()
}

func serve(t *testing.T, b Backend, mod func(*ServerConfig)) *served {
	t.Helper()
	path := filepath.Join(shortDir(t), "agent.sock")
	cfg := ServerConfig{Path: path, Backend: b, Log: slog.New(slog.DiscardHandler)}
	if mod != nil {
		mod(&cfg)
	}
	srv, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	s := &served{path: path, srv: srv}
	var once sync.Once
	s.stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Serve 返回错误: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Serve 未在取消后返回")
			}
		})
	}
	t.Cleanup(s.stop)
	return s
}

func TestRoundTrip_五个操作(t *testing.T) {
	b := &fakeBackend{}
	s := serve(t, b, nil)
	c := Client{Path: s.path}
	ctx := context.Background()

	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := b.Status(); st.Mode != want.Mode || st.HubURL != want.HubURL || !st.Since.Equal(t0) || st.Instances != 3 ||
		st.Incompatible == nil || *st.Incompatible != *want.Incompatible || st.ProtocolVersion != 1 {
		t.Fatalf("status = %+v", st)
	}

	w, err := c.Pair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != "AAAA-BBBB" || w.Port != 31418 || w.Fingerprint != "SHA256:fp" || len(w.IPs) != 1 || !w.ExpiresAt.Equal(t0) {
		t.Fatalf("pair = %+v", w)
	}

	var pin certs.SPKI
	pin[0], pin[31] = 1, 2
	if err := c.Repin(ctx, pin.String()); err != nil {
		t.Fatal(err)
	}
	b.pinMu.Lock()
	got := b.pin
	b.pinMu.Unlock()
	if got != pin {
		t.Fatalf("repin 收到 %v", got)
	}

	if err := c.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	ui, err := c.UpgradeInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ui != (UpgradeInfo{HubVersion: "0.3.0", WebPort: 31415, SHA256: "deadbeef", HubHost: "10.0.0.2"}) {
		t.Fatalf("upgrade_info = %+v", ui)
	}
}

// rawCall 发一行原文，返回一行应答。
func rawCall(t *testing.T, path, line string) Response {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = io.WriteString(conn, line) // 超长请求时服务端读满上限就应答并关闭，余下的写可能失败
	resp, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("读取应答: %v", err)
	}
	var r Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatalf("应答不是 JSON: %q", resp)
	}
	return r
}

func TestProtocol_应答格式(t *testing.T) {
	s := serve(t, &fakeBackend{}, nil)
	r := rawCall(t, s.path, `{"op":"status"}`+"\n")
	if !r.OK || r.Error != "" || !strings.Contains(string(r.Data), `"mode":"dial_in"`) {
		t.Fatalf("status 应答 = %+v", r)
	}
	r = rawCall(t, s.path, `{"op":"reload"}`+"\n")
	if !r.OK || len(r.Data) != 0 {
		t.Fatalf("reload 应答 = %+v", r)
	}
}

func TestProtocol_未知操作与非法请求返回错误且继续服务(t *testing.T) {
	b := &fakeBackend{}
	s := serve(t, b, nil)
	cases := map[string]string{
		`{"op":"bogus"}` + "\n":                   "未知操作",
		"not json\n":                              "JSON",
		`{"fp":"x"}` + "\n":                       "op",
		`{"op":"repin","fp":"SHA256:bad"}` + "\n": "指纹",
		strings.Repeat("a", MaxLine+10):           "64 KiB",
	}
	for line, want := range cases {
		r := rawCall(t, s.path, line)
		if r.OK || !strings.Contains(r.Error, want) {
			t.Fatalf("请求 %.40q 的应答 = %+v，应含 %q", line, r, want)
		}
	}
	if n := b.calls.Load(); n != 0 {
		t.Fatalf("非法请求不应调用后端，调用了 %d 次", n)
	}
	if _, err := (Client{Path: s.path}).Status(context.Background()); err != nil {
		t.Fatalf("之后仍应正常服务: %v", err)
	}
}

func TestProtocol_恰好64KiB的一行可接受(t *testing.T) {
	s := serve(t, &fakeBackend{}, nil)
	head := `{"op":"status","fp":"`
	line := head + strings.Repeat("a", MaxLine-len(head)-2) + `"}`
	if len(line) != MaxLine {
		t.Fatalf("构造长度 %d", len(line))
	}
	if r := rawCall(t, s.path, line+"\n"); !r.OK {
		t.Fatalf("64 KiB 的请求应被接受: %+v", r)
	}
}

func TestProtocol_后端错误原样返回(t *testing.T) {
	b := &fakeBackend{pairErr: errors.New("该操作只在拨出模式下可用：拨入模式请用 install --code 重新配对")}
	s := serve(t, b, nil)
	_, err := (Client{Path: s.path}).Pair(context.Background())
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(re.Error(), "install --code") {
		t.Fatalf("应返回远端错误，得到 %v", err)
	}
}

func TestListen_套接字权限0600(t *testing.T) {
	s := serve(t, &fakeBackend{}, nil)
	fi, err := os.Lstat(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("套接字模式 = %v", fi.Mode())
	}
	s.stop()
	if _, err := os.Lstat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("关闭后应删除套接字文件: %v", err)
	}
}

func TestServe_非同用户连接被拒(t *testing.T) {
	b := &fakeBackend{}
	var seen atomic.Int64
	s := serve(t, b, func(c *ServerConfig) {
		c.PeerUID = func(*net.UnixConn) (int, error) { return 4242, nil }
		c.Authorize = func(uid int) bool { seen.Store(int64(uid)); return uid == 1000 || uid == 0 }
	})
	_, err := (Client{Path: s.path}).Status(context.Background())
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(re.Msg, "无权") {
		t.Fatalf("应被拒绝，得到 %v", err)
	}
	if seen.Load() != 4242 || b.calls.Load() != 0 {
		t.Fatalf("校验的 uid = %d，后端调用 %d 次", seen.Load(), b.calls.Load())
	}
}

func TestServe_对端凭据读取失败被拒(t *testing.T) {
	b := &fakeBackend{}
	s := serve(t, b, func(c *ServerConfig) {
		c.PeerUID = func(*net.UnixConn) (int, error) { return -1, errors.New("boom") }
	})
	if _, err := (Client{Path: s.path}).Status(context.Background()); err == nil || b.calls.Load() != 0 {
		t.Fatalf("应被拒绝，得到 %v", err)
	}
}

func TestDefaultAuthorize_只允许同用户与root(t *testing.T) {
	a := defaultAuthorize(1000)
	if !a(1000) || !a(0) || a(1001) || a(-1) {
		t.Fatal("默认校验应只允许 euid 与 root")
	}
}

func TestListen_清理残留套接字(t *testing.T) {
	path := filepath.Join(shortDir(t), "agent.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	_ = l.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("应留下残留文件")
	}
	srv, err := Listen(ServerConfig{Path: path, Backend: &fakeBackend{}})
	if err != nil {
		t.Fatalf("残留套接字应被清理: %v", err)
	}
	_ = srv.Close()
}

func TestListen_已有进程监听时拒绝(t *testing.T) {
	s := serve(t, &fakeBackend{}, nil)
	if _, err := Listen(ServerConfig{Path: s.path, Backend: &fakeBackend{}}); err == nil || !strings.Contains(err.Error(), "已在运行") {
		t.Fatalf("活跃监听不应被顶掉: %v", err)
	}
	if _, err := (Client{Path: s.path}).Status(context.Background()); err != nil {
		t.Fatalf("原监听应不受影响: %v", err)
	}
}

func TestListen_拒绝覆盖非套接字文件(t *testing.T) {
	path := filepath.Join(shortDir(t), "agent.sock")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(ServerConfig{Path: path, Backend: &fakeBackend{}}); err == nil {
		t.Fatal("不应删除普通文件")
	}
	if b, _ := os.ReadFile(path); string(b) != "x" {
		t.Fatal("普通文件被改动")
	}
}

func TestListen_父目录权限过宽拒绝(t *testing.T) {
	dir := shortDir(t)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(ServerConfig{Path: filepath.Join(dir, "a.sock"), Backend: &fakeBackend{}}); err == nil || !strings.Contains(err.Error(), "权限过宽") {
		t.Fatalf("应拒绝: %v", err)
	}
}

func TestListen_父目录不存在时创建为0700(t *testing.T) {
	dir := filepath.Join(shortDir(t), "run")
	srv, err := Listen(ServerConfig{Path: filepath.Join(dir, "a.sock"), Backend: &fakeBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("目录 = %v %v", fi.Mode(), err)
	}
}

func TestListen_路径过长报错带长度(t *testing.T) {
	path := filepath.Join(shortDir(t), strings.Repeat("x", 120)+".sock")
	_, err := Listen(ServerConfig{Path: path, Backend: &fakeBackend{}})
	if err == nil || !strings.Contains(err.Error(), "字节") || !strings.Contains(err.Error(), itoa(len(path))) {
		t.Fatalf("错误应带路径长度 %d: %v", len(path), err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestClient_守护进程未运行(t *testing.T) {
	path := filepath.Join(shortDir(t), "agent.sock")
	_, err := (Client{Path: path}).Status(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("文件不存在应报未运行: %v", err)
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	_ = l.Close()
	if _, err := (Client{Path: path}).Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("残留套接字应报未运行: %v", err)
	}
}

func TestServe_关闭时取消在途请求(t *testing.T) {
	b := &fakeBackend{block: make(chan struct{})}
	s := serve(t, b, nil)
	errc := make(chan error, 1)
	go func() {
		_, err := (Client{Path: s.path}).UpgradeInfo(context.Background())
		errc <- err
	}()
	<-b.block // 请求已进入后端
	s.stop()  // Serve 应取消在途请求并等它结束后返回
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("在途请求应失败")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("客户端未结束")
	}
}

func TestClient_ctx取消时立即返回(t *testing.T) {
	b := &fakeBackend{block: make(chan struct{})}
	s := serve(t, b, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := (Client{Path: s.path}).UpgradeInfo(ctx)
		errc <- err
	}()
	<-b.block
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("应返回 ctx 取消: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("客户端未结束")
	}
}

func TestPairWindow_日志不含配对码(t *testing.T) {
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("x", "w", PairWindow{Code: "SECRET-CODE", Fingerprint: "fp"})
	if strings.Contains(sb.String(), "SECRET-CODE") {
		t.Fatalf("日志含配对码: %s", sb.String())
	}
}
