package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/agent/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

var t0 = time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)

const testManifest = `id: test-count
version: 1.0.0
api_version: 1
name: {zh: 计数, en: Count}
kind: source
runtime: builtin
runs_on: [agent]
interval: 60s
timeout: 30s
config_schema:
  - key: name
    type: string
    title: {zh: 名称, en: Name}
    required: true
  - key: n
    type: number
    title: {zh: 数字, en: Number}
    min: 1
    max: 10
  - key: token
    type: secret
    title: {zh: 令牌, en: Token}
outputs:
  - {key: v, type: number, title: {zh: 值, en: Value}}
widgets:
  - id: w
    name: {zh: 值, en: Value}
    sizes:
      1x1: {template: value, bind: {value: {item: v}}}
`

func parseManifest(t *testing.T, src string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(src))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return m
}

// call 是计数插件收到的一次调用。
type call struct {
	name string
	ctx  context.Context
	in   runtime.Input
}

// countSource 是只注入、不注册到全局的计数型测试插件：按配置 name 计数，
// 返回的 state 在输入 state 后追加 "|"+name；block 为真时等 gate 关闭或 ctx 结束。
type countSource struct {
	m     *manifest.Manifest
	block atomic.Bool
	// ignoreCancel 为真时阻塞调用只等 gate，不理会 ctx（模拟不响应取消、照常返回成功的插件）。
	ignoreCancel atomic.Bool
	gate         chan struct{}
	failWith     atomic.Pointer[string]
	// onCancel 在阻塞的调用因 ctx 结束而返回前调用（测试 Close 是否等待插件退出）。
	onCancel atomic.Pointer[func()]
	started  chan call
	finished atomic.Int64

	mu    sync.Mutex
	calls map[string]int
}

func newCountSource(t *testing.T) *countSource {
	return &countSource{
		m: parseManifest(t, testManifest), gate: make(chan struct{}),
		started: make(chan call, 1000), calls: map[string]int{},
	}
}

func (c *countSource) Manifest() *manifest.Manifest { return c.m }

func (c *countSource) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	defer c.finished.Add(1)
	name, _ := in.Config["name"].(string)
	c.mu.Lock()
	c.calls[name]++
	c.mu.Unlock()
	c.started <- call{name: name, ctx: ctx, in: in}
	if c.block.Load() && c.ignoreCancel.Load() {
		<-c.gate
	} else if c.block.Load() {
		select {
		case <-c.gate:
		case <-ctx.Done():
			if fn := c.onCancel.Load(); fn != nil {
				(*fn)()
			}
			return nil, ctx.Err()
		}
	}
	if msg := c.failWith.Load(); msg != nil {
		return nil, errors.New(*msg)
	}
	return &report.Report{Status: report.StatusOK, Summary: name, State: in.State + "|" + name}, nil
}

func (c *countSource) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[name]
}

// next 等下一次调用（真实时间上限 5 秒，只用于失败兜底）。
func (c *countSource) next(t *testing.T) call {
	t.Helper()
	select {
	case cl := <-c.started:
		return cl
	case <-time.After(5 * time.Second):
		t.Fatal("等待插件调用超时")
		return call{}
	}
}

// fakeSender 实现 Sender：校验并记录每条消息。
type fakeSender struct {
	t    *testing.T
	ch   chan agent.Message
	done chan struct{}

	mu   sync.Mutex
	all  []agent.Message
	once sync.Once
}

func newSender(t *testing.T) *fakeSender {
	return &fakeSender{t: t, ch: make(chan agent.Message, 1000), done: make(chan struct{})}
}

func (f *fakeSender) Send(m agent.Message) error {
	if err := m.Validate(); err != nil {
		f.t.Errorf("发出不合法的消息 %T: %v", m, err)
	}
	f.mu.Lock()
	f.all = append(f.all, m)
	f.mu.Unlock()
	f.ch <- m
	return nil
}

func (f *fakeSender) Done() <-chan struct{} { return f.done }
func (f *fakeSender) disconnect()           { f.once.Do(func() { close(f.done) }) }

func (f *fakeSender) messages() []agent.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Message(nil), f.all...)
}

// expect 等下一条类型为 T 的消息，跳过其他类型。
func expect[T agent.Message](t *testing.T, f *fakeSender) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-f.ch:
			if v, ok := m.(T); ok {
				return v
			}
		case <-deadline:
			var zero T
			t.Fatalf("等待 %T 超时", zero)
			return zero
		}
	}
}

// fixture 组装运行器：真实注册表（只注入测试插件）、临时 agent.db、假时钟。
type fixture struct {
	clk   *clock.Fake
	store *store.Store
	reg   *plugins.Registry
	r     *Runner
}

func newFixture(t *testing.T, jitter time.Duration, srcs ...runtime.Source) *fixture {
	t.Helper()
	return newFixtureAt(t, filepath.Join(t.TempDir(), "agent.db"), jitter, srcs...)
}

// newFixtureAt 用指定的库文件组装运行器；同一路径再调用一次即模拟进程重启。
func newFixtureAt(t *testing.T, dbPath string, jitter time.Duration, srcs ...runtime.Source) *fixture {
	t.Helper()
	clk := clock.NewFake(t0)
	st, err := store.Open(dbPath, store.WithClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := plugins.New(plugins.Config{Dir: filepath.Join(t.TempDir(), "plugins"), Builtins: srcs, Clock: clk, Log: log})
	reg.Scan()
	r := New(Config{
		Registry: reg, Store: st, Clock: clk, Log: log,
		Jitter: func(time.Duration) time.Duration { return jitter },
	})
	return &fixture{clk: clk, store: st, reg: reg, r: r}
}

// attach 绑定一个替身会话，测试结束时关闭。
func (f *fixture) attach(t *testing.T) (*Binding, *fakeSender) {
	t.Helper()
	s := newSender(t)
	b := f.r.Attach(s)
	t.Cleanup(b.Close)
	return b, s
}

func inst(id, name, hash string) agent.AssignedInstance {
	return agent.AssignedInstance{
		ID: id, PluginID: "test-count", Config: map[string]any{"name": name},
		IntervalMs: 60000, TimeoutMs: 30000, ConfigHash: hash, ContentHash: "c:" + hash,
	}
}

func state(t *testing.T, st *store.Store, id string) (string, bool) {
	t.Helper()
	v, err := st.State(context.Background(), id)
	if err != nil {
		return "", false
	}
	return v, true
}

// waitUntil 轮询 cond，真实时间 5 秒内不成立则失败。
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("等待条件超时")
		}
		time.Sleep(time.Millisecond)
	}
}
