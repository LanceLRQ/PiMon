package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

const probeManifest = `id: %ID%
version: %VER%
api_version: 1
name: Probe
kind: source
runtime: %RT%
runs_on: [hub]
interval: 60s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, required: true}
  - {key: api_key, type: secret, title: Key, required: true}
  - {key: proxy, type: proxy, title: Proxy}
outputs:
  - {key: temp, type: number, title: Temp}
`

const plainManifest = `id: %ID%
version: 1.0.0
api_version: 1
name: Plain
kind: source
runtime: %RT%
runs_on: [hub]
interval: 60s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, required: true}
outputs:
  - {key: temp, type: number, title: Temp}
`

// floorManifest 声明 min_interval: 30s，用来测试实例刷新间隔的下限。
const floorManifest = `id: floor
version: 1.0.0
api_version: 1
name: Floor
kind: source
runtime: builtin
runs_on: [hub]
interval: 60s
min_interval: 30s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, required: true}
outputs:
  - {key: temp, type: number, title: Temp}
`

// notifierManifest 是通知渠道插件，不能建数据源实例。
const notifierManifest = `id: notif
version: 1.0.0
api_version: 1
name: Notif
kind: notifier
runtime: builtin
runs_on: [hub]
`

func parseManifest(t *testing.T, tpl, id, ver, rt string) *manifest.Manifest {
	t.Helper()
	y := strings.NewReplacer("%ID%", id, "%VER%", ver, "%RT%", rt).Replace(tpl)
	m, err := manifest.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// probeSource 是可编程的内置插件替身：记录每次调用的输入。
type probeSource struct {
	m *manifest.Manifest

	mu     sync.Mutex
	inputs []runtime.Input
	fn     func(ctx context.Context, in runtime.Input) (*report.Report, error)
}

func (s *probeSource) Manifest() *manifest.Manifest { return s.m }

func (s *probeSource) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	s.mu.Lock()
	s.inputs = append(s.inputs, in)
	n := len(s.inputs)
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return okReport(fmt.Sprint(in.Config["host"]), n), nil
}

func okReport(host string, n int) *report.Report {
	v := 21.5
	return &report.Report{
		Status: report.StatusOK, Summary: "host=" + host,
		Items: []report.Item{{Key: "temp", Type: report.TypeNumber, Value: &v}},
		State: fmt.Sprintf("s%d", n),
	}
}

func (s *probeSource) setFn(fn func(ctx context.Context, in runtime.Input) (*report.Report, error)) {
	s.mu.Lock()
	s.fn = fn
	s.mu.Unlock()
}

func (s *probeSource) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inputs)
}

func (s *probeSource) last() runtime.Input {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputs[len(s.inputs)-1]
}

// streamSource 额外实现 Streamer：每次启动 emit 一份报告后阻塞到 ctx 结束。
type streamSource struct {
	*probeSource
	mu      sync.Mutex
	started int
}

func (s *streamSource) Run(ctx context.Context, in runtime.Input, emit func(*report.Report)) error {
	s.mu.Lock()
	s.started++
	s.mu.Unlock()
	emit(okReport("stream", 1))
	<-ctx.Done()
	return ctx.Err()
}

func (s *streamSource) startedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

type fakeProxies struct {
	mu    sync.Mutex
	known map[string]*proxy.Proxy
}

func (f *fakeProxies) Resolve(_ context.Context, id string) (*proxy.Proxy, error) {
	if id == "" || strings.EqualFold(id, proxy.DirectValue) {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.known[id]
	if !ok {
		return nil, proxies.ErrNotFound
	}
	return p, nil
}

type recHistory struct {
	mu   sync.Mutex
	recs []string
}

func (h *recHistory) Record(id string, _ time.Time, rep *report.Report) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, id+"|"+rep.Summary)
}

func (h *recHistory) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.recs)
}

type fx struct {
	t       *testing.T
	dir     string
	plugDir string
	db      *store.DB
	box     *secret.Box
	clk     *clock.Fake
	reg     *plugins.Registry
	svc     *Service
	probe   *probeSource
	plain   *probeSource
	stream  *streamSource
	floor   *probeSource
	px      *fakeProxies
	hist    *recHistory
}

func newFx(t *testing.T) *fx {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fx{t: t, dir: dir, db: db, box: box, clk: clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
	f.probe = &probeSource{m: parseManifest(t, probeManifest, "probe", "1.0.0", "builtin")}
	f.plain = &probeSource{m: parseManifest(t, plainManifest, "plain", "", "builtin")}
	f.stream = &streamSource{probeSource: &probeSource{m: parseManifest(t, plainManifest, "streamer", "", "builtin")}}
	f.floor = &probeSource{m: parseManifest(t, floorManifest, "floor", "", "builtin")}
	notif := &probeSource{m: parseManifest(t, notifierManifest, "notif", "", "builtin")}
	f.plugDir = filepath.Join(dir, "plugins")
	if err := os.MkdirAll(f.plugDir, 0o750); err != nil {
		t.Fatal(err)
	}
	f.reg = plugins.New(plugins.Config{
		Dir: f.plugDir, DB: db, Clock: f.clk,
		Builtins: []runtime.Source{f.probe, f.plain, f.stream, f.floor, notif},
	})
	if _, err := f.reg.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.px = &fakeProxies{known: map[string]*proxy.Proxy{}}
	f.hist = &recHistory{}
	f.svc = f.newService()
	return f
}

func (f *fx) newService() *Service {
	svc := New(Config{
		DB: f.db, Box: f.box, Clock: f.clk, Plugins: f.reg, History: f.hist,
		Jitter: func(time.Duration) time.Duration { return 0 },
	})
	svc.UseProxies(f.px)
	return svc
}

// start 启动调度器与落盘循环，测试结束时停止并等待。
func (f *fx) start() {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := f.svc.Start(ctx)
	f.t.Cleanup(func() {
		cancel()
		<-done
	})
}

// putExec 在插件目录下放一个 exec 插件。
func (f *fx) putExec(id string) {
	f.t.Helper()
	d := filepath.Join(f.plugDir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		f.t.Fatal(err)
	}
	must(f.t, os.Chmod(d, 0o755))
	y := parseManifestYAML(probeManifest, id, "1.0.0", "exec")
	must(f.t, os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(y), 0o644))
	must(f.t, os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\necho '{\"status\":\"ok\"}'\n"), 0o755))
	must(f.t, os.Chmod(filepath.Join(d, "run"), 0o755))
}

func parseManifestYAML(tpl, id, ver, rt string) string {
	return strings.NewReplacer("%ID%", id, "%VER%", ver, "%RT%", rt).Replace(tpl)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fx) rescan() {
	f.t.Helper()
	if _, err := f.reg.Scan(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (f *fx) create(plugin, name string, cfg map[string]any) model.InstanceDetail {
	f.t.Helper()
	d, err := f.svc.Create(context.Background(), model.InstanceInput{PluginID: plugin, Name: name, Config: cfg})
	if err != nil {
		f.t.Fatalf("Create: %v", err)
	}
	return d
}

func probeCfg(host string) map[string]any {
	return map[string]any{"host": host, "api_key": "s3cret-key"}
}

func (f *fx) stateRows() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM instance_state`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

var bg = context.Background()

func TestSecretRoundTrip(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "主机 A", probeCfg("a.example"))

	// GET 不回显明文，显示 {"set": true}
	got, err := f.svc.Get(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "s3cret-key") {
		t.Fatalf("详情回显了密钥明文: %s", raw)
	}
	if m, ok := got.Config["api_key"].(map[string]any); !ok || m["set"] != true {
		t.Fatalf("api_key 应回显 {set:true}: %v", got.Config["api_key"])
	}

	// 库里是密文：config_json 不含密钥，secrets_enc 不含明文
	var cfgJSON, enc string
	must(t, f.db.QueryRow(`SELECT config_json, secrets_enc FROM plugin_instances WHERE id=?`, d.ID).Scan(&cfgJSON, &enc))
	if strings.Contains(cfgJSON, "s3cret-key") || strings.Contains(enc, "s3cret-key") || enc == "" {
		t.Fatalf("库内存储不正确: config=%s enc=%q", cfgJSON, enc)
	}

	// PUT 留空（或回显标记）保留原值
	for _, keep := range []any{nil, "", map[string]any{"set": true}, "MISSING"} {
		cfg := map[string]any{"host": "b.example"}
		if keep != "MISSING" {
			cfg["api_key"] = keep
		}
		if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "主机 A", Config: cfg}); err != nil {
			t.Fatalf("Update(keep=%v): %v", keep, err)
		}
		if _, err := f.svc.Run(bg, d.ID); err != nil {
			t.Fatal(err)
		}
		in := f.probe.last()
		if in.Secrets["api_key"] != "s3cret-key" || in.Config["host"] != "b.example" {
			t.Fatalf("keep=%v 后密钥未保留: secrets=%v config=%v", keep, in.Secrets, in.Config)
		}
		if _, leaked := in.Config["api_key"]; leaked {
			t.Fatal("普通配置不应含密钥")
		}
	}

	// 给新值则更新
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "主机 A", Config: map[string]any{"host": "b.example", "api_key": "new-key"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.probe.last().Secrets["api_key"] != "new-key" {
		t.Fatalf("新密钥未生效: %v", f.probe.last().Secrets)
	}
}

func TestCreateValidation(t *testing.T) {
	f := newFx(t)
	_, err := f.svc.Create(bg, model.InstanceInput{PluginID: "nope", Name: "x", Config: map[string]any{}})
	if !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("未知插件: %v", err)
	}
	var fe model.FieldErrors
	_, err = f.svc.Create(bg, model.InstanceInput{PluginID: "probe", Name: " ", Config: map[string]any{"host": "h"}, IntervalSeconds: 2})
	if !errors.As(err, &fe) {
		t.Fatalf("应为字段错误: %v", err)
	}
	if fe["name"] != model.FieldRequired || fe["api_key"] != model.FieldRequired || fe["interval_seconds"] != model.FieldOutOfRange {
		t.Fatalf("字段错误 = %v", fe)
	}
	_, err = f.svc.Create(bg, model.InstanceInput{PluginID: "probe", Name: "x", Config: map[string]any{"host": "h", "api_key": "k", "proxy": "ghost"}})
	if !errors.As(err, &fe) || fe["proxy"] != model.FieldInvalid {
		t.Fatalf("引用不存在的代理应报 proxy invalid: %v", err)
	}
	if _, err := f.svc.Get(bg, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get 不存在: %v", err)
	}
	// 更新时不能换插件
	d := f.create("probe", "a", probeCfg("h"))
	_, err = f.svc.Update(bg, d.ID, model.InstanceInput{PluginID: "plain", Name: "a", Config: map[string]any{"host": "h"}})
	if !errors.As(err, &fe) || fe["plugin_id"] != model.FieldInvalid {
		t.Fatalf("换插件应被拒: %v", err)
	}
}

func TestStateFlushAndRestore(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	f.start()
	waitFor(t, "落盘循环就绪", func() bool { return f.clk.Waiters() >= 1 })

	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.stateRows(); n != 0 {
		t.Fatalf("未到 30 秒不应落盘，行数 %d", n)
	}
	f.clk.Advance(30 * time.Second)
	waitFor(t, "30 秒后落盘", func() bool { return f.stateRows() == 1 })

	// 重启：新服务实例从库恢复
	svc2 := f.newService()
	must(t, svc2.Load(bg))
	got, err := svc2.Get(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := f.svc.Get(bg, d.ID)
	if got.Report == nil || got.Report.Summary != "host=a" || got.Report.State != "" {
		t.Fatalf("恢复的报告 = %+v", got.Report)
	}
	if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(*want.LastSuccessAt) || got.DisplayState != "ok" {
		t.Fatalf("恢复的状态 = %+v want %+v", got.Instance, want.Instance)
	}
	// 私有 state 不经 API 返回，但恢复后下次运行仍会传回插件
	saved := fmt.Sprintf("s%d", f.probe.calls())
	if _, err := svc2.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.probe.last().State != saved {
		t.Fatalf("恢复后的私有 state = %q，期望 %q", f.probe.last().State, saved)
	}
}

func TestStateFinalFlushOnStop(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	ctx, cancel := context.WithCancel(bg)
	done := f.svc.Start(ctx)
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if n := f.stateRows(); n != 1 {
		t.Fatalf("停止时应做最后一次落盘，行数 %d", n)
	}
}

func TestFlushFailureCounted(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.svc.WriteErrors() != 0 {
		t.Fatal("初始计数应为 0")
	}
	if _, err := f.db.Exec(`DROP TABLE instance_state`); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Flush(bg); err == nil {
		t.Fatal("落盘应失败")
	}
	if f.svc.WriteErrors() != 1 {
		t.Fatalf("WriteErrors = %d，期望 1", f.svc.WriteErrors())
	}
}

func TestRunSuccessFailureTimeout(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))

	res, err := f.svc.Run(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Report == nil || res.Report.Summary != "host=a" || res.Instance.DisplayState != "ok" || res.Instance.LastSuccessAt == nil {
		t.Fatalf("成功结果 = %+v", res)
	}
	if res.Instance.Summary != "host=a" {
		t.Fatalf("摘要 = %q", res.Instance.Summary)
	}

	// 失败：错误分类为 ErrFailed；旧值保留并标记过期；错误文字已脱敏
	f.probe.setFn(func(context.Context, runtime.Input) (*report.Report, error) {
		return nil, errors.New("连接 https://x/?key=s3cret-key 被拒绝")
	})
	_, err = f.svc.Run(bg, d.ID)
	if !errors.Is(err, runtime.ErrFailed) {
		t.Fatalf("失败应归为 ErrFailed: %v", err)
	}
	if strings.Contains(err.Error(), "s3cret-key") {
		t.Fatalf("错误文字含密钥: %v", err)
	}
	got, _ := f.svc.Get(bg, d.ID)
	if got.DisplayState != "error" || got.Failures != 1 || got.LastError == "" || strings.Contains(got.LastError, "s3cret-key") {
		t.Fatalf("失败后状态 = %+v", got.Instance)
	}
	if got.Report == nil || !got.Report.Stale || got.Report.Summary != "host=a" || !got.Report.Items[0].Stale {
		t.Fatalf("旧值应保留并标记过期: %+v", got.Report)
	}

	// 超时
	f.probe.setFn(func(context.Context, runtime.Input) (*report.Report, error) {
		return nil, fmt.Errorf("慢: %w", context.DeadlineExceeded)
	})
	_, err = f.svc.Run(bg, d.ID)
	if !errors.Is(err, runtime.ErrTimeout) {
		t.Fatalf("超时应归为 ErrTimeout: %v", err)
	}
	if got, _ = f.svc.Get(bg, d.ID); got.Failures != 2 {
		t.Fatalf("连续失败 = %d，期望 2", got.Failures)
	}

	// 恢复
	f.probe.setFn(nil)
	if _, err = f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = f.svc.Get(bg, d.ID)
	if got.Failures != 0 || got.LastError != "" || got.DisplayState != "ok" || got.Report.Stale {
		t.Fatalf("恢复后状态 = %+v", got.Instance)
	}

	// 未知实例
	if _, err := f.svc.Run(bg, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Run 不存在: %v", err)
	}
}

func TestHistoryRecorderOnlyOnSuccess(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	f.probe.setFn(func(context.Context, runtime.Input) (*report.Report, error) { return nil, errors.New("boom") })
	_, _ = f.svc.Run(bg, d.ID)
	if f.hist.count() != 1 || f.hist.recs[0] != d.ID+"|host=a" {
		t.Fatalf("历史记录器应只收到成功报告: %v", f.hist.recs)
	}
}

func TestSchedulerWiringHashPauseResume(t *testing.T) {
	f := newFx(t)
	f.start()
	d := f.create("probe", "a", probeCfg("a"))
	if !f.svc.isScheduled(d.ID) {
		t.Fatal("新实例应进入调度器")
	}
	waitFor(t, "首次采集", func() bool { return f.probe.calls() == 1 })
	waitFor(t, "排入下次", func() bool { return f.clk.Waiters() >= 2 })
	waitFor(t, "结果写入状态", func() bool {
		g, _ := f.svc.Get(bg, d.ID)
		return g.LastSuccessAt != nil
	})
	h1 := f.svc.scheduleHash(d.ID)

	f.clk.Advance(60 * time.Second)
	waitFor(t, "按间隔再次采集", func() bool { return f.probe.calls() >= 2 })

	// 只改名称：哈希不变
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "a"}}); err != nil {
		t.Fatal(err)
	}
	if f.svc.scheduleHash(d.ID) != h1 {
		t.Fatal("改名不应改变调度哈希")
	}
	// 改配置、改间隔：哈希变化
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "z"}}); err != nil {
		t.Fatal(err)
	}
	h2 := f.svc.scheduleHash(d.ID)
	if h2 == h1 {
		t.Fatal("改配置应改变调度哈希")
	}
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "z"}, IntervalSeconds: 120}); err != nil {
		t.Fatal(err)
	}
	if f.svc.scheduleHash(d.ID) == h2 {
		t.Fatal("改间隔应改变调度哈希")
	}

	// 暂停移出、恢复加回；暂停时 API 带 paused
	p, err := f.svc.Pause(bg, d.ID)
	if err != nil || !p.Paused {
		t.Fatalf("Pause = %+v %v", p, err)
	}
	if f.svc.isScheduled(d.ID) {
		t.Fatal("暂停后不应在调度器里")
	}
	if _, err := f.svc.Resume(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if !f.svc.isScheduled(d.ID) {
		t.Fatal("恢复后应回到调度器")
	}

	// 删除移出调度器并清掉状态
	if _, err := f.svc.Delete(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.svc.isScheduled(d.ID) {
		t.Fatal("删除后不应在调度器里")
	}
	if _, err := f.svc.Delete(bg, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除: %v", err)
	}
}

func TestStateResetOnlyWhenContentChanges(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "a"}}); err != nil {
		t.Fatal(err)
	}
	if g, _ := f.svc.Get(bg, d.ID); g.Report == nil {
		t.Fatal("只改名称（密钥也未变）不应清空当前状态")
	}
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "b"}}); err != nil {
		t.Fatal(err)
	}
	g, _ := f.svc.Get(bg, d.ID)
	if g.Report != nil || g.LastSuccessAt != nil || g.DisplayState != "unknown" {
		t.Fatalf("配置变了应清空旧状态: %+v", g)
	}
}

func TestPluginVanishesMakesInstanceBroken(t *testing.T) {
	f := newFx(t)
	f.putExec("ghost")
	f.rescan()
	f.start()
	d := f.create("ghost", "g", probeCfg("g"))
	if !f.svc.isScheduled(d.ID) {
		t.Fatal("exec 插件实例应进入调度器")
	}
	if err := os.RemoveAll(filepath.Join(f.plugDir, "ghost")); err != nil {
		t.Fatal(err)
	}
	f.rescan() // 注册表通知实例服务重排
	if f.svc.isScheduled(d.ID) {
		t.Fatal("插件消失后不应再调度")
	}
	g, err := f.svc.Get(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.DisplayState != "broken" || g.Issue == "" {
		t.Fatalf("应为 broken: %+v", g.Instance)
	}
	if _, err := f.svc.Run(bg, d.ID); !errors.Is(err, runtime.ErrFailed) {
		t.Fatalf("broken 实例不能运行: %v", err)
	}
	// 插件回来后恢复调度
	f.putExec("ghost")
	f.rescan()
	if !f.svc.isScheduled(d.ID) {
		t.Fatal("插件恢复后应重新调度")
	}
	if g, _ = f.svc.Get(bg, d.ID); g.DisplayState == "broken" {
		t.Fatalf("插件恢复后不应再是 broken: %+v", g.Instance)
	}
}

func TestPausedNotStale(t *testing.T) {
	f := newFx(t)
	a := f.create("probe", "a", probeCfg("a"))
	b := f.create("probe", "b", probeCfg("b"))
	for _, id := range []string{a.ID, b.ID} {
		if _, err := f.svc.Run(bg, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Pause(bg, b.ID); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(10 * time.Minute) // 远超 3 倍间隔
	ga, _ := f.svc.Get(bg, a.ID)
	gb, _ := f.svc.Get(bg, b.ID)
	if ga.DisplayState != "stale" {
		t.Fatalf("运行中的实例应过期: %s", ga.DisplayState)
	}
	if gb.DisplayState != "ok" || !gb.Paused {
		t.Fatalf("暂停的实例不做过期判定: %+v", gb.Instance)
	}
}

func TestCopyClearsSecrets(t *testing.T) {
	f := newFx(t)
	f.start()
	src := f.create("probe", "a", probeCfg("a.example"))
	c, err := f.svc.Copy(bg, src.ID, "a 副本")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == src.ID || c.Name != "a 副本" || c.PluginID != "probe" {
		t.Fatalf("副本 = %+v", c.Instance)
	}
	if c.Config["host"] != "a.example" {
		t.Fatalf("普通配置应复制: %v", c.Config)
	}
	if _, has := c.Config["api_key"]; has {
		t.Fatalf("副本的密钥应置空: %v", c.Config)
	}
	if c.Problems["api_key"] != model.FieldRequired || c.DisplayState != "unconfigured" {
		t.Fatalf("副本需重新填写密钥: state=%s problems=%v", c.DisplayState, c.Problems)
	}
	if f.svc.isScheduled(c.ID) {
		t.Fatal("配置不完整的副本不应进入调度器")
	}
	var enc string
	must(t, f.db.QueryRow(`SELECT secrets_enc FROM plugin_instances WHERE id=?`, c.ID).Scan(&enc))
	if enc != "" {
		t.Fatal("副本不应带密钥密文")
	}
	// 填写密钥后即可运行
	if _, err := f.svc.Update(bg, c.ID, model.InstanceInput{Name: "a 副本", Config: probeCfg("a.example")}); err != nil {
		t.Fatal(err)
	}
	if !f.svc.isScheduled(c.ID) {
		t.Fatal("补全配置后应进入调度器")
	}
	// 源实例不受影响
	if _, err := f.svc.Run(bg, src.ID); err != nil {
		t.Fatal(err)
	}
	if f.probe.last().Secrets["api_key"] != "s3cret-key" {
		t.Fatal("源实例密钥被改动")
	}
}

func TestUndeclaredKeysDroppedNotFatal(t *testing.T) {
	f := newFx(t)
	d := f.create("plain", "p", map[string]any{"host": "h"})
	// 模拟插件升级删掉了字段：库里的旧配置带着未声明的键
	if _, err := f.db.Exec(`UPDATE plugin_instances SET config_json=? WHERE id=?`, `{"host":"h","retired":"x"}`, d.ID); err != nil {
		t.Fatal(err)
	}
	g, err := f.svc.Get(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Problems) != 0 || g.DisplayState == "broken" || g.DisplayState == "unconfigured" {
		t.Fatalf("未声明键不应让实例失效: %+v %v", g.Instance, g.Problems)
	}
	if _, bad := g.Config["retired"]; bad {
		t.Fatalf("回显应丢弃未声明键: %v", g.Config)
	}
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, bad := f.plain.last().Config["retired"]; bad {
		t.Fatal("插件输入不应含未声明键")
	}
}

func TestReferrersAndMissingProxy(t *testing.T) {
	f := newFx(t)
	px, _ := proxy.Parse("http://127.0.0.1:8080")
	f.px.known["px1"] = px
	cfg := probeCfg("a")
	cfg["proxy"] = "px1"
	a := f.create("probe", "走代理", cfg)
	f.create("probe", "直连", probeCfg("b"))

	refs, err := f.svc.ListByProxy(bg, "px1")
	if err != nil || len(refs) != 1 || refs[0].ID != a.ID || refs[0].Name != "走代理" {
		t.Fatalf("ListByProxy = %v %v", refs, err)
	}
	if refs, _ = f.svc.ListByProxy(bg, "other"); len(refs) != 0 {
		t.Fatalf("无引用应为空: %v", refs)
	}
	if _, err := f.svc.Run(bg, a.ID); err != nil {
		t.Fatal(err)
	}
	if f.probe.last().Proxy.IsDirect() || f.probe.last().Proxy.Redacted() != "http://127.0.0.1:8080" {
		t.Fatalf("应经代理运行: %v", f.probe.last().Proxy)
	}

	for i := 0; i < 2; i++ { // 幂等
		if err := f.svc.ResetToDirect(bg, "px1"); err != nil {
			t.Fatal(err)
		}
	}
	if refs, _ = f.svc.ListByProxy(bg, "px1"); len(refs) != 0 {
		t.Fatalf("改直连后不应再有引用: %v", refs)
	}
	g, _ := f.svc.Get(bg, a.ID)
	if g.Config["proxy"] != "direct" {
		t.Fatalf("配置应改为 direct: %v", g.Config["proxy"])
	}
	if _, err := f.svc.Run(bg, a.ID); err != nil {
		t.Fatal(err)
	}
	if !f.probe.last().Proxy.IsDirect() {
		t.Fatal("改直连后应直连运行")
	}
	// 指向已不存在代理的实例不再按直连运行，见 TestRemovedProxyFailsRun（Ruling 49）。
}

func TestStreamerUsesStreamManager(t *testing.T) {
	f := newFx(t)
	f.start()
	d := f.create("streamer", "s", map[string]any{"host": "h"})
	waitFor(t, "Streamer 启动", func() bool { return f.stream.startedCount() == 1 })
	waitFor(t, "emit 写入当前状态", func() bool {
		g, _ := f.svc.Get(bg, d.ID)
		return g.Summary == "host=stream"
	})
	if f.stream.calls() != 0 {
		t.Fatal("Streamer 实例不应走定时采集")
	}
	if !f.svc.isScheduled(d.ID) {
		t.Fatal("Streamer 实例应被管理")
	}
	if _, err := f.svc.Pause(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.svc.isScheduled(d.ID) {
		t.Fatal("暂停后应停掉 Streamer")
	}
	if f.hist.count() == 0 {
		t.Fatal("emit 的报告也应交给历史记录器")
	}
}

func TestListAndDeleteCascadesState(t *testing.T) {
	f := newFx(t)
	a := f.create("probe", "b 名", probeCfg("a"))
	f.create("probe", "a 名", probeCfg("b"))
	if _, err := f.svc.Run(bg, a.ID); err != nil {
		t.Fatal(err)
	}
	must(t, f.svc.Flush(bg))
	list, err := f.svc.List(bg)
	if err != nil || len(list) != 2 || list[0].Name != "a 名" {
		t.Fatalf("List 应按名称排序: %+v %v", list, err)
	}
	if f.stateRows() != 1 {
		t.Fatal("应有一行状态")
	}
	res, err := f.svc.Delete(bg, a.ID)
	if err != nil || res.AffectedScreens == nil || len(res.AffectedScreens) != 0 {
		t.Fatalf("Delete = %+v %v", res, err)
	}
	if f.stateRows() != 0 {
		t.Fatal("删除实例应级联删除状态行")
	}
	// 删除后迟到的结果不得让落盘失败（外键）
	must(t, f.svc.Flush(bg))
	if f.svc.WriteErrors() != 0 {
		t.Fatal("不应有写库错误")
	}
}

// 插件私有 state 只在内存与库里流转，不得经 API 返回（可能缓存登录态）。
func TestPrivateStateNotExposed(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	res, err := f.svc.Run(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), `"state"`) || res.Report.State != "" {
		t.Fatalf("run 响应含私有 state: %s", raw)
	}
	check := func(what string, v any) {
		t.Helper()
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), `"state"`) {
			t.Fatalf("%s 含私有 state: %s", what, b)
		}
	}
	got, _ := f.svc.Get(bg, d.ID)
	check("Get", got)
	upd, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	check("Update", upd)
	cp, err := f.svc.Copy(bg, d.ID, "副本")
	if err != nil {
		t.Fatal(err)
	}
	check("Copy", cp)

	// 内存中的 state 不受影响：下一次运行仍收到上次的值
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	in := f.probe.last()
	if in.State != "s1" {
		t.Fatalf("下次运行的 Input.State = %q，期望 s1", in.State)
	}
}

func TestMinIntervalEnforced(t *testing.T) {
	f := newFx(t)
	cfg := map[string]any{"host": "h"}
	var fe model.FieldErrors
	if _, err := f.svc.Create(bg, model.InstanceInput{PluginID: "floor", Name: "x", Config: cfg, IntervalSeconds: 29}); !errors.As(err, &fe) || fe["interval_seconds"] != model.FieldOutOfRange {
		t.Fatalf("低于 min_interval 应报 out_of_range: %v", err)
	}
	d, err := f.svc.Create(bg, model.InstanceInput{PluginID: "floor", Name: "x", Config: cfg, IntervalSeconds: 30})
	if err != nil {
		t.Fatalf("等于 min_interval 应通过: %v", err)
	}
	if _, err := f.svc.Create(bg, model.InstanceInput{PluginID: "floor", Name: "y", Config: cfg}); err != nil {
		t.Fatalf("0 表示用默认值应通过: %v", err)
	}
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "x", Config: cfg, IntervalSeconds: 10}); !errors.As(err, &fe) || fe["interval_seconds"] != model.FieldOutOfRange {
		t.Fatalf("更新时低于 min_interval 应报 out_of_range: %v", err)
	}
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "x", Config: cfg, IntervalSeconds: 0}); err != nil {
		t.Fatalf("更新为 0 应通过: %v", err)
	}
	// 未声明 min_interval 的插件仍只受全局下限约束。
	if _, err := f.svc.Create(bg, model.InstanceInput{PluginID: "plain", Name: "p", Config: cfg, IntervalSeconds: 5}); err != nil {
		t.Fatalf("无 min_interval 的插件 5 秒应通过: %v", err)
	}
}

// changeLog 记录 OnChange 回调收到的实例 id。
type changeLog struct {
	mu  sync.Mutex
	ids []string
}

func (c *changeLog) add(id string) {
	c.mu.Lock()
	c.ids = append(c.ids, id)
	c.mu.Unlock()
}

func (c *changeLog) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.ids
	c.ids = nil
	return out
}

func (c *changeLog) only(id string) bool {
	got := c.take()
	return len(got) > 0 && slices.Compact(got)[0] == id && len(slices.Compact(got)) == 1
}

func TestOnChangeNotifiesEveryMutation(t *testing.T) {
	f := newFx(t)
	log := &changeLog{}
	f.svc.OnChange(log.add)

	d := f.create("probe", "a", probeCfg("a"))
	if !log.only(d.ID) {
		t.Fatal("新建应通知该实例")
	}
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "a"}}); err != nil {
		t.Fatal(err)
	}
	if !log.only(d.ID) {
		t.Fatal("更新应通知该实例")
	}
	if _, err := f.svc.Pause(bg, d.ID); err != nil || !log.only(d.ID) {
		t.Fatalf("暂停应通知该实例: %v", err)
	}
	if _, err := f.svc.Resume(bg, d.ID); err != nil || !log.only(d.ID) {
		t.Fatalf("恢复应通知该实例: %v", err)
	}
	cp, err := f.svc.Copy(bg, d.ID, "副本")
	if err != nil || !log.only(cp.ID) {
		t.Fatalf("复制应通知新实例: %v", err)
	}
	log.take()
	if _, err := f.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if got := log.take(); !slices.Contains(got, d.ID) {
		t.Fatalf("采集结果写入状态后应通知: %v", got)
	}
	if _, err := f.svc.Delete(bg, d.ID); err != nil || !log.only(d.ID) {
		t.Fatalf("删除应通知该实例: %v", err)
	}
	if _, err := f.svc.View(bg, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 View 应返回 ErrNotFound: %v", err)
	}
	v, err := f.svc.View(bg, cp.ID)
	if err != nil || v.ID != cp.ID || v.Name != "副本" {
		t.Fatalf("View = %+v %v", v, err)
	}
}

// corruptRow 模拟库里配置损坏（config_json 不是合法 JSON）或密钥密文无法解密。
func corruptRow(t *testing.T, f *fx, id, kind string) {
	t.Helper()
	switch kind {
	case "config":
		_, err := f.db.Exec(`UPDATE plugin_instances SET config_json=? WHERE id=?`, `{not json`, id)
		must(t, err)
	case "secrets":
		_, err := f.db.Exec(`UPDATE plugin_instances SET secrets_enc=? WHERE id=?`, `garbage-ciphertext`, id)
		must(t, err)
	}
}

func TestGetCorruptInstanceReportsRefillProblem(t *testing.T) {
	for _, kind := range []string{"config", "secrets"} {
		t.Run(kind, func(t *testing.T) {
			f := newFx(t)
			d := f.create("probe", "损坏实例", probeCfg("a.example"))
			corruptRow(t, f, d.ID, kind)
			got, err := f.svc.Get(bg, d.ID)
			if err != nil {
				t.Fatalf("Get 不应失败: %v", err)
			}
			if got.Problems[RefillKey] != model.FieldInvalid {
				t.Fatalf("应提示需重新填写: %v", got.Problems)
			}
		})
	}
}

func TestUpdateCorruptInstanceTakesBodyAsFullConfig(t *testing.T) {
	for _, kind := range []string{"config", "secrets"} {
		t.Run(kind, func(t *testing.T) {
			f := newFx(t)
			f.start()
			d := f.create("probe", "损坏实例", probeCfg("a.example"))
			corruptRow(t, f, d.ID, kind)

			// 请求体不带密钥：不能再沿用旧值，按必填缺失返回字段错误而不是 500
			_, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "损坏实例", Config: map[string]any{"host": "b.example"}})
			var fe model.FieldErrors
			if !errors.As(err, &fe) || fe["api_key"] != model.FieldRequired {
				t.Fatalf("缺密钥应返回字段错误: %v", err)
			}

			// 回显标记也不能当作保留旧值
			_, err = f.svc.Update(bg, d.ID, model.InstanceInput{Name: "损坏实例",
				Config: map[string]any{"host": "b.example", "api_key": map[string]any{"set": true}}})
			if !errors.As(err, &fe) {
				t.Fatalf("回显标记应被拒绝: %v", err)
			}

			// 完整重填后成功，问题标记清除，可正常运行
			got, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "损坏实例", Config: probeCfg("b.example")})
			if err != nil {
				t.Fatalf("重填应成功: %v", err)
			}
			if len(got.Problems) != 0 || got.Issue != "" {
				t.Fatalf("成功后应清除问题: issue=%q problems=%v", got.Issue, got.Problems)
			}
			g2, err := f.svc.Get(bg, d.ID)
			if err != nil || len(g2.Problems) != 0 {
				t.Fatalf("再次读取仍有问题: %v %v", err, g2.Problems)
			}
			if _, err := f.svc.Run(bg, d.ID); err != nil {
				t.Fatal(err)
			}
			if f.probe.last().Secrets["api_key"] != "s3cret-key" || f.probe.last().Config["host"] != "b.example" {
				t.Fatalf("重填后的配置未生效: %+v", f.probe.last())
			}
		})
	}
}
