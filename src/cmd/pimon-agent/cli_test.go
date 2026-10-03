package main

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
)

// syncBuf 是并发安全的输出缓冲（守护进程的日志协程会写 stderr）。
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// testEnv 是一个用户模式的 agent 环境：HOME 用短路径（macOS 套接字路径上限 104 字节）。
type testEnv struct {
	t     *testing.T
	home  string
	paths config.Paths
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	home, err := os.MkdirTemp("", "pa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	p, err := config.Resolve(config.Env{GOOS: goruntime.GOOS, EUID: os.Geteuid(), Home: home, User: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureDataDir(p); err != nil {
		t.Fatal(err)
	}
	return &testEnv{t: t, home: home, paths: p}
}

func (e *testEnv) save(c config.Config) {
	e.t.Helper()
	if err := config.Save(e.paths.Config, c); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) getenv(k string) string {
	if k == "HOME" {
		return e.home
	}
	return ""
}

// cli 返回一个输出到独立缓冲的命令行实例；拨入方向的 mDNS 兜底换成空实现，不发组播。
func (e *testEnv) cli() (*cli, *syncBuf, *syncBuf) {
	out, errb := &syncBuf{}, &syncBuf{}
	c := newCLI(strings.NewReader(""), out, errb, e.getenv)
	c.tweak = func(cfg *daemon.Config) {
		cfg.MDNS = func(context.Context, string) ([]netip.Addr, error) { return nil, nil }
	}
	return c, out, errb
}

// call 运行一条命令，返回退出码与输出。
func (e *testEnv) call(args ...string) (int, string, string) {
	c, out, errb := e.cli()
	code := c.run(args)
	return code, out.String(), errb.String()
}

// runningDaemon 是 `run --user` 启动的守护进程，信号经注入的通道送达。
type runningDaemon struct {
	sig    chan os.Signal
	code   chan int
	stderr *syncBuf
}

func (e *testEnv) startDaemon(extra ...string) *runningDaemon {
	e.t.Helper()
	c, _, errb := e.cli()
	rd := &runningDaemon{sig: make(chan os.Signal, 4), code: make(chan int, 1), stderr: errb}
	c.signals = func() (<-chan os.Signal, func()) { return rd.sig, func() {} }
	go func() { rd.code <- c.run(append([]string{"run", "--user"}, extra...)) }()
	waitUntil(e.t, "控制套接字就绪", func() bool {
		_, err := (ctl.Client{Path: e.paths.CtlSock}).Status(context.Background())
		return err == nil
	})
	e.t.Cleanup(func() { rd.stop(e.t) })
	return rd
}

// stop 发 SIGTERM 并等待退出码（可重复调用）。
func (rd *runningDaemon) stop(t *testing.T) int {
	t.Helper()
	select {
	case rd.sig <- syscall.SIGTERM:
	default:
	}
	select {
	case code := <-rd.code:
		rd.code <- code
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("守护进程未在 SIGTERM 后退出")
		return -1
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

const testHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func dialOutConf() config.Config {
	return config.Config{Mode: config.ModeDialOut, Listen: "127.0.0.1:0", CredentialHash: testHash}
}

func dialInConf(t *testing.T) (config.Config, string) {
	t.Helper()
	var pin certs.SPKI
	pin[0] = 7
	cred := "credential-secret-value-0123456789abcdefghij"
	return config.Config{Mode: config.ModeDialIn, HubURL: "wss://127.0.0.1:1", HubPin: pin.String(),
		Credential: config.Credential(cred), HubHostname: "pimon"}, cred
}

func TestRun_SIGHUP重载_SIGTERM优雅退出(t *testing.T) {
	e := newEnv(t)
	e.save(dialOutConf())
	rd := e.startDaemon("--log-level", "debug")
	rd.sig <- syscall.SIGHUP
	waitUntil(t, "处理 SIGHUP", func() bool { return strings.Contains(rd.stderr.String(), "SIGHUP") })
	if code := rd.stop(t); code != 0 {
		t.Fatalf("SIGTERM 后退出码 = %d，stderr=%s", code, rd.stderr.String())
	}
	if _, err := os.Lstat(e.paths.CtlSock); !os.IsNotExist(err) {
		t.Fatalf("退出后应删除控制套接字: %v", err)
	}
	if !strings.Contains(rd.stderr.String(), "level=DEBUG") {
		t.Fatal("--log-level debug 应输出 DEBUG 日志")
	}
}

func TestRun_日志级别非法为用法错误(t *testing.T) {
	e := newEnv(t)
	if code, _, errb := e.call("run", "--user", "--log-level", "loud"); code != 2 || !strings.Contains(errb, "日志级别") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
}

func TestRun_控制套接字被占用时失败(t *testing.T) {
	e := newEnv(t)
	e.save(dialOutConf())
	e.startDaemon()
	if code, _, errb := e.call("run", "--user"); code != 1 || !strings.Contains(errb, "控制套接字") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
}

func TestStatusPair_输出不含凭据哈希且配对码只在pair输出(t *testing.T) {
	e := newEnv(t)
	e.save(dialOutConf())
	rd := e.startDaemon()
	waitUntil(t, "拨出监听就绪", func() bool {
		st, err := (ctl.Client{Path: e.paths.CtlSock}).Status(context.Background())
		return err == nil && st.Listen != "127.0.0.1:0"
	})
	code, pairOut, errb := e.call("pair", "--user")
	if code != 0 {
		t.Fatalf("pair code=%d err=%q", code, errb)
	}
	w := between(pairOut, "配对码：", "\n")
	if len(w) < 20 || !strings.Contains(pairOut, "SHA256:") || !strings.Contains(pairOut, "监听端口：") ||
		!strings.Contains(pairOut, "本机 IP：") || !strings.Contains(pairOut, "公网地址") {
		t.Fatalf("pair 输出 = %q", pairOut)
	}
	code, out, errb := e.call("status", "--user")
	if code != 0 {
		t.Fatalf("status code=%d err=%q", code, errb)
	}
	for _, s := range []string{w, testHash, testHash[:8]} {
		if strings.Contains(out, s) || strings.Contains(errb, s) {
			t.Fatalf("status 输出含敏感值 %q: %s", s, out)
		}
	}
	for _, s := range []string{"拨出", "监听地址：127.0.0.1:", "未连接", "已分配实例：0", "配对窗口", "协议版本 1"} {
		if !strings.Contains(out, s) {
			t.Fatalf("status 输出缺少 %q: %s", s, out)
		}
	}
	if strings.Contains(rd.stderr.String(), w) {
		t.Fatal("配对码进了守护进程日志")
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return s
}

func TestStatus_拨入运行中不含凭据(t *testing.T) {
	e := newEnv(t)
	conf, cred := dialInConf(t)
	e.save(conf)
	e.startDaemon()
	code, out, errb := e.call("status", "--user")
	if code != 0 || strings.Contains(out+errb, cred) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, "拨入") || !strings.Contains(out, conf.HubURL) || !strings.Contains(out, conf.HubPin) {
		t.Fatalf("status 输出 = %q", out)
	}
}

func TestStatus_守护进程未运行时读配置并提示(t *testing.T) {
	e := newEnv(t)
	conf, cred := dialInConf(t)
	e.save(conf)
	code, out, errb := e.call("status", "--user")
	if code != 1 || !strings.Contains(out+errb, "守护进程未运行") || strings.Contains(out+errb, cred) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	if !strings.Contains(out, conf.HubURL) || !strings.Contains(out, conf.HubPin) {
		t.Fatalf("应显示配置里的 hub 地址与指纹: %q", out)
	}
}

func TestStatus_未安装时提示(t *testing.T) {
	e := newEnv(t)
	if code, out, errb := e.call("status", "--user"); code != 1 || !strings.Contains(out+errb, "尚未安装") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
}

func TestPair_拨入模式报错(t *testing.T) {
	e := newEnv(t)
	conf, _ := dialInConf(t)
	e.save(conf)
	e.startDaemon()
	code, out, errb := e.call("pair", "--user")
	if code != 1 || out != "" || !strings.Contains(errb, "拨入模式请用 install --code 重新配对") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
}

func TestCLI_守护进程未运行时提示(t *testing.T) {
	e := newEnv(t)
	e.save(dialOutConf())
	var pin certs.SPKI
	for _, args := range [][]string{{"pair", "--user"}, {"repin", "--user", "--fp", pin.String()}} {
		code, out, errb := e.call(args...)
		if code != 1 || out != "" || !strings.Contains(errb, "守护进程未运行") {
			t.Fatalf("%v: code=%d out=%q err=%q", args, code, out, errb)
		}
	}
}

func TestRepin_指纹格式校验(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"repin", "--user"}, {"repin", "--user", "--fp", "SHA256:not-a-fp"}} {
		if code, _, errb := e.call(args...); code != 2 || !strings.Contains(errb, "指纹") {
			t.Fatalf("%v: code=%d err=%q", args, code, errb)
		}
	}
}

func TestRepin_经控制套接字更新钉扎(t *testing.T) {
	e := newEnv(t)
	conf, _ := dialInConf(t)
	e.save(conf)
	e.startDaemon()
	var pin certs.SPKI
	pin[0], pin[31] = 9, 9
	code, out, errb := e.call("repin", "--user", "--fp", pin.String())
	if code != 0 || !strings.Contains(out, pin.String()) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	got, err := config.Load(e.paths.Config, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if got.HubPin != pin.String() {
		t.Fatalf("配置里的指纹 = %s", got.HubPin)
	}
}

func TestCLI_多余参数为用法错误(t *testing.T) {
	e := newEnv(t)
	for _, cmd := range []string{"status", "pair", "repin", "run"} {
		if code, _, _ := e.call(cmd, "--user", "extra"); code != 2 {
			t.Fatalf("%s: code=%d", cmd, code)
		}
	}
}
