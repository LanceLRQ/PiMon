package instances

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// gate 让假插件的 Collect 阻塞到放行，并记录同时进入 Collect 的峰值。
type gate struct {
	entered  chan struct{}
	release  chan struct{}
	inflight atomic.Int32
	peak     atomic.Int32
}

func newGate() *gate {
	return &gate{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (g *gate) collect(_ context.Context, in runtime.Input) (*report.Report, error) {
	n := g.inflight.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	g.entered <- struct{}{}
	<-g.release
	g.inflight.Add(-1)
	return okReport(in.Config["host"].(string), 0), nil
}

func (g *gate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待进入 Collect 超时")
	}
}

type runOut struct {
	res model.InstanceRunResult
	err error
}

func (f *fx) runAsync(ctx context.Context, id string) <-chan runOut {
	ch := make(chan runOut, 1)
	go func() {
		res, err := f.svc.Run(ctx, id)
		ch <- runOut{res, err}
	}()
	return ch
}

func recvRun(t *testing.T, ch <-chan runOut) runOut {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("等待保存并测试返回超时")
		return runOut{}
	}
}

// 定时采集运行中时，保存并测试等待运行锁，两条路径不会同时进入 Collect。
func TestManualRunWaitsForScheduledRun(t *testing.T) {
	f := newFx(t)
	g := newGate()
	f.probe.setFn(g.collect)
	f.start()
	waitFor(t, "落盘循环就绪", func() bool { return f.clk.Waiters() >= 1 })
	d := f.create("probe", "a", probeCfg("a"))
	g.waitEntered(t) // 首次定时采集（零抖动）已进入并阻塞

	out := f.runAsync(bg, d.ID)
	waitFor(t, "保存并测试在等运行锁", func() bool { return f.clk.Waiters() >= 2 })
	if f.probe.calls() != 1 {
		t.Fatalf("持锁期间手动运行不应进入 Collect: calls=%d", f.probe.calls())
	}
	close(g.release)
	o := recvRun(t, out)
	if o.err != nil {
		t.Fatalf("放行后保存并测试应成功: %v", o.err)
	}
	if g.peak.Load() != 1 || f.probe.calls() != 2 {
		t.Fatalf("不应并发进入 Collect: peak=%d calls=%d", g.peak.Load(), f.probe.calls())
	}
}

// 手动运行持锁时，定时采集等锁而不是并发进入。
func TestScheduledRunWaitsForManualRun(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	g := newGate()
	f.probe.setFn(g.collect)
	out := f.runAsync(bg, d.ID)
	g.waitEntered(t) // 手动运行持锁

	f.start() // 调度器启动后零抖动立即运行，应等锁
	waitFor(t, "定时任务已排入", func() bool { return f.svc.isScheduled(d.ID) })
	close(g.release)
	if o := recvRun(t, out); o.err != nil {
		t.Fatal(o.err)
	}
	waitFor(t, "定时采集在手动运行之后执行", func() bool { return f.probe.calls() == 2 })
	if g.peak.Load() != 1 {
		t.Fatalf("不应并发进入 Collect: peak=%d", g.peak.Load())
	}
}

// 等锁超过插件超时（5s）返回 ErrRunBusy。
func TestManualRunBusyAfterTimeout(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	g := newGate()
	f.probe.setFn(g.collect)
	first := f.runAsync(bg, d.ID)
	g.waitEntered(t)

	second := f.runAsync(bg, d.ID)
	waitFor(t, "第二次在等锁", func() bool { return f.clk.Waiters() >= 1 })
	f.clk.Advance(5 * time.Second)
	if o := recvRun(t, second); !errors.Is(o.err, ErrRunBusy) {
		t.Fatalf("等锁超时应返回 ErrRunBusy: %v", o.err)
	}
	close(g.release)
	if o := recvRun(t, first); o.err != nil {
		t.Fatal(o.err)
	}
	if f.probe.calls() != 1 {
		t.Fatalf("忙时不应运行插件: calls=%d", f.probe.calls())
	}
}

// 运行期间配置内容变了：这次结果对应旧配置，丢弃（不写状态、不写历史）。
func TestStaleResultDiscardedAfterConfigChange(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	g := newGate()
	f.probe.setFn(g.collect)
	out := f.runAsync(bg, d.ID)
	g.waitEntered(t)
	if _, err := f.svc.Update(bg, d.ID, model.InstanceInput{Name: "a", Config: map[string]any{"host": "b"}}); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	recvRun(t, out)
	got, _ := f.svc.Get(bg, d.ID)
	if got.Report != nil || got.LastSuccessAt != nil || got.Failures != 0 {
		t.Fatalf("旧配置的结果不应写入状态: %+v", got.Instance)
	}
	if f.hist.count() != 0 {
		t.Fatalf("旧配置的结果不应写历史: %v", f.hist.recs)
	}
}

func TestApplyResultDropsMismatchedHash(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	f.svc.applyResult(d.ID, "not-the-current-hash", okReport("a", 1), nil)
	if g, _ := f.svc.Get(bg, d.ID); g.Report != nil || f.hist.count() != 0 {
		t.Fatalf("hash 不符的结果应丢弃: %+v", g.Instance)
	}
	var cur string
	must(t, f.db.QueryRow(`SELECT config_hash FROM plugin_instances WHERE id = ?`, d.ID).Scan(&cur))
	f.svc.applyResult(d.ID, cur, okReport("a", 1), nil)
	if g, _ := f.svc.Get(bg, d.ID); g.Report == nil || f.hist.count() != 1 {
		t.Fatalf("hash 相符的结果应写入: %+v", g.Instance)
	}
}

// 存量实例的间隔低于插件（升级后抬高的）min_interval 或全局下限时，生效间隔被钳住。
func TestEffectiveIntervalClamped(t *testing.T) {
	f := newFx(t)
	fl := f.create("floor", "fl", map[string]any{"host": "h"})
	pl := f.create("plain", "pl", map[string]any{"host": "h"})
	must(t, execErr(f.db.Exec(`UPDATE plugin_instances SET interval_seconds = 10 WHERE id = ?`, fl.ID)))
	must(t, execErr(f.db.Exec(`UPDATE plugin_instances SET interval_seconds = 2 WHERE id = ?`, pl.ID)))
	if g, _ := f.svc.Get(bg, fl.ID); g.EffectiveIntervalSeconds != 30 {
		t.Fatalf("应钳到 min_interval 30s: %d", g.EffectiveIntervalSeconds)
	}
	if g, _ := f.svc.Get(bg, pl.ID); g.EffectiveIntervalSeconds != 5 {
		t.Fatalf("应钳到全局下限 5s: %d", g.EffectiveIntervalSeconds)
	}
	// 复制出来的实例同样按下限生效
	c, err := f.svc.Copy(bg, fl.ID, "副本")
	if err != nil {
		t.Fatal(err)
	}
	if c.EffectiveIntervalSeconds != 30 {
		t.Fatalf("副本应钳到 min_interval: %d", c.EffectiveIntervalSeconds)
	}
}

func execErr(_ any, err error) error { return err }

// 单行配置 JSON 损坏：其余实例照常列出与调度，坏行以 broken 呈现。
func TestCorruptRowSkippedNotFatal(t *testing.T) {
	f := newFx(t)
	good := f.create("probe", "good", probeCfg("g"))
	bad := f.create("probe", "bad", probeCfg("b"))
	must(t, execErr(f.db.Exec(`UPDATE plugin_instances SET config_json = '{oops' WHERE id = ?`, bad.ID)))
	list, err := f.svc.List(bg)
	if err != nil || len(list) != 2 {
		t.Fatalf("List 不应因单行损坏整体失败: %v %v", list, err)
	}
	for _, in := range list {
		if in.ID == bad.ID && (in.DisplayState != "broken" || in.Issue == "") {
			t.Fatalf("损坏的实例应为 broken 且带原因: %+v", in)
		}
	}
	if g, err := f.svc.Get(bg, bad.ID); err != nil || g.DisplayState != "broken" {
		t.Fatalf("Get 损坏的实例: %+v %v", g.Instance, err)
	}
	f.start()
	if !f.svc.isScheduled(good.ID) || f.svc.isScheduled(bad.ID) {
		t.Fatal("正常实例应调度，损坏的不调度")
	}
}

// notifier 插件不是数据源，不能建实例。
func TestNotifierCannotHaveInstances(t *testing.T) {
	f := newFx(t)
	var fe model.FieldErrors
	_, err := f.svc.Create(bg, model.InstanceInput{PluginID: "notif", Name: "n", Config: map[string]any{}})
	if !errors.As(err, &fe) || fe["plugin_id"] != model.FieldInvalid {
		t.Fatalf("Create notifier 应报 plugin_id invalid: %v", err)
	}
	must(t, execErr(f.db.Exec(`INSERT INTO plugin_instances (id, plugin_id, name, config_json, config_hash, created_at, updated_at)
VALUES ('n1', 'notif', 'n', '{}', 'h', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)))
	if _, err := f.svc.Copy(bg, "n1", "副本"); !errors.As(err, &fe) || fe["plugin_id"] != model.FieldInvalid {
		t.Fatalf("Copy notifier 实例应报 plugin_id invalid: %v", err)
	}
}

// 实例引用的代理已不存在：不退回直连，本次运行失败，展示为 error（Ruling 49）。
func TestRemovedProxyFailsRun(t *testing.T) {
	f := newFx(t)
	d := f.create("probe", "a", probeCfg("a"))
	must(t, execErr(f.db.Exec(`UPDATE plugin_instances SET config_json=?, proxy_id='gone' WHERE id=?`, `{"host":"a","proxy":"gone"}`, d.ID)))
	_, err := f.svc.Run(bg, d.ID)
	if !errors.Is(err, runtime.ErrFailed) || !strings.Contains(err.Error(), "代理已删除") {
		t.Fatalf("代理已删除应运行失败: %v", err)
	}
	if f.probe.calls() != 0 {
		t.Fatal("代理已删除时不得直连运行插件")
	}
	g, _ := f.svc.Get(bg, d.ID)
	if g.DisplayState != "error" || !strings.Contains(g.LastError, "proxy removed") {
		t.Fatalf("应展示为 error 并带原因: %+v", g.Instance)
	}

	// 定时路径同样失败而不直连
	f.start()
	if !f.svc.isScheduled(d.ID) {
		t.Fatal("代理已删除的实例仍排程（每次运行失败）")
	}
	waitFor(t, "定时运行记为失败", func() bool {
		g, _ := f.svc.Get(bg, d.ID)
		return g.Failures >= 2
	})
	if f.probe.calls() != 0 {
		t.Fatal("定时路径不得直连运行插件")
	}
}
