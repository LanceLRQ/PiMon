package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentconfig "github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// e2eProbeManifest 是只注入 agent、不注册到全局的计数型测试插件（hub 经 hello 得知它）；带一个密钥字段。
const e2eProbeManifest = `id: e2e-probe
version: 1.0.0
api_version: 1
name: {zh: 探针, en: Probe}
kind: source
runtime: builtin
runs_on: [agent]
interval: 60s
timeout: 30s
config_schema:
  - key: label
    type: string
    title: {zh: 标签, en: Label}
  - key: token
    type: secret
    title: {zh: 令牌, en: Token}
outputs:
  - {key: v, type: number, title: {zh: 值, en: Value}}
`

// e2eSecret 是实例的密钥值：跑完后 agent 数据目录下任何文件都不得出现它。
const e2eSecret = "e2e-SECRET-7f3a9c41d2b8"

// e2eProbe 统计调用次数，记录收到的密钥，并返回带序号的摘要与非空 state（让 agent.db 真正写入）。
type e2eProbe struct {
	m      *manifest.Manifest
	calls  atomic.Int64
	mu     sync.Mutex
	tokens []string
}

func newE2EProbe(t *testing.T) *e2eProbe {
	t.Helper()
	m, err := manifest.Parse([]byte(e2eProbeManifest))
	if err != nil {
		t.Fatal(err)
	}
	return &e2eProbe{m: m}
}

func (p *e2eProbe) Manifest() *manifest.Manifest { return p.m }

func (p *e2eProbe) Collect(_ context.Context, in runtime.Input) (*report.Report, error) {
	n := p.calls.Add(1)
	p.mu.Lock()
	p.tokens = append(p.tokens, in.Secrets["token"])
	p.mu.Unlock()
	return &report.Report{Status: report.StatusOK, Summary: fmt.Sprintf("probe-%d", n), State: fmt.Sprintf("seen=%d", n)}, nil
}

func (p *e2eProbe) sawSecret() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.tokens {
		if s == e2eSecret {
			return true
		}
	}
	return false
}

// connCounter 是透明 TCP 转发：统计 agent 拨向 hub 的连接次数（吊销后据此证明不再重连）。
type connCounter struct {
	ln    net.Listener
	n     atomic.Int64
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns []net.Conn
}

func startConnCounter(t *testing.T, target string) *connCounter {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cc := &connCounter{ln: ln}
	cc.wg.Add(1)
	go func() {
		defer cc.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			cc.n.Add(1)
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			cc.mu.Lock()
			cc.conns = append(cc.conns, c, up)
			cc.mu.Unlock()
			cc.wg.Add(2)
			go func() { defer cc.wg.Done(); _, _ = io.Copy(up, c); _ = up.Close() }()
			go func() { defer cc.wg.Done(); _, _ = io.Copy(c, up); _ = c.Close() }()
		}
	}()
	t.Cleanup(cc.close)
	return cc
}

func (cc *connCounter) addr() string { return cc.ln.Addr().String() }

func (cc *connCounter) close() {
	_ = cc.ln.Close()
	cc.mu.Lock()
	for _, c := range cc.conns {
		_ = c.Close()
	}
	cc.mu.Unlock()
	cc.wg.Wait()
}

// e2eAgent 是同一进程内运行的真实 agent 守护进程（用户模式路径，根在临时目录）。
type e2eAgent struct {
	t      *testing.T
	home   string
	paths  agentconfig.Paths
	clk    *clock.Fake
	probe  *e2eProbe
	d      *daemon.Daemon
	cancel context.CancelFunc
	errc   chan error

	mu   sync.Mutex
	addr string
}

func newE2EAgent(t *testing.T, clk *clock.Fake, probe *e2eProbe, home string) *e2eAgent {
	t.Helper()
	paths, err := agentconfig.Resolve(agentconfig.Env{GOOS: goruntime.GOOS, EUID: os.Geteuid(), Home: home, User: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentconfig.EnsureDataDir(paths); err != nil {
		t.Fatal(err)
	}
	return &e2eAgent{t: t, home: home, paths: paths, clk: clk, probe: probe}
}

// start 新建并运行守护进程（每次调用都是一个新进程的等价物：新日志环、新 Daemon，磁盘状态沿用）。
func (ea *e2eAgent) start() {
	ea.t.Helper()
	ring := logging.NewRing(daemon.RingCapacity)
	ea.mu.Lock()
	ea.addr = ""
	ea.mu.Unlock()
	ea.d = daemon.New(daemon.Config{
		Paths: ea.paths, Clock: ea.clk, Ring: ring,
		Log:      logging.NewWithRing(io.Discard, slog.LevelDebug, ring),
		Builtins: []runtime.Source{ea.probe},
		Jitter:   func(time.Duration) time.Duration { return 0 },
		Rand:     func() float64 { return 0.5 },
		Listen:   "127.0.0.1:0",
		OnListen: func(a string) { ea.mu.Lock(); ea.addr = a; ea.mu.Unlock() },
		Hostname: func() (string, error) { return "e2e-agent", nil },
		MDNS:     func(context.Context, string) ([]netip.Addr, error) { return nil, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	ea.cancel, ea.errc = cancel, make(chan error, 1)
	d := ea.d
	go func() { ea.errc <- d.Run(ctx) }()
}

// stop 取消守护进程并等 Run 返回（可重复调用）。
func (ea *e2eAgent) stop() {
	if ea.cancel == nil {
		return
	}
	ea.cancel()
	ea.cancel = nil
	select {
	case err := <-ea.errc:
		if err != nil {
			ea.t.Errorf("agent Run 返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		ea.t.Fatal("agent Run 未在取消后返回")
	}
}

// running 报告 Run 是否仍未返回。
func (ea *e2eAgent) running() bool {
	select {
	case err := <-ea.errc:
		ea.errc <- err
		return false
	default:
		return true
	}
}

func (ea *e2eAgent) listenAddr() string {
	ea.t.Helper()
	waitUntil(ea.t, "agent 拨出监听就绪", func() bool { ea.mu.Lock(); defer ea.mu.Unlock(); return ea.addr != "" })
	ea.mu.Lock()
	defer ea.mu.Unlock()
	return ea.addr
}

// assertNoSecretOnDisk 扫描 agent 根目录下全部文件（配置、agent.db 及 -wal/-shm、插件目录等），断言不含密钥值。
func (ea *e2eAgent) assertNoSecretOnDisk() {
	ea.t.Helper()
	var files []string
	sawState := false
	err := filepath.WalkDir(ea.home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, filepath.Base(p))
		if bytes.Contains(b, []byte(e2eSecret)) {
			ea.t.Errorf("agent 磁盘文件 %s 含有插件密钥", p)
		}
		sawState = sawState || bytes.Contains(b, []byte("seen="))
		return nil
	})
	if err != nil {
		ea.t.Fatal(err)
	}
	joined := strings.Join(files, ",")
	for _, want := range []string{filepath.Base(ea.paths.Config), filepath.Base(ea.paths.DB)} {
		if !strings.Contains(joined, want) {
			ea.t.Fatalf("扫描范围应包含 %s，实际文件: %s", want, joined)
		}
	}
	if !sawState { // 证明插件私有 state 确实落在被扫描的文件里（agent.db 或其 -wal）
		ea.t.Fatalf("扫描的文件中应能找到插件 state，实际文件: %s", joined)
	}
}

// e2eHub 是运行中的真实 hub 与已登录的管理端客户端。
type e2eHub struct {
	a         *App
	c         *http.Client
	base      string
	agentAddr string
	clk       *clock.Fake
}

func startE2EHub(t *testing.T, clk *clock.Fake) *e2eHub {
	t.Helper()
	cfg := testConfig(t)
	cfg.AgentAddr = "127.0.0.1:0"
	addrCh := make(chan string, 1)
	a := openApp(t, cfg, WithClock(clk), WithOnAgentListen(func(addr string) { addrCh <- addr }))
	c, base := setupAdmin(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve 退出超时")
		}
	})
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-done:
		t.Fatalf("Serve 提前结束: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 agent 通道监听超时")
	}
	return &e2eHub{a: a, c: c, base: base, agentAddr: addr, clk: clk}
}

func (h *e2eHub) presence(id string) string {
	st, _ := h.a.presence.State(id)
	return st
}

// createProbeInstance 在设备上创建探针实例（带密钥），推进 1 秒合并窗口，等 agent 运行并回传、hub 入库。
func (h *e2eHub) createProbeInstance(t *testing.T, devID string, probe *e2eProbe) string {
	t.Helper()
	before := probe.calls.Load()
	resp, data := call(t, h.c, h.base, "POST", "/api/instances", map[string]any{
		"plugin_id": "e2e-probe", "name": "探针 " + devID, "runs_on": devID,
		"config": map[string]any{"label": "x", "token": e2eSecret},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建远程实例 = %d %s", resp.StatusCode, data)
	}
	if bytes.Contains(data, []byte(e2eSecret)) {
		t.Fatalf("创建实例的响应不应回显密钥: %s", data)
	}
	var inst model.InstanceDetail
	if err := json.Unmarshal(data, &inst); err != nil {
		t.Fatal(err)
	}
	if inst.RunsOn != devID {
		t.Fatalf("实例 runs_on = %q，应为 %q", inst.RunsOn, devID)
	}
	h.clk.Advance(time.Second) // 下发合并窗口
	waitUntil(t, "agent 收到 assign 并运行探针", func() bool { return probe.calls.Load() > before })
	if !probe.sawSecret() {
		t.Fatal("探针应经 assign 收到解密后的密钥")
	}
	waitUntil(t, "hub 侧实例有报告", func() bool {
		d, err := h.a.instances.Get(context.Background(), inst.ID)
		return err == nil && d.Report != nil && strings.HasPrefix(d.Report.Summary, "probe-")
	})
	return inst.ID
}

// saveAndTest 「保存并测试」：经通道 run_now，返回的报告来自 agent 上的这次运行。
func (h *e2eHub) saveAndTest(t *testing.T, instID string, probe *e2eProbe) {
	t.Helper()
	before := probe.calls.Load()
	resp, data := call(t, h.c, h.base, "POST", "/api/instances/"+instID+"/run", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存并测试 = %d %s", resp.StatusCode, data)
	}
	var rr model.InstanceRunResult
	if err := json.Unmarshal(data, &rr); err != nil {
		t.Fatal(err)
	}
	if after := probe.calls.Load(); after <= before {
		t.Fatalf("保存并测试应在 agent 上运行一次探针（调用 %d → %d）", before, after)
	}
	if rr.Report == nil || rr.Report.Status != report.StatusOK || !strings.HasPrefix(rr.Report.Summary, "probe-") {
		t.Fatalf("保存并测试的报告 = %+v", rr.Report)
	}
	if rr.Report.State != "" {
		t.Fatalf("agent 实例的 state 不应回传到 hub: %q", rr.Report.State)
	}
}

func (h *e2eHub) pluginIDs(t *testing.T, devID string) []string {
	t.Helper()
	resp, data := call(t, h.c, h.base, "GET", "/api/plugins?machine="+devID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("设备插件列表 = %d %s", resp.StatusCode, data)
	}
	var list struct {
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("解析插件列表: %v %s", err, data)
	}
	ids := make([]string, 0, len(list.Plugins))
	for _, p := range list.Plugins {
		ids = append(ids, p.ID)
	}
	return ids
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// writeAgentExecPlugin 在 agent 插件目录写入 runs_on 含 agent 的 exec 插件（目录与 run 0755、清单 0644，属主为当前用户）。
func writeAgentExecPlugin(t *testing.T, dir, id string) {
	t.Helper()
	d := filepath.Join(dir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	y := "id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: " + id +
		"\nkind: source\nruntime: exec\nruns_on: [agent]\noutputs:\n  - {key: v, type: number, title: V}\n"
	if err := os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\necho '{\"status\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, mode := range map[string]os.FileMode{d: 0o755, filepath.Join(d, "run"): 0o755, filepath.Join(d, "plugin.yaml"): 0o644} {
		if err := os.Chmod(p, mode); err != nil { // 不受 umask 影响
			t.Fatal(err)
		}
	}
}

// 计划 C8：真实 hub 与真实 agent 守护进程在同一进程、同一假时钟下走通拨入与拨出两个方向：
// 配对 → 在线 → 下发与运行 → 报告入库 → 保存并测试 → 拉日志 → exec 插件出现 → 断开满 30/60 秒离线 →
// 重启恢复 → 吊销后不再重连；拨出配对后同样下发与回传；两台 agent 的磁盘上都不出现插件密钥。
func TestAgent端到端拨入与拨出全流程(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC))
	h := startE2EHub(t, clk)
	bg := context.Background()

	t.Run("拨入", func(t *testing.T) {
		probe := newE2EProbe(t)
		ag := newE2EAgent(t, clk, probe, t.TempDir())
		t.Cleanup(ag.stop)
		fwd := startConnCounter(t, h.agentAddr)
		hubURL := "wss://" + fwd.addr()

		// 网页生成配对码 → agent 一次性配对（凭据落盘、配对会话上当场 hello）。
		res := pairingCode(t, h.c, h.base, "/api/devices/pairing", map[string]any{"name": "nas-e2e", "role": "nas"})
		pin, err := certs.ParseSPKI(res.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		pr, err := link.PairDialIn(bg, link.PairRequest{
			HubURL: hubURL, Pin: pin, Code: res.Code, Name: "nas-e2e", Clock: clk,
			Log:   slog.New(slog.DiscardHandler),
			Hello: func() agent.Hello { return link.NewHello("e2e-agent", nil) },
			Save: func(ok agent.PairOK) error {
				return agentconfig.Save(ag.paths.Config, agentconfig.Config{
					Mode: agentconfig.ModeDialIn, HubURL: hubURL, HubPin: pin.String(),
					Credential: agentconfig.Credential(ok.Credential), HubHostname: ok.ListenerInfo.Hostname,
				})
			},
		})
		if err != nil {
			t.Fatalf("拨入配对: %v", err)
		}
		devID := pr.DeviceID
		if dev, err := h.a.devices.Get(bg, devID); err != nil || dev.Name != "nas-e2e" || dev.Direction != model.DeviceDialIn {
			t.Fatalf("配对后设备 = %+v %v", dev, err)
		}

		// 守护进程用凭据连上 → 设备在线，hello 带来探针插件。
		ag.start()
		waitUntil(t, "设备 online", func() bool { return h.presence(devID) == model.PresenceOnline })
		waitUntil(t, "守护进程已就绪", func() bool { return ag.d.Status().Connected })
		if p := devicePresence(t, h.c, h.base, devID); p != model.PresenceOnline {
			t.Fatalf("设备详情应为 online，得到 %q", p)
		}
		if ids := h.pluginIDs(t, devID); !contains(ids, "e2e-probe") {
			t.Fatalf("设备可用插件应含 e2e-probe: %v", ids)
		}

		// 在设备上创建实例 → 1 秒合并窗口后下发 → agent 运行 → hub 入库；保存并测试经通道返回。
		instID := h.createProbeInstance(t, devID, probe)
		waitUntil(t, "agent 状态显示已分配 1 个实例", func() bool { return ag.d.Status().Instances == 1 })
		h.saveAndTest(t, instID, probe)

		// 拉日志：agent 日志环经通道返回，且不含密钥。
		resp, data := call(t, h.c, h.base, "GET", "/api/devices/"+devID+"/logs", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("拉日志 = %d %s", resp.StatusCode, data)
		}
		var logs model.LogList
		if err := json.Unmarshal(data, &logs); err != nil {
			t.Fatal(err)
		}
		if len(logs.Entries) == 0 {
			t.Fatalf("设备日志应有条目: %s", data)
		}
		if bytes.Contains(data, []byte(e2eSecret)) {
			t.Fatal("设备日志不应含插件密钥")
		}

		// 复制 exec 插件到 agent 插件目录 → plugins_changed → hub 上该设备的可用插件出现新插件。
		writeAgentExecPlugin(t, ag.paths.PluginDir, "e2e-exec")
		const maxRounds = 30
		for round := 0; !contains(h.pluginIDs(t, devID), "e2e-exec"); round++ {
			if round >= maxRounds {
				t.Fatalf("推进 %d 轮后 hub 上设备插件列表仍未出现 e2e-exec: %v", maxRounds, h.pluginIDs(t, devID))
			}
			clk.Advance(200 * time.Millisecond) // 推进插件目录防抖
			time.Sleep(10 * time.Millisecond)
		}

		// 关闭 agent：立即 disconnected，满 30 秒 offline，60 秒仍 offline。
		ag.stop()
		waitUntil(t, "断开", func() bool { return h.presence(devID) == model.PresenceDisconnected })
		clk.Advance(devices.OfflineAfter - time.Second)
		if p := devicePresence(t, h.c, h.base, devID); p != model.PresenceDisconnected {
			t.Fatalf("断开 29 秒应为 disconnected，得到 %q", p)
		}
		clk.Advance(time.Second)
		waitUntil(t, "满 30 秒离线", func() bool { return h.presence(devID) == model.PresenceOffline })
		clk.Advance(30 * time.Second)
		if p := devicePresence(t, h.c, h.base, devID); p != model.PresenceOffline {
			t.Fatalf("断开 60 秒应为 offline，得到 %q", p)
		}

		// 重启 agent：自动用凭据重连恢复在线，重新下发后再次运行并回传。
		before, err := h.a.instances.Get(bg, instID)
		if err != nil || before.Report == nil {
			t.Fatalf("重启前读取实例失败: report=%v err=%v", before.Report, err)
		}
		calls := probe.calls.Load()
		ag.start()
		waitUntil(t, "重启后 online", func() bool { return h.presence(devID) == model.PresenceOnline })
		waitUntil(t, "重启后重新下发并运行", func() bool { return probe.calls.Load() > calls })
		waitUntil(t, "重启后新报告入库", func() bool {
			d, err := h.a.instances.Get(bg, instID)
			return err == nil && d.Report != nil && d.Report.Summary != before.Report.Summary
		})

		// 吊销：hub 断开会话（bye revoked）→ 守护进程状态为已吊销、清除凭据、不退出，且不再拨号。
		if resp, data := call(t, h.c, h.base, "DELETE", "/api/devices/"+devID, nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("吊销 = %d %s", resp.StatusCode, data)
		}
		waitUntil(t, "守护进程状态为已吊销", func() bool { return ag.d.Status().State == daemon.StateRevoked })
		dials := fwd.n.Load()
		if dials < 3 { // 配对、首次启动、重启各经转发拨号一次：计数确实覆盖了 agent 的拨号路径
			t.Fatalf("经转发的拨号次数 = %d，应至少 3", dials)
		}
		for range 60 {
			clk.Advance(time.Second)
		}
		time.Sleep(100 * time.Millisecond) // 给可能的重连留出真实时间
		if n := fwd.n.Load(); n != dials {
			t.Fatalf("吊销后不应再拨号 hub：连接数 %d → %d", dials, n)
		}
		if !ag.running() {
			t.Fatal("吊销后守护进程不应退出")
		}
		if st := ag.d.Status(); st.State != daemon.StateRevoked || st.Connected {
			t.Fatalf("吊销后状态 = %+v", st)
		}
		if c, err := agentconfig.Load(ag.paths.Config, os.Geteuid()); err != nil || c.Credential != "" {
			t.Fatalf("吊销后磁盘上的凭据应已清除: err=%v", err)
		}

		ag.stop()
		ag.assertNoSecretOnDisk()
	})

	t.Run("拨出", func(t *testing.T) {
		probe := newE2EProbe(t)
		ag := newE2EAgent(t, clk, probe, t.TempDir())
		t.Cleanup(ag.stop)
		if err := agentconfig.Save(ag.paths.Config, agentconfig.Config{Mode: agentconfig.ModeDialOut, Listen: "127.0.0.1:0"}); err != nil {
			t.Fatal(err)
		}
		ag.start()
		addr := ag.listenAddr()

		// 打开配对窗口（监听就绪与配置读取之间可能有短暂间隔，轮询到不再是「监听尚未就绪」）。
		var w daemon.PairWindow
		waitUntil(t, "打开配对窗口", func() bool {
			var err error
			w, err = ag.d.OpenPairWindow(bg)
			if err != nil && !errors.Is(err, daemon.ErrNotListening) {
				t.Fatalf("打开配对窗口: %v", err)
			}
			return err == nil
		})

		resp, data := call(t, h.c, h.base, "POST", "/api/devices/dial-out", map[string]any{
			"address": "wss://" + addr, "fingerprint": w.Fingerprint, "code": w.Code, "name": "vps-e2e",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("拨出配对 = %d %s", resp.StatusCode, data)
		}
		var dev model.Device
		if err := json.Unmarshal(data, &dev); err != nil {
			t.Fatal(err)
		}
		if dev.Direction != model.DeviceDialOut || dev.Presence != model.PresenceOnline {
			t.Fatalf("拨出设备 = %+v", dev)
		}
		waitUntil(t, "agent 已就绪", func() bool { return ag.d.Status().Connected })
		if c, err := agentconfig.Load(ag.paths.Config, os.Geteuid()); err != nil || agent.DeviceIDFromHash(c.CredentialHash) != dev.ID {
			t.Fatalf("agent 应只落盘与设备对应的凭据哈希: %v", err)
		}
		if ids := h.pluginIDs(t, dev.ID); !contains(ids, "e2e-probe") {
			t.Fatalf("拨出设备可用插件应含 e2e-probe: %v", ids)
		}

		instID := h.createProbeInstance(t, dev.ID, probe)
		h.saveAndTest(t, instID, probe)

		ag.stop()
		waitUntil(t, "拨出设备断开", func() bool { return h.presence(dev.ID) != model.PresenceOnline })
		ag.assertNoSecretOnDisk()
	})
}
