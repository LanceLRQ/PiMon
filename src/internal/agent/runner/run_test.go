package runner

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

func TestRun_state往返与报告序号(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	first := src.next(t)
	if first.in.State != "" || first.in.Last != nil {
		t.Fatalf("首次运行 state=%q last=%v", first.in.State, first.in.Last)
	}
	r1 := expect[*agent.Report](t, s)
	if r1.InstanceID != "a" || r1.Seq != 1 || r1.ConfigHash != "h1" || r1.Report == nil {
		t.Fatalf("第一份报告 = %+v", r1)
	}
	if r1.Report.State != "" {
		t.Fatal("报告不得带 state")
	}
	if v, ok := state(t, f.store, "a"); !ok || v != "|a" {
		t.Fatalf("本地 state = %q %v", v, ok)
	}

	f.clk.Advance(60 * time.Second)
	second := src.next(t)
	if second.in.State != "|a" || second.in.Last == nil || second.in.Last.Summary != "a" {
		t.Fatalf("第二次运行应拿到第一次写入的 state 与上次报告，state=%q last=%+v", second.in.State, second.in.Last)
	}
	if r2 := expect[*agent.Report](t, s); r2.Seq != 2 || r2.ConfigHash != "h1" {
		t.Fatalf("第二份报告 = %+v", r2)
	}

	// 重新下发（配置变化）后序号继续递增，ConfigHash 换成新值
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{inst("a", "a", "h2")}})
	third := src.next(t)
	if third.in.State != "" || third.in.Last != nil {
		t.Fatalf("配置变化后应清空 state 与上次报告（Ruling 5），state=%q last=%v", third.in.State, third.in.Last)
	}
	if r3 := expect[*agent.Report](t, s); r3.Seq != 3 || r3.ConfigHash != "h2" {
		t.Fatalf("第三份报告 = %+v", r3)
	}

	// 移出 assign 再加回：同一会话内序号仍然递增
	b.Assign(&agent.Assign{Version: 3})
	b.Assign(&agent.Assign{Version: 4, Instances: []agent.AssignedInstance{inst("a", "a", "h2")}})
	if c := src.next(t); c.in.Last != nil {
		t.Fatal("移出 assign 后上次报告应被丢弃")
	}
	if r4 := expect[*agent.Report](t, s); r4.Seq != 4 {
		t.Fatalf("加回后报告序号 = %d", r4.Seq)
	}
}

func TestRun_失败也发报告且错误已脱敏(t *testing.T) {
	src := newCountSource(t)
	msg := "连接失败 token=SECRET-VALUE"
	src.failWith.Store(&msg)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	a := inst("a", "a", "h1")
	a.Secrets = map[string]string{"token": "SECRET-VALUE"}
	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{a}})
	r := expect[*agent.Report](t, s)
	if r.Report != nil || r.ErrorKind != agent.ErrorKindFailed || r.Error == "" || r.ConfigHash != "h1" || r.Seq != 1 {
		t.Fatalf("失败报告 = %+v", r)
	}
	if strings.Contains(r.Error, "SECRET-VALUE") {
		t.Fatalf("错误文字泄露密钥: %s", r.Error)
	}
	if _, ok := state(t, f.store, "a"); ok {
		t.Fatal("失败不应写 state")
	}
}

func TestRun_下发超时到期归为timeout(t *testing.T) {
	src := newCountSource(t)
	src.block.Store(true) // 只会因 ctx 截止而返回
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	a := inst("a", "a", "h1")
	a.TimeoutMs = 30 // manifest 超时为 30 秒，这里由下发的 timeout_ms 生效
	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{a}})
	r := expect[*agent.Report](t, s)
	if r.ErrorKind != agent.ErrorKindTimeout || r.Error == "" {
		t.Fatalf("超时报告 = %+v", r)
	}
}

func TestState_移出assign保留且30天未出现才清理(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)
	ctx := context.Background()

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "ha"), inst("k", "k", "hk")}})
	expect[*agent.Report](t, s)
	expect[*agent.Report](t, s)

	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{inst("k", "k", "hk")}})
	if _, ok := state(t, f.store, "a"); !ok {
		t.Fatal("移出 assign 不应删除 state")
	}

	f.clk.Advance(29 * 24 * time.Hour)
	if n, err := f.r.GC(ctx); err != nil || n != 0 {
		t.Fatalf("29 天时 GC = %d %v", n, err)
	}
	f.clk.Advance(2 * 24 * time.Hour)
	if n, err := f.r.GC(ctx); err != nil || n != 1 {
		t.Fatalf("31 天时 GC = %d %v", n, err)
	}
	if _, ok := state(t, f.store, "a"); ok {
		t.Fatal("30 天未出现在 assign 里的 state 应被清理")
	}
	if _, ok := state(t, f.store, "k"); !ok {
		t.Fatal("仍在 assign 里的实例即使很久没重新下发也不应清理")
	}
}

func TestClose_断线后不再运行且内存中无密钥(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	a := inst("a", "a", "h1")
	a.Secrets = map[string]string{"token": "SECRET-VALUE"}
	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{a, inst("q", "q", "hq")}})
	expect[*agent.Report](t, s)
	expect[*agent.Report](t, s)
	src.next(t) // 两次周期调用已结束
	src.next(t)

	// 一个运行中的 run_now：Close 必须等它退出。插件收到取消后再拖 100ms 才返回，
	// 这期间 Done 若已关闭，说明 Close 没有等它。
	var closedEarly atomic.Bool
	hook := func() {
		select {
		case <-b.Done():
			closedEarly.Store(true)
		case <-time.After(100 * time.Millisecond):
		}
	}
	src.onCancel.Store(&hook)
	src.block.Store(true)
	b.RunNow(&agent.RunNow{RequestID: "r", InstanceID: "q"})
	inflight := src.next(t)

	s.disconnect()
	select {
	case <-b.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("断线后 Binding 未关闭")
	}
	// 等插件调用全部退出后再看钩子的观察（Close 正确时此刻已经全部退出）
	waitUntil(t, func() bool { return int64(src.count("a")+src.count("q")) == src.finished.Load() })
	if closedEarly.Load() {
		t.Fatal("Close 在 run_now 的插件退出前就完成了")
	}
	if inflight.ctx.Err() == nil {
		t.Fatal("断线应取消运行中的 run_now")
	}
	if started, done := src.count("a")+src.count("q"), src.finished.Load(); int64(started) != done {
		t.Fatalf("Close 返回时仍有插件调用未退出：开始 %d，结束 %d", started, done)
	}
	b.mu.Lock()
	specs, sched, streams := len(b.specs), b.sched, b.streams
	b.mu.Unlock()
	if specs != 0 || sched != nil || streams != nil {
		t.Fatalf("断线后应清空配置与密钥并释放调度器：specs=%d sched=%v streams=%v", specs, sched, streams)
	}

	before := src.count("a")
	f.clk.Advance(10 * time.Minute)
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{a}})
	b.RunNow(&agent.RunNow{RequestID: "r2", InstanceID: "a"})
	f.clk.Advance(10 * time.Minute)
	if src.count("a") != before {
		t.Fatal("断线后不应再运行")
	}
	for _, m := range s.messages() {
		if rr, ok := m.(*agent.RunResult); ok && rr.RequestID == "r2" {
			t.Fatal("关闭后不应再回复")
		}
		if ar, ok := m.(*agent.AssignResult); ok && ar.Version == 2 {
			t.Fatal("关闭后不应再应用 assign")
		}
	}
}

func TestRunNow_成功只回run_result(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, time.Hour, src) // 首次周期运行推迟 1 小时，不干扰
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	expect[*agent.AssignResult](t, s)
	b.RunNow(&agent.RunNow{RequestID: "req-1", InstanceID: "a"})
	rr := expect[*agent.RunResult](t, s)
	if rr.RequestID != "req-1" || rr.InstanceID != "a" || rr.Report == nil || rr.ConfigHash != "h1" || rr.Report.State != "" {
		t.Fatalf("run_result = %+v", rr)
	}
	if v, _ := state(t, f.store, "a"); v != "|a" {
		t.Fatalf("手动运行也应写 state，得到 %q", v)
	}
	b.Close()
	for _, m := range s.messages() {
		if _, ok := m.(*agent.Report); ok {
			t.Fatal("手动运行的结果不应再发 report")
		}
	}
}

func TestRunNow_忙与未下发(t *testing.T) {
	src := newCountSource(t)
	src.block.Store(true)
	f := newFixture(t, time.Hour, src)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	expect[*agent.AssignResult](t, s)
	b.RunNow(&agent.RunNow{RequestID: "r1", InstanceID: "a"})
	src.next(t)

	// 持锁运行中：第二次立即回 busy（在调用内同步发出）
	b.RunNow(&agent.RunNow{RequestID: "r2", InstanceID: "a"})
	msgs := s.messages()
	busy, ok := msgs[len(msgs)-1].(*agent.RunResult)
	if !ok || busy.RequestID != "r2" || busy.ErrorKind != agent.ErrorKindBusy || busy.Error == "" {
		t.Fatalf("应同步回 busy，最后一条消息 = %+v", msgs[len(msgs)-1])
	}
	expect[*agent.RunResult](t, s) // 即 busy

	b.RunNow(&agent.RunNow{RequestID: "r3", InstanceID: "ghost"})
	msgs = s.messages()
	na, ok := msgs[len(msgs)-1].(*agent.RunResult)
	if !ok || na.RequestID != "r3" || na.ErrorKind != agent.ErrorKindNotAssigned || na.Error == "" {
		t.Fatalf("应同步回 not_assigned，最后一条消息 = %+v", msgs[len(msgs)-1])
	}
	expect[*agent.RunResult](t, s)

	close(src.gate)
	if r1 := expect[*agent.RunResult](t, s); r1.RequestID != "r1" || r1.Report == nil {
		t.Fatalf("r1 = %+v", r1)
	}
}

// streamSource 是事件驱动型测试插件：启动时 emit 一份带 state 的报告，然后等 ctx 结束。
type streamSource struct {
	m      *manifest.Manifest
	inputs chan runtime.Input
}

func (s *streamSource) Manifest() *manifest.Manifest { return s.m }
func (s *streamSource) Collect(context.Context, runtime.Input) (*report.Report, error) {
	return &report.Report{Status: report.StatusOK}, nil
}
func (s *streamSource) Run(ctx context.Context, in runtime.Input, emit func(*report.Report)) error {
	s.inputs <- in
	emit(&report.Report{Status: report.StatusOK, State: in.State + "+"})
	<-ctx.Done()
	return nil
}

func TestStream_报告带序号与ConfigHash并写state(t *testing.T) {
	ss := &streamSource{m: parseManifest(t, testManifest), inputs: make(chan runtime.Input, 10)}
	f := newFixture(t, 0, ss)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	in := <-ss.inputs
	if in.Config["name"] != "a" {
		t.Fatalf("Streamer 输入 = %+v", in)
	}
	r := expect[*agent.Report](t, s)
	if r.Seq != 1 || r.ConfigHash != "h1" || r.Report == nil || r.Report.State != "" {
		t.Fatalf("报告 = %+v", r)
	}
	if v, _ := state(t, f.store, "a"); v != "+" {
		t.Fatalf("state = %q", v)
	}
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{inst("a", "a", "h2")}})
	if in := <-ss.inputs; in.State != "" || in.Last != nil {
		t.Fatalf("配置变化重启后应清空 state 与上次报告，state=%q last=%v", in.State, in.Last)
	}
	if r := expect[*agent.Report](t, s); r.Seq != 2 || r.ConfigHash != "h2" {
		t.Fatalf("重启后报告 = %+v", r)
	}
}

func TestAssign_配置哈希变化清空上次报告与state(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "ha"), inst("k", "k", "hk")}})
	expect[*agent.Report](t, s)
	expect[*agent.Report](t, s)
	src.next(t)
	src.next(t)

	// a 的配置变化、k 未变：a 立即按新配置重跑，拿不到旧的上次报告与 state
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{inst("a", "a", "ha2"), inst("k", "k", "hk")}})
	c := src.next(t)
	if c.name != "a" || c.in.State != "" || c.in.Last != nil {
		t.Fatalf("配置变化后的首次运行 = name %q state %q last %v", c.name, c.in.State, c.in.Last)
	}
	expect[*agent.Report](t, s)

	// k 未变：下次周期运行仍拿到原来的 state 与上次报告
	// （a 的新循环可能还没登记下一次计时，这里只看 k）
	f.clk.Advance(60 * time.Second)
	for {
		c := src.next(t)
		if c.name != "k" {
			continue
		}
		if c.in.State != "|k" || c.in.Last == nil {
			t.Fatalf("未变的实例应保留 state 与上次报告，state=%q last=%v", c.in.State, c.in.Last)
		}
		break
	}
}

func TestAssign_配置哈希变化时删除本地state(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, time.Hour, src) // 周期运行推迟，便于观察删除本身
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "ha"), inst("k", "k", "hk")}})
	expect[*agent.AssignResult](t, s)
	for _, id := range []string{"a", "k"} {
		b.RunNow(&agent.RunNow{RequestID: id, InstanceID: id})
		expect[*agent.RunResult](t, s)
	}
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{inst("a", "a", "ha2"), inst("k", "k", "hk")}})
	if _, ok := state(t, f.store, "a"); ok {
		t.Fatal("配置变化后应删除本地 state")
	}
	if v, ok := state(t, f.store, "k"); !ok || v != "|k" {
		t.Fatalf("未变实例的 state = %q %v", v, ok)
	}
	// 移出 assign 仍不删 state（Ruling 1）
	b.Assign(&agent.Assign{Version: 3})
	if _, ok := state(t, f.store, "k"); !ok {
		t.Fatal("移出 assign 不应删除 state")
	}
}

func TestAssign_只改间隔或代理不清空state与上次报告(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	expect[*agent.Report](t, s)
	src.next(t)

	// 下发摘要变了（间隔、代理），内容摘要没变：按新配置重排，但 state 与上次报告保留
	changed := inst("a", "a", "h2")
	changed.ContentHash = "c:h1"
	changed.IntervalMs = 120000
	changed.ProxyURL = "socks5://127.0.0.1:1080"
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{changed}})
	c := src.next(t)
	if c.in.State != "|a" || c.in.Last == nil {
		t.Fatalf("内容未变时应保留 state 与上次报告，state=%q last=%v", c.in.State, c.in.Last)
	}
	expect[*agent.Report](t, s) // 等这次运行写完 state
	if v, ok := state(t, f.store, "a"); !ok || v != "|a|a" {
		t.Fatalf("本地 state = %q %v", v, ok)
	}
}

func TestAssign_内容摘要为空视为未知不清空(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	a := inst("a", "a", "h1")
	a.ContentHash = ""
	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{a}})
	expect[*agent.Report](t, s)
	src.next(t)
	a2 := inst("a", "a", "h2")
	a2.ContentHash = ""
	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{a2}})
	if c := src.next(t); c.in.State != "|a" || c.in.Last == nil {
		t.Fatalf("摘要未知时不应清空，state=%q last=%v", c.in.State, c.in.Last)
	}
}

func TestAssign_新会话收到不同内容摘要时清空(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, 0, src)
	b1, s1 := f.attach(t)

	b1.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1"), inst("k", "k", "hk")}})
	expect[*agent.Report](t, s1)
	expect[*agent.Report](t, s1)
	src.next(t)
	src.next(t)
	s1.disconnect()
	<-b1.Done()

	b2, s2 := f.attach(t)
	b2.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h2"), inst("k", "k", "hk")}})
	for range 2 {
		c := src.next(t)
		switch c.name {
		case "a":
			if c.in.State != "" || c.in.Last != nil {
				t.Fatalf("新会话里内容变化的实例应清空，state=%q last=%v", c.in.State, c.in.Last)
			}
		case "k":
			if c.in.State != "|k" || c.in.Last == nil {
				t.Fatalf("新会话里内容未变的实例应保留，state=%q last=%v", c.in.State, c.in.Last)
			}
		}
	}
	expect[*agent.Report](t, s2)
}

func TestAssign_进程重启后按库中内容摘要删除state(t *testing.T) {
	src := newCountSource(t)
	db := filepath.Join(t.TempDir(), "agent.db")
	f1 := newFixtureAt(t, db, 0, src)
	b1, s1 := f1.attach(t)
	b1.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1"), inst("k", "k", "hk")}})
	expect[*agent.Report](t, s1)
	expect[*agent.Report](t, s1)
	b1.Close()
	_ = f1.store.Close()

	// 新进程：内存为空，只有库
	f2 := newFixtureAt(t, db, time.Hour, src)
	b2, s2 := f2.attach(t)
	b2.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h2"), inst("k", "k", "hk")}})
	expect[*agent.AssignResult](t, s2)
	if _, ok := state(t, f2.store, "a"); ok {
		t.Fatal("库中内容摘要与下发不同，应删除 state")
	}
	if v, ok := state(t, f2.store, "k"); !ok || v != "|k" {
		t.Fatalf("内容摘要相同应保留 state，得到 %q %v", v, ok)
	}
}

func TestRunNow_配置替换后旧运行不写回(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, time.Hour, src)
	b, s := f.attach(t)
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(src.gate) }) }
	t.Cleanup(openGate) // 先于 Binding.Close 执行：断言失败时也不会卡在不理会取消的插件上

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	expect[*agent.AssignResult](t, s)
	src.block.Store(true)
	src.ignoreCancel.Store(true) // 插件不理会取消，放行后照常返回成功
	b.RunNow(&agent.RunNow{RequestID: "old", InstanceID: "a"})
	old := src.next(t)

	b.Assign(&agent.Assign{Version: 2, Instances: []agent.AssignedInstance{inst("a", "a", "h2")}})
	if old.ctx.Err() == nil {
		t.Fatal("配置替换应取消旧配置的 run_now")
	}
	src.block.Store(false)
	openGate()
	// 等旧运行完全结束（插件返回后还要释放运行锁）
	waitUntil(t, func() bool {
		release, ok := b.r.tryAcquire("a")
		if ok {
			release()
		}
		return src.finished.Load() == 1 && ok
	})

	b.RunNow(&agent.RunNow{RequestID: "new", InstanceID: "a"})
	c := src.next(t)
	if c.in.State != "" || c.in.Last != nil {
		t.Fatalf("旧运行的结果不应写回，新运行看到 state=%q last=%v", c.in.State, c.in.Last)
	}
	rr := expect[*agent.RunResult](t, s)
	if rr.RequestID != "new" {
		t.Fatalf("旧配置的 run_now 不应回复成功结果，收到 %+v", rr)
	}
}

func TestRunNow_移出assign取消运行(t *testing.T) {
	src := newCountSource(t)
	f := newFixture(t, time.Hour, src)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{inst("a", "a", "h1")}})
	expect[*agent.AssignResult](t, s)
	src.block.Store(true)
	b.RunNow(&agent.RunNow{RequestID: "r", InstanceID: "a"})
	c := src.next(t)
	b.Assign(&agent.Assign{Version: 2})
	if c.ctx.Err() == nil {
		t.Fatal("移出 assign 应取消运行中的 run_now")
	}
}
