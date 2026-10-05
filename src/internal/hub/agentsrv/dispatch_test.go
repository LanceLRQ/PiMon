package agentsrv

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// ---- 替身 ----

// fakeSender 记录发出的消息；err 非空时 Send 返回它（不记录）。
// Request 也记入 msgs（便于检查与 assign 的先后），应答由 reply 决定（为空时返回 ErrRequestTimeout）。
type fakeSender struct {
	mu    sync.Mutex
	msgs  []agent.Message
	err   error
	done  chan struct{}
	reply func(req agent.Requester, timeout time.Duration) (agent.Message, error)
}

func (s *fakeSender) Request(_ context.Context, req agent.Requester, timeout time.Duration) (agent.Message, error) {
	s.mu.Lock()
	if req.GetRequestID() == "" {
		req.(agent.RequestIDSetter).SetRequestID("req-1")
	}
	s.msgs = append(s.msgs, req.(agent.Message))
	reply := s.reply
	s.mu.Unlock()
	if reply == nil {
		return nil, agentlink.ErrRequestTimeout
	}
	return reply(req, timeout)
}

func (s *fakeSender) setReply(fn func(agent.Requester, time.Duration) (agent.Message, error)) {
	s.mu.Lock()
	s.reply = fn
	s.mu.Unlock()
}

func (s *fakeSender) all() []agent.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.msgs)
}

func newFakeSender() *fakeSender { return &fakeSender{done: make(chan struct{})} }

func (s *fakeSender) Send(m agent.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.msgs = append(s.msgs, m)
	return nil
}

func (s *fakeSender) Done() <-chan struct{} { return s.done }

func (s *fakeSender) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *fakeSender) assigns() []*agent.Assign {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*agent.Assign
	for _, m := range s.msgs {
		if a, ok := m.(*agent.Assign); ok {
			out = append(out, a)
		}
	}
	return out
}

// fakeInst 是实例服务替身。
type fakeInst struct {
	mu        sync.Mutex
	specs     map[string][]instances.RemoteSpec
	calls     map[string]int
	issues    map[string]string // machine/id → msg（清除时删除）
	issueLog  []string
	created   []model.InstanceInput
	createErr error
	// createCtxErr 记录每次 Create 调用时 ctx 的错误。
	createCtxErr []error
	applied      []applied
	// block 非空时 RemoteSpecs 等它关闭（模拟下发组装卡住）。
	block chan struct{}
}

// applied 是一次 ApplyRemoteResult 调用。
type applied struct {
	id, hash string
	rep      *report.Report
	err      error
}

func (f *fakeInst) ApplyRemoteResult(id, hash string, rep *report.Report, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, applied{id: id, hash: hash, rep: rep, err: err})
}

func (f *fakeInst) appliedList() []applied {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.applied)
}

func newFakeInst() *fakeInst {
	return &fakeInst{specs: map[string][]instances.RemoteSpec{}, calls: map[string]int{}, issues: map[string]string{}}
}

func (f *fakeInst) RemoteSpecs(_ context.Context, machine string) ([]instances.RemoteSpec, error) {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[machine]++
	return slices.Clone(f.specs[machine]), nil
}

func (f *fakeInst) SetAssignIssue(machine, id, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueLog = append(f.issueLog, machine+"/"+id+"="+msg)
	if msg == "" {
		delete(f.issues, machine+"/"+id)
		return
	}
	f.issues[machine+"/"+id] = msg
}

func (f *fakeInst) Create(ctx context.Context, in model.InstanceInput) (model.InstanceDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	f.createCtxErr = append(f.createCtxErr, ctx.Err())
	if f.createErr != nil {
		return model.InstanceDetail{}, f.createErr
	}
	return model.InstanceDetail{}, nil
}

func (f *fakeInst) setSpecs(machine string, specs ...instances.RemoteSpec) {
	f.mu.Lock()
	f.specs[machine] = specs
	f.mu.Unlock()
}

func (f *fakeInst) callCount(machine string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[machine]
}

func (f *fakeInst) issue(machine, id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.issues[machine+"/"+id]
	return m, ok
}

func (f *fakeInst) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

// fakeDevs 是设备仓库替身。
type fakeDevs struct {
	mu   sync.Mutex
	devs map[string]model.Device
	auto map[string]bool
	cbs  []func(string, bool)
	// onTake 非空时在 TakeAutoHostMetrics 返回前调用。
	onTake func()
}

func (f *fakeDevs) Get(_ context.Context, id string) (model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devs[id]
	if !ok {
		return model.Device{}, devices.ErrNotFound
	}
	return d, nil
}

func (f *fakeDevs) TakeAutoHostMetrics(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.auto[id]
	f.auto[id] = false
	if f.onTake != nil {
		f.onTake()
	}
	return v, nil
}

func (f *fakeDevs) OnChange(fn func(string, bool)) {
	f.mu.Lock()
	f.cbs = append(f.cbs, fn)
	f.mu.Unlock()
}

func (f *fakeDevs) fire(id string, removed bool) {
	f.mu.Lock()
	cbs := slices.Clone(f.cbs)
	f.mu.Unlock()
	for _, cb := range cbs {
		cb(id, removed)
	}
}

func (f *fakeDevs) autoFlag(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auto[id]
}

// fakePlugs 是按机器取插件的替身。
type fakePlugs struct {
	mu  sync.Mutex
	has map[string]bool // machine/id
}

func (f *fakePlugs) MachinePlugin(machine, id string) (plugins.Plugin, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.has[machine+"/"+id] {
		return plugins.Plugin{ID: id}, true
	}
	return plugins.Plugin{}, false
}

type dfx struct {
	clk   *clock.Fake
	inst  *fakeInst
	devs  *fakeDevs
	plugs *fakePlugs
	mu    sync.Mutex
	sess  map[string]*fakeSender
	d     *Dispatcher
}

func newDFX(t *testing.T, opts ...func(*DispatcherConfig)) *dfx {
	t.Helper()
	f := &dfx{
		clk: clock.NewFake(t0), inst: newFakeInst(),
		devs:  &fakeDevs{devs: map[string]model.Device{}, auto: map[string]bool{}},
		plugs: &fakePlugs{has: map[string]bool{}},
		sess:  map[string]*fakeSender{},
	}
	cfg := DispatcherConfig{
		Instances: f.inst, Devices: f.devs, Plugins: f.plugs, Clock: f.clk, Log: agentlinktest.QuietLog(),
		HubVersion: "0.2.0", WebPort: func() int { return 31415 },
	}
	for _, o := range opts {
		o(&cfg)
	}
	d, err := newDispatcher(cfg, f.session)
	must(t, err)
	f.d = d
	t.Cleanup(d.Close)
	return f
}

func (f *dfx) session(id string) (sender, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sess[id]
	if !ok {
		return nil, false
	}
	return s, true
}

func (f *dfx) online(id string) *fakeSender {
	s := newFakeSender()
	f.mu.Lock()
	f.sess[id] = s
	f.mu.Unlock()
	return s
}

func (f *dfx) offline(id string) {
	f.mu.Lock()
	delete(f.sess, id)
	f.mu.Unlock()
}

// timerArmed 返回合并计时是否已登记。
func (f *dfx) timerArmed() bool {
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	return f.d.timer != nil
}

// fired 等合并计时到期并被工作协程取走（计时复位与转入 due 在同一把锁内完成），再等处理完毕。
func (f *dfx) fired(t *testing.T) {
	t.Helper()
	waitFor(t, "合并计时被取走", func() bool { return !f.timerArmed() })
	f.idle(t)
}

// idle 等下发器把已登记的工作处理完（立即队列为空且工作协程不在处理中）。
func (f *dfx) idle(t *testing.T) {
	t.Helper()
	waitFor(t, "下发器空闲", func() bool {
		f.d.mu.Lock()
		defer f.d.mu.Unlock()
		return len(f.d.due) == 0 && !f.d.busy && len(f.d.wake) == 0
	})
}

func spec(id, plugin string) instances.RemoteSpec {
	return instances.RemoteSpec{
		ID: id, PluginID: plugin, Config: map[string]any{"host": id}, Secrets: map[string]string{"token": "s-" + id},
		ProxyURL: "http://u:pw@127.0.0.1:8080", Interval: 45 * time.Second, Timeout: 7 * time.Second, ConfigHash: "h-" + id,
		ContentHash: "c-" + id,
	}
}

func waitAssigns(t *testing.T, s *fakeSender, n int) []*agent.Assign {
	t.Helper()
	waitFor(t, fmt.Sprintf("收到 %d 个 assign", n), func() bool { return len(s.assigns()) >= n })
	return s.assigns()
}

// ---- 用例 ----

func TestDispatcher_OnReady立即全量下发(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"), spec("i2", "p2"))

	f.d.OnReady("dev1")
	as := waitAssigns(t, s, 1)
	a := as[0]
	if a.Version != 1 || len(a.Instances) != 2 {
		t.Fatalf("assign = version %d, %d 个实例", a.Version, len(a.Instances))
	}
	got := a.Instances[0]
	if got.ID != "i1" || got.PluginID != "p1" || got.Config["host"] != "i1" || got.Secrets["token"] != "s-i1" ||
		got.ProxyURL != "http://u:pw@127.0.0.1:8080" || got.IntervalMs != 45000 || got.TimeoutMs != 7000 || got.ConfigHash != "h-i1" ||
		got.ContentHash != "c-i1" {
		t.Fatalf("实例转换不对: %v", got)
	}
	if h, ok := f.d.SentHash("dev1", "i2"); !ok || h != "h-i2" {
		t.Fatalf("SentHash = %q %v", h, ok)
	}
	if _, ok := f.d.SentHash("dev1", "nope"); ok {
		t.Fatal("未下发的实例不应有 hash")
	}
	if f.timerArmed() {
		t.Fatal("OnReady 不应登记合并计时")
	}
}

func TestDispatcher_变更合并1秒且版本递增(t *testing.T) {
	f := newDFX(t)
	s1 := f.online("dev1")
	s2 := f.online("dev2")
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.inst.setSpecs("dev2", spec("j1", "p1"))
	f.d.OnReady("dev1")
	waitAssigns(t, s1, 1)

	for range 3 {
		f.d.MachineChanged("dev1")
		f.d.MachineChanged("dev2")
	}
	if !f.timerArmed() {
		t.Fatal("变更后应登记合并计时")
	}
	if w := f.clk.Waiters(); w != 1 {
		t.Fatalf("多次变更只应登记一个计时，得到 %d", w)
	}
	f.clk.Advance(999 * time.Millisecond)
	f.idle(t)
	if len(s1.assigns()) != 1 || len(s2.assigns()) != 0 {
		t.Fatal("合并窗口未到不应下发")
	}
	f.inst.setSpecs("dev1", spec("i1", "p1"), spec("i3", "p1"))
	f.clk.Advance(time.Millisecond)
	as1 := waitAssigns(t, s1, 2)
	as2 := waitAssigns(t, s2, 1)
	f.fired(t)
	if len(s1.assigns()) != 2 || len(s2.assigns()) != 1 {
		t.Fatalf("窗口内多次变更只应各下发一次: dev1=%d dev2=%d", len(s1.assigns()), len(s2.assigns()))
	}
	if as1[1].Version != 2 || len(as1[1].Instances) != 2 || as2[0].Version != 1 {
		t.Fatalf("版本按设备递增: dev1 v%d(%d 个) dev2 v%d", as1[1].Version, len(as1[1].Instances), as2[0].Version)
	}
	if f.timerArmed() {
		t.Fatal("到期后计时应复位")
	}

	// 下一轮变更重新登记计时
	f.d.MachineChanged("dev1")
	f.clk.Advance(time.Second)
	if as := waitAssigns(t, s1, 3); as[2].Version != 3 {
		t.Fatalf("第三次下发版本 = %d", as[2].Version)
	}
}

func TestDispatcher_离线设备不下发_上线后全量(t *testing.T) {
	f := newDFX(t)
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.d.MachineChanged("dev1")
	f.clk.Advance(time.Second)
	f.fired(t)
	if n := f.inst.callCount("dev1"); n != 0 {
		t.Fatalf("离线设备不应组装下发，调用 %d 次", n)
	}
	s := f.online("dev1")
	f.d.OnReady("dev1")
	if as := waitAssigns(t, s, 1); as[0].Version != 1 {
		t.Fatalf("上线后首个版本 = %d", as[0].Version)
	}
	// 断开后重新上线：版本继续递增（agent 在新会话里接受任何版本）
	f.offline("dev1")
	s2 := f.online("dev1")
	f.d.OnReady("dev1")
	if as := waitAssigns(t, s2, 1); as[0].Version != 2 {
		t.Fatalf("重新上线的版本 = %d", as[0].Version)
	}
}

func TestDispatcher_OnReady取消待合并的同设备变更(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.d.MachineChanged("dev1")
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)
	f.clk.Advance(time.Second)
	f.fired(t)
	if n := len(s.assigns()); n != 1 {
		t.Fatalf("OnReady 已全量下发，待合并的变更不应再发，得到 %d 次", n)
	}
}

func TestDispatcher_assign_result按版本写入问题(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"), spec("i2", "p2"), spec("i3", "p3"), spec("i4", "p4"))
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)
	f.d.OnReady("dev1")
	waitAssigns(t, s, 2)

	// 旧版本的回执忽略
	f.d.OnAssignResult("dev1", &agent.AssignResult{Version: 1, Results: []agent.AssignResultItem{
		{InstanceID: "i1", Status: agent.AssignPluginMissing, Message: "old"},
	}})
	if _, ok := f.inst.issue("dev1", "i1"); ok {
		t.Fatal("旧版本回执不应写入")
	}

	f.d.OnAssignResult("dev1", &agent.AssignResult{Version: 2, Results: []agent.AssignResultItem{
		{InstanceID: "i1", Status: agent.AssignPluginMissing, Message: "no such plugin"},
		{InstanceID: "i2", Status: agent.AssignInvalidConfig, Message: "bad host"},
		{InstanceID: "i3", Status: agent.AssignIncompatible},
		{InstanceID: "i4", Status: agent.AssignStarted},
		{InstanceID: "ghost", Status: agent.AssignPluginMissing, Message: "x"},
	}})
	want := map[string]string{
		"i1": "设备上没有该插件：no such plugin",
		"i2": "设备拒绝配置：bad host",
		"i3": "设备上的插件版本不兼容",
	}
	for id, w := range want {
		if got, ok := f.inst.issue("dev1", id); !ok || got != w {
			t.Fatalf("%s 的问题 = %q（%v），应为 %q", id, got, ok, w)
		}
	}
	if _, ok := f.inst.issue("dev1", "i4"); ok {
		t.Fatal("started 不应留下问题")
	}
	if _, ok := f.inst.issue("dev1", "ghost"); ok {
		t.Fatal("未下发给该设备的实例不应写入问题")
	}
	if !slices.Contains(f.inst.issueLog, "dev1/i4=") {
		t.Fatalf("started 应清除问题: %v", f.inst.issueLog)
	}

	// started 清除已有问题
	f.d.OnAssignResult("dev1", &agent.AssignResult{Version: 2, Results: []agent.AssignResultItem{
		{InstanceID: "i1", Status: agent.AssignStarted},
	}})
	if _, ok := f.inst.issue("dev1", "i1"); ok {
		t.Fatal("started 应清除 i1 的问题")
	}

	// 别的设备的回执不认（该设备没有下发过）
	f.d.OnAssignResult("dev2", &agent.AssignResult{Version: 2, Results: []agent.AssignResultItem{
		{InstanceID: "i2", Status: agent.AssignStarted},
	}})
	if _, ok := f.inst.issue("dev1", "i2"); !ok {
		t.Fatal("别的设备的回执不应影响本设备实例")
	}
}

func TestDispatcher_回执消息截断(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)
	f.d.OnAssignResult("dev1", &agent.AssignResult{Version: 1, Results: []agent.AssignResultItem{
		{InstanceID: "i1", Status: agent.AssignInvalidConfig, Message: strings.Repeat("长", 2000)},
	}})
	got, _ := f.inst.issue("dev1", "i1")
	if len(got) > maxIssueMessage+64 {
		t.Fatalf("回执消息应截断，长度 %d", len(got))
	}
}

func TestDispatcher_超过单帧上限标记全部实例(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)

	f.inst.setSpecs("dev1", spec("i1", "p1"), spec("i2", "p1"))
	s.setErr(fmt.Errorf("%w：assign 编码后 9000000 字节", agentlink.ErrFrameTooLarge))
	f.d.OnReady("dev1")
	waitFor(t, "标记下发过大", func() bool {
		_, ok := f.inst.issue("dev1", "i2")
		return ok
	})
	for _, id := range []string{"i1", "i2"} {
		if got, _ := f.inst.issue("dev1", id); !strings.Contains(got, "8 MiB") {
			t.Fatalf("%s 的问题 = %q", id, got)
		}
	}
	// 未送达的版本不算已下发：仍保留上一次成功下发的内容
	if _, ok := f.d.SentHash("dev1", "i2"); ok {
		t.Fatal("未送达的下发不应记入 lastSent")
	}
	if h, ok := f.d.SentHash("dev1", "i1"); !ok || h != "h-i1" {
		t.Fatal("应保留上一次成功下发的 hash")
	}
	// 未送达版本的回执不认
	f.d.OnAssignResult("dev1", &agent.AssignResult{Version: 2, Results: []agent.AssignResultItem{
		{InstanceID: "i1", Status: agent.AssignStarted},
	}})
	if _, ok := f.inst.issue("dev1", "i1"); !ok {
		t.Fatal("未送达版本的回执不应生效")
	}
	// 之后能送达时版本继续递增
	s.setErr(nil)
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.d.OnReady("dev1")
	if as := waitAssigns(t, s, 2); as[1].Version != 3 {
		t.Fatalf("送达的版本 = %d", as[1].Version)
	}
}

func TestDispatcher_首次上线自动创建hostmetrics(t *testing.T) {
	f := newDFX(t)
	f.devs.devs["dev1"] = model.Device{ID: "dev1", Name: "nas-box"}
	f.devs.auto["dev1"] = true
	s := f.online("dev1")

	// 设备没有上报 host-metrics：不创建，标记保留
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)
	if f.inst.createdCount() != 0 || !f.devs.autoFlag("dev1") {
		t.Fatal("未上报 host-metrics 时不应创建，标记应保留")
	}

	// plugins_changed 后上报了：创建一次，标记清零
	f.plugs.has["dev1/"+HostMetricsPluginID] = true
	f.d.OnReady("dev1")
	waitAssigns(t, s, 2)
	if f.inst.createdCount() != 1 || f.devs.autoFlag("dev1") {
		t.Fatalf("应创建一次并清零，创建 %d 次", f.inst.createdCount())
	}
	in := f.inst.created[0]
	if in.PluginID != HostMetricsPluginID || in.Name != "nas-box" || in.RunsOn != "dev1" || in.Config == nil {
		t.Fatalf("创建参数 = %+v", in)
	}
	f.d.OnReady("dev1")
	waitAssigns(t, s, 3)
	if f.inst.createdCount() != 1 {
		t.Fatal("不应重复创建")
	}
}

func TestDispatcher_自动创建失败只记警告不重试(t *testing.T) {
	f := newDFX(t)
	f.devs.devs["dev1"] = model.Device{ID: "dev1", Name: "nas-box"}
	f.devs.auto["dev1"] = true
	f.plugs.has["dev1/"+HostMetricsPluginID] = true
	f.inst.createErr = errors.New("名称冲突")
	s := f.online("dev1")
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)
	f.d.OnReady("dev1")
	waitAssigns(t, s, 2)
	if f.inst.createdCount() != 1 || f.devs.autoFlag("dev1") {
		t.Fatalf("失败后应清零且不重试，创建 %d 次", f.inst.createdCount())
	}
}

// Ruling 23：标记清零后开始关停，仍要把实例建成（Create 不随下发器 ctx 取消）。
func TestDispatcher_自动创建不受关停取消影响(t *testing.T) {
	f := newDFX(t)
	f.devs.devs["dev1"] = model.Device{ID: "dev1", Name: "nas-box"}
	f.devs.auto["dev1"] = true
	f.devs.onTake = f.d.cancel // 模拟标记刚清零时 Close 取消了 ctx
	f.plugs.has["dev1/"+HostMetricsPluginID] = true
	f.online("dev1")
	f.d.OnReady("dev1")
	waitFor(t, "自动创建", func() bool { return f.inst.createdCount() == 1 })
	f.inst.mu.Lock()
	defer f.inst.mu.Unlock()
	if err := f.inst.createCtxErr[0]; err != nil {
		t.Fatalf("Create 的 ctx 不应随关停取消: %v", err)
	}
}

func TestDispatcher_设备变更与删除(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"))
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)

	// 设备行变化（如重新配对确认迁移了实例）：投递一次合并下发
	f.devs.fire("dev1", false)
	if !f.timerArmed() {
		t.Fatal("设备变化应登记合并下发")
	}
	f.clk.Advance(time.Second)
	waitAssigns(t, s, 2)

	// 删除后清掉下发记录
	f.devs.fire("dev1", true)
	f.idle(t)
	if _, ok := f.d.SentHash("dev1", "i1"); ok {
		t.Fatal("设备删除后不应保留下发记录")
	}
}

func TestDispatcher_关闭后回调不阻塞(t *testing.T) {
	f := newDFX(t)
	f.online("dev1")
	f.d.Close()
	f.d.MachineChanged("dev1")
	f.d.OnReady("dev1")
	f.d.OnAssignResult("dev1", &agent.AssignResult{Version: 1})
	f.d.onReport(newFakeSender(), "dev1", &agent.Report{InstanceID: "i1", Seq: 1})
	f.d.Close()
	if f.inst.callCount("dev1") != 0 {
		t.Fatal("关闭后不应再下发")
	}
}

// ---- 端到端：真实 Listener + 真实实例服务 ----

// devFacts 用设备仓库适配实例服务的设备状态。
type devFacts struct{ s *devices.Store }

func (d devFacts) Device(id string) (instances.DeviceFacts, bool) {
	dev, err := d.s.Get(context.Background(), id)
	if err != nil {
		return instances.DeviceFacts{}, false
	}
	return instances.DeviceFacts{Role: dev.Role}, true
}

func TestDispatcher_端到端下发与回执(t *testing.T) {
	e := newEnv(t)
	box := mustBox(t)
	inst := instances.New(instances.Config{DB: e.db, Box: box, Clock: e.clk, Plugins: e.reg, Logger: agentlinktest.QuietLog()})
	inst.UseDevices(devFacts{e.devs})
	d, err := NewDispatcher(DispatcherConfig{
		Server: e.srv, Instances: inst, Devices: e.devs, Plugins: e.reg, Clock: e.clk, Log: agentlinktest.QuietLog(),
	})
	must(t, err)
	t.Cleanup(d.Close)
	inst.UseRemote(d)
	e.srv.UseRouter(d)

	code := e.newCode(t, devices.PairingOptions{Name: "nas-box", Role: model.DeviceRoleNAS})
	p, err := e.pair(t, code)
	must(t, err)
	p.helloOK(t,
		agent.PluginManifest{Origin: "builtin", Manifest: agentManifestJSON(t, HostMetricsPluginID, "1.0.0")},
		agent.PluginManifest{Origin: "exec", Manifest: agentManifestJSON(t, "probe-agent", "2.0.0")},
	)

	// 上线：自动创建 host-metrics 实例并立即全量下发
	a1 := recvAssign(t, p)
	if a1.Version != 1 || len(a1.Instances) != 1 || a1.Instances[0].PluginID != HostMetricsPluginID {
		t.Fatalf("首个 assign = v%d %v", a1.Version, a1.Instances)
	}
	hmID := a1.Instances[0].ID
	if v, err := inst.View(context.Background(), hmID); err != nil || v.Name != "nas-box" || v.RunsOn != p.id {
		t.Fatalf("自动创建的实例 = %+v（%v）", v, err)
	}

	// 新建实例：合并窗口到期后收到新版本
	created, err := inst.Create(context.Background(), model.InstanceInput{
		PluginID: "probe-agent", Name: "probe", RunsOn: p.id, Config: map[string]any{},
	})
	must(t, err)
	waitFor(t, "合并计时登记", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.timer != nil
	})
	e.clk.Advance(AssignWindow)
	a2 := recvAssign(t, p)
	if a2.Version != 2 || len(a2.Instances) != 2 {
		t.Fatalf("新建后 assign = v%d，%d 个实例", a2.Version, len(a2.Instances))
	}
	if h, ok := d.SentHash(p.id, created.ID); !ok || h == "" {
		t.Fatal("新实例应记入 lastSent")
	}

	// 暂停：实例从 assign 中消失
	_, err = inst.Pause(context.Background(), created.ID)
	must(t, err)
	e.clk.Advance(AssignWindow)
	a3 := recvAssign(t, p)
	if a3.Version != 3 || len(a3.Instances) != 1 || a3.Instances[0].ID != hmID {
		t.Fatalf("暂停后 assign = v%d %v", a3.Version, a3.Instances)
	}

	// 回执失败原因出现在实例视图，started 清除
	must(t, p.sess.Send(&agent.AssignResult{Version: 3, Results: []agent.AssignResultItem{
		{InstanceID: hmID, Status: agent.AssignInvalidConfig, Message: "temp_warn 无效"},
	}}))
	waitFor(t, "回执问题出现在实例详情", func() bool {
		dt, err := inst.Get(context.Background(), hmID)
		return err == nil && dt.Issue == "设备拒绝配置：temp_warn 无效" && dt.DisplayState == "broken"
	})
	must(t, p.sess.Send(&agent.AssignResult{Version: 3, Results: []agent.AssignResultItem{
		{InstanceID: hmID, Status: agent.AssignStarted},
	}}))
	waitFor(t, "started 清除问题", func() bool {
		v, err := inst.View(context.Background(), hmID)
		return err == nil && v.Issue == ""
	})
}

func recvAssign(t *testing.T, p *peer) *agent.Assign {
	t.Helper()
	for {
		m := p.recv(t)
		if a, ok := m.(*agent.Assign); ok {
			return a
		}
	}
}

func mustBox(t *testing.T) *secret.Box {
	t.Helper()
	box, err := secret.New(make([]byte, 32))
	must(t, err)
	return box
}
