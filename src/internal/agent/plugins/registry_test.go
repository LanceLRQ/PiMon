package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/pluginscan"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

func manifestYAML(id, kind, rt, runsOn string) string {
	y := "id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: " + id + "\nkind: " + kind + "\nruntime: " + rt +
		"\nruns_on: " + runsOn + "\n"
	if kind == "source" {
		y += "config_schema:\n  - {key: host, type: string, title: Host, pattern: \"^[a-z]+$\"}\n" +
			"outputs:\n  - {key: v, type: number, title: V}\n"
	}
	return y
}

type fakeSource struct{ m *manifest.Manifest }

func (s fakeSource) Manifest() *manifest.Manifest { return s.m }
func (fakeSource) Collect(context.Context, runtime.Input) (*report.Report, error) {
	return &report.Report{}, nil
}

func builtin(t *testing.T, id, kind, runsOn string) runtime.Source {
	t.Helper()
	m, err := manifest.Parse([]byte(manifestYAML(id, kind, "builtin", runsOn)))
	if err != nil {
		t.Fatal(err)
	}
	return fakeSource{m}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	dir string
	clk *clock.Fake
	log *lockedBuf
	reg *Registry
}

func newFixture(t *testing.T, euid func() int, builtins ...runtime.Source) *fixture {
	t.Helper()
	f := &fixture{
		dir: filepath.Join(t.TempDir(), "plugins"),
		clk: clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		log: &lockedBuf{},
	}
	f.reg = New(Config{
		Dir: f.dir, Builtins: builtins, Clock: f.clk, EUID: euid,
		Log: slog.New(slog.NewTextHandler(f.log, nil)),
	})
	return f
}

// lockedBuf 是可被监视协程与测试协程并发使用的日志缓冲。
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// put 在插件目录下写一个 exec 插件，返回其目录。
func (f *fixture) put(t *testing.T, id, yaml string) string {
	t.Helper()
	d := filepath.Join(f.dir, id)
	must(t, os.MkdirAll(d, 0o755))
	must(t, os.Chmod(d, 0o755))
	must(t, os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(yaml), 0o644))
	must(t, os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\necho '{}'\n"), 0o755))
	must(t, os.Chmod(filepath.Join(d, "run"), 0o755))
	return d
}

func ids(s Snapshot) string {
	var out []string
	for _, p := range s.Plugins {
		out = append(out, p.ID)
	}
	return strings.Join(out, ",")
}

func issueFor(t *testing.T, s Snapshot, dir string) Issue {
	t.Helper()
	for _, i := range s.Issues {
		if i.Dir == dir {
			return i
		}
	}
	t.Fatalf("没有目录 %s 的加载问题: %+v", dir, s.Issues)
	return Issue{}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBuiltins只保留runs_on含agent的数据源插件(t *testing.T) {
	f := newFixture(t, nil,
		builtin(t, "core", "source", "[hub]"),
		builtin(t, "host-metrics", "source", "[hub, agent]"),
		builtin(t, "agent-only", "source", "[agent]"),
		builtin(t, "notify", "notifier", "[hub, agent]"),
	)
	s := f.reg.Scan()
	if got := ids(s); got != "agent-only,host-metrics" {
		t.Fatalf("插件 = %s", got)
	}
	p, ok := f.reg.Get("host-metrics")
	if !ok || p.Origin != OriginBuiltin || p.Source == nil || p.Manifest.ID != "host-metrics" || p.Dir != "" {
		t.Fatalf("内置插件不符: %+v ok=%v", p, ok)
	}
	for _, id := range []string{"core", "notify"} {
		if _, ok := f.reg.Get(id); ok {
			t.Fatalf("%s 不应在 agent 注册表里", id)
		}
	}
	if len(s.Issues) != 0 {
		t.Fatalf("内置插件被过滤不算加载问题: %+v", s.Issues)
	}
}

func TestExec插件扫描后带已构造的Source(t *testing.T) {
	f := newFixture(t, nil)
	d := f.put(t, "probe", manifestYAML("probe", "source", "exec", "[hub, agent]"))
	f.reg.Scan()
	p, ok := f.reg.Get("probe")
	if !ok {
		t.Fatal("应能取到 exec 插件")
	}
	es, isExec := p.Source.(*runtime.ExecSource)
	if p.Origin != OriginExec || !isExec || es.Manifest() != p.Manifest || p.Dir != d || p.RunPath != filepath.Join(d, "run") {
		t.Fatalf("exec 插件不符: %+v", p)
	}
}

func TestExec插件属主不符被拒(t *testing.T) {
	other := os.Geteuid() + 12345
	f := newFixture(t, func() int { return other })
	d := f.put(t, "probe", manifestYAML("probe", "source", "exec", "[agent]"))
	s := f.reg.Scan()
	if _, ok := f.reg.Get("probe"); ok {
		t.Fatal("属主不符的插件不应可用")
	}
	if os.Geteuid() == 0 {
		return // root 属主的文件总能通过属主检查，这里只确认不 panic
	}
	is := issueFor(t, s, d)
	if is.Kind != pluginscan.IssueInsecure || !strings.Contains(is.Message, "agent 运行用户") {
		t.Fatalf("issue = %+v", is)
	}
}

func TestExec与内置id冲突时内置优先并记问题(t *testing.T) {
	f := newFixture(t, nil,
		builtin(t, "dup", "source", "[hub, agent]"),
		builtin(t, "core", "source", "[hub]"),
	)
	dup := f.put(t, "dup", manifestYAML("dup", "source", "exec", "[agent]"))
	core := f.put(t, "core", manifestYAML("core", "source", "exec", "[agent]"))
	s := f.reg.Scan()
	if p, ok := f.reg.Get("dup"); !ok || p.Origin != OriginBuiltin {
		t.Fatalf("冲突时应取内置插件: %+v", p)
	}
	// hub 专用的内置插件 id 同样保留，不让同名 exec 插件冒充。
	if _, ok := f.reg.Get("core"); ok {
		t.Fatal("与 hub 专用内置插件同名的 exec 插件不应可用")
	}
	for _, d := range []string{dup, core} {
		if is := issueFor(t, s, d); is.Kind != pluginscan.IssueConflict {
			t.Fatalf("%s: issue = %+v", d, is)
		}
	}
	out := f.log.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "dir="+dup) || !strings.Contains(out, "id=dup") {
		t.Fatalf("冲突应写 Warn 日志并带 dir、id: %s", out)
	}
}

func TestExec插件不在agent运行时记IssueNotForAgent(t *testing.T) {
	f := newFixture(t, nil)
	hubOnly := f.put(t, "hub-only", manifestYAML("hub-only", "source", "exec", "[hub]"))
	notifier := f.put(t, "notify", manifestYAML("notify", "notifier", "exec", "[hub, agent]"))
	s := f.reg.Scan()
	if len(s.Plugins) != 0 {
		t.Fatalf("不应有可用插件: %s", ids(s))
	}
	for _, d := range []string{hubOnly, notifier} {
		if is := issueFor(t, s, d); is.Kind != IssueNotForAgent || is.ID == "" || is.Message == "" {
			t.Fatalf("%s: issue = %+v", d, is)
		}
	}
	ms, err := f.reg.Manifests()
	if err != nil || len(ms) != 0 {
		t.Fatalf("不在 agent 运行的插件不上报: %v %v", ms, err)
	}
}

func TestManifests与hub的FromJSON往返一致(t *testing.T) {
	f := newFixture(t, nil, builtin(t, "host-metrics", "source", "[hub, agent]"))
	f.put(t, "probe", manifestYAML("probe", "source", "exec", "[agent]"))
	f.reg.Scan()
	ms, err := f.reg.Manifests()
	must(t, err)
	if len(ms) != 2 || ms[0].Origin != "builtin" || ms[1].Origin != "exec" {
		t.Fatalf("manifests = %+v", ms)
	}
	for i, id := range []string{"host-metrics", "probe"} {
		want, _ := f.reg.Get(id)
		got, err := manifest.FromJSON(ms[i].Manifest)
		if err != nil {
			t.Fatalf("%s: FromJSON: %v", id, err)
		}
		if !reflect.DeepEqual(got, want.Manifest) {
			t.Fatalf("%s 往返不一致:\n got=%+v\nwant=%+v", id, got, want.Manifest)
		}
		again, err := json.Marshal(got)
		must(t, err)
		if !bytes.Equal(again, ms[i].Manifest) {
			t.Fatalf("%s 再序列化不一致", id)
		}
	}
}

func TestWatch目录变化防抖后重扫并回调(t *testing.T) {
	f := newFixture(t, nil)
	f.reg.Scan()
	var calls atomic.Int32
	f.reg.OnChange(func() { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	done, err := f.reg.Watch(ctx)
	must(t, err)
	if st, err := os.Stat(f.dir); err != nil || !st.IsDir() {
		t.Fatalf("Watch 应先创建插件目录: %v", err)
	}

	f.put(t, "late", manifestYAML("late", "source", "exec", "[hub, agent]"))
	waitFor(t, "防抖计时器注册", func() bool { return f.clk.Waiters() > 0 })
	if calls.Load() != 0 {
		t.Fatal("防抖到期前不应回调")
	}
	// 写入会产生多个事件，可能在推进后才到达而顺延计时；有等待者就继续推进，直到回调发生。
	waitFor(t, "回调", func() bool {
		if f.clk.Waiters() > 0 {
			f.clk.Advance(time.Second)
		}
		return calls.Load() >= 1
	})
	if p, ok := f.reg.Get("late"); !ok || p.Source == nil {
		t.Fatalf("回调时新插件应已可取: %+v", p)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 结束后监视未停止")
	}
}
