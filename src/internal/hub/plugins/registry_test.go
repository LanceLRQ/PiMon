package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

func manifestYAML(id, rt string) string {
	return "id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: " + id + "\nkind: source\nruntime: " + rt +
		"\nruns_on: [hub]\noutputs:\n  - {key: v, type: number, title: V}\n"
}

type fakeSource struct{ m *manifest.Manifest }

func (s fakeSource) Manifest() *manifest.Manifest { return s.m }
func (fakeSource) Collect(context.Context, runtime.Input) (*report.Report, error) {
	return &report.Report{}, nil
}

func builtin(t *testing.T, id string) runtime.Source {
	t.Helper()
	m, err := manifest.Parse([]byte(manifestYAML(id, "builtin")))
	if err != nil {
		t.Fatal(err)
	}
	return fakeSource{m}
}

type fixture struct {
	dir string
	db  *store.DB
	clk *clock.Fake
	reg *Registry
}

func newFixture(t *testing.T, builtins ...runtime.Source) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: filepath.Join(t.TempDir(), "plugins"), db: db, clk: clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
	if err := os.MkdirAll(f.dir, 0o750); err != nil {
		t.Fatal(err)
	}
	f.reg = New(Config{Dir: f.dir, DB: db, Clock: f.clk, Builtins: builtins})
	return f
}

// put 在插件目录下写一个合法插件，返回其目录。
func (f *fixture) put(t *testing.T, id, yaml string) string {
	t.Helper()
	d := filepath.Join(f.dir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, os.Chmod(d, 0o755))
	must(t, os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(yaml), 0o644))
	must(t, os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\necho '{}'\n"), 0o755))
	must(t, os.Chmod(filepath.Join(d, "run"), 0o755))
	return d
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) scan(t *testing.T) Snapshot {
	t.Helper()
	s, err := f.reg.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ids(s Snapshot) []string {
	var out []string
	for _, p := range s.Plugins {
		out = append(out, p.ID)
	}
	return out
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

func TestScanAppearsAndDisappears(t *testing.T) {
	f := newFixture(t, builtin(t, "core"))
	d := f.put(t, "demo-exec", manifestYAML("demo-exec", "exec"))
	s := f.scan(t)
	if got := strings.Join(ids(s), ","); got != "core,demo-exec" {
		t.Fatalf("插件 = %s", got)
	}
	p, ok := f.reg.Get("demo-exec")
	if !ok || p.Origin != OriginExec || p.Dir != d || p.RunPath != filepath.Join(d, "run") || p.Manifest == nil || p.Source != nil {
		t.Fatalf("exec 条目不对: %+v", p)
	}
	if c, ok := f.reg.Get("core"); !ok || c.Origin != OriginBuiltin || c.Source == nil {
		t.Fatalf("内置条目不对: %+v", c)
	}
	must(t, os.RemoveAll(d))
	s = f.scan(t)
	if got := strings.Join(ids(s), ","); got != "core" {
		t.Fatalf("删除后插件 = %s", got)
	}
	if _, ok := f.reg.Get("demo-exec"); ok {
		t.Fatal("删除后不应再取到")
	}
	// 库里保留记录并标为不可用
	st, err := f.reg.Stored(context.Background())
	must(t, err)
	avail := map[string]bool{}
	for _, m := range st {
		avail[m.PluginID] = m.Available
	}
	if a, ok := avail["demo-exec"]; !ok || a {
		t.Fatalf("demo-exec 应保留且不可用: %+v", st)
	}
	if !avail["core"] {
		t.Fatalf("core 应可用: %+v", st)
	}
	// 重新出现后恢复可用
	f.put(t, "demo-exec", manifestYAML("demo-exec", "exec"))
	f.scan(t)
	st, _ = f.reg.Stored(context.Background())
	for _, m := range st {
		if m.PluginID == "demo-exec" && !m.Available {
			t.Fatal("重新出现后应恢复可用")
		}
	}
}

func TestInvalidManifestReportsLine(t *testing.T) {
	f := newFixture(t)
	bad := strings.Replace(manifestYAML("bad", "exec"), "kind: source", "kind: nonsense", 1)
	d := f.put(t, "bad", bad)
	s := f.scan(t)
	if len(s.Plugins) != 0 {
		t.Fatalf("不应加载: %v", ids(s))
	}
	is := issueFor(t, s, d)
	if is.Kind != IssueInvalidManifest || len(is.Problems) == 0 || is.Problems[0].Line != 5 {
		t.Fatalf("应报 kind 所在第 5 行: %+v", is)
	}
}

func TestIDMismatchAndRuntimeNotExec(t *testing.T) {
	f := newFixture(t)
	d1 := f.put(t, "dir-a", manifestYAML("other-id", "exec"))
	d2 := f.put(t, "b", manifestYAML("b", "builtin"))
	s := f.scan(t)
	if len(s.Plugins) != 0 {
		t.Fatalf("不应加载: %v", ids(s))
	}
	if k := issueFor(t, s, d1).Kind; k != IssueIDMismatch {
		t.Fatalf("kind = %s", k)
	}
	if k := issueFor(t, s, d2).Kind; k != IssueNotExec {
		t.Fatalf("kind = %s", k)
	}
}

func TestMissingRunAndNotExecutable(t *testing.T) {
	f := newFixture(t)
	d1 := f.put(t, "norun", manifestYAML("norun", "exec"))
	must(t, os.Remove(filepath.Join(d1, "run")))
	d2 := f.put(t, "noexec", manifestYAML("noexec", "exec"))
	must(t, os.Chmod(filepath.Join(d2, "run"), 0o644))
	s := f.scan(t)
	if k := issueFor(t, s, d1).Kind; k != IssueMissingRun {
		t.Fatalf("kind = %s", k)
	}
	if k := issueFor(t, s, d2).Kind; k != IssueNotExecutable {
		t.Fatalf("kind = %s", k)
	}
}

func TestInsecurePermissionsRejected(t *testing.T) {
	f := newFixture(t)
	d1 := f.put(t, "gw", manifestYAML("gw", "exec"))
	must(t, os.Chmod(filepath.Join(d1, "run"), 0o775))
	d2 := f.put(t, "ow", manifestYAML("ow", "exec"))
	must(t, os.Chmod(filepath.Join(d2, "run"), 0o757))
	d3 := f.put(t, "dirw", manifestYAML("dirw", "exec"))
	must(t, os.Chmod(d3, 0o777))
	s := f.scan(t)
	for _, d := range []string{d1, d2, d3} {
		if k := issueFor(t, s, d).Kind; k != IssueInsecure {
			t.Fatalf("%s kind = %s", d, k)
		}
	}
	if len(s.Plugins) != 0 {
		t.Fatalf("不应加载: %v", ids(s))
	}
}

func TestInsecureOwnerRejected(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 下属主恒被接受")
	}
	f := newFixture(t)
	f.reg = New(Config{Dir: f.dir, DB: f.db, Clock: f.clk, EUID: func() int { return os.Geteuid() + 1 }})
	d := f.put(t, "own", manifestYAML("own", "exec"))
	s := f.scan(t)
	if k := issueFor(t, s, d).Kind; k != IssueInsecure {
		t.Fatalf("属主不是 euid 应拒绝: %+v", s.Issues)
	}
}

func TestBuiltinWinsConflict(t *testing.T) {
	f := newFixture(t, builtin(t, "core"))
	d := f.put(t, "core", manifestYAML("core", "exec"))
	s := f.scan(t)
	p, ok := f.reg.Get("core")
	if !ok || p.Origin != OriginBuiltin {
		t.Fatalf("内置应优先: %+v", p)
	}
	if k := issueFor(t, s, d).Kind; k != IssueConflict {
		t.Fatalf("kind = %s", k)
	}
}

func TestMissingPluginDirIsEmpty(t *testing.T) {
	f := newFixture(t, builtin(t, "core"))
	must(t, os.RemoveAll(f.dir))
	s := f.scan(t)
	if len(s.Issues) != 0 || len(s.Plugins) != 1 {
		t.Fatalf("目录不存在应视为没有 exec 插件: %+v", s)
	}
}

// waitFor 轮询直到条件满足（fsnotify 事件来自真实文件系统，需要真实等待）。
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

func TestWatchDiscoversNewDirAfterDebounce(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done, err := f.reg.Watch(ctx)
	must(t, err)
	f.put(t, "late", manifestYAML("late", "exec"))
	// 防抖计时器用假时钟：等它注册、并让本次写入产生的事件收尾后才推进
	waitFor(t, "防抖计时器注册", func() bool { return f.clk.Waiters() > 0 })
	time.Sleep(100 * time.Millisecond)
	if _, ok := f.reg.Get("late"); ok {
		t.Fatal("防抖期内不应已扫描")
	}
	f.clk.Advance(time.Second)
	waitFor(t, "发现新插件", func() bool { _, ok := f.reg.Get("late"); return ok })
	// 新目录内的后续变更也能被监视到
	must(t, os.RemoveAll(filepath.Join(f.dir, "late")))
	waitFor(t, "防抖计时器再次注册", func() bool { return f.clk.Waiters() > 0 })
	time.Sleep(100 * time.Millisecond)
	f.clk.Advance(time.Second)
	waitFor(t, "插件消失", func() bool { _, ok := f.reg.Get("late"); return !ok })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 结束后监视未停止")
	}
}

// 插件根目录读取失败（这里用同名文件模拟）不让扫描失败：内置插件照常注册，失败记为一条加载问题。
func TestUnreadablePluginDirIsIssueNotError(t *testing.T) {
	f := newFixture(t, builtin(t, "core"))
	must(t, os.RemoveAll(f.dir))
	must(t, os.WriteFile(f.dir, []byte("x"), 0o644))
	s := f.scan(t)
	if len(s.Plugins) != 1 || s.Plugins[0].ID != "core" {
		t.Fatalf("内置插件应照常注册: %+v", s.Plugins)
	}
	is := issueFor(t, s, f.dir)
	if is.Kind != IssueDirUnreadable || is.Message == "" {
		t.Fatalf("应记为插件目录不可读: %+v", is)
	}
	if _, ok := f.reg.Get("core"); !ok {
		t.Fatal("Get 应能取到内置插件")
	}
}
