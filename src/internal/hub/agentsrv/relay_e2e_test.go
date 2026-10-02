package agentsrv

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// recHist 记录写入历史的报告摘要。
type recHist struct {
	mu   sync.Mutex
	sums []string
}

func (h *recHist) Record(id string, _ time.Time, rep *report.Report) {
	h.mu.Lock()
	h.sums = append(h.sums, id+"|"+rep.Summary)
	h.mu.Unlock()
}

func (h *recHist) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.sums...)
}

// recChanges 记录实例变化通知（app 里接到 ws.NotifyInstance，即 UI patch）。
type recChanges struct {
	mu  sync.Mutex
	ids map[string]int
}

func (r *recChanges) add(id string) {
	r.mu.Lock()
	r.ids[id]++
	r.mu.Unlock()
}

func (r *recChanges) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ids[id]
}

// relayEnv 是真实 Listener + 真实实例服务 + 下发器。
type relayEnv struct {
	*env
	inst    *instances.Service
	d       *Dispatcher
	hist    *recHist
	changes *recChanges
}

func newRelayEnv(t *testing.T) *relayEnv {
	t.Helper()
	e := newEnv(t)
	r := &relayEnv{env: e, hist: &recHist{}, changes: &recChanges{ids: map[string]int{}}}
	r.inst = instances.New(instances.Config{
		DB: e.db, Box: mustBox(t), Clock: e.clk, Plugins: e.reg, History: r.hist, Logger: agentlinktest.QuietLog(),
	})
	r.inst.UseDevices(devFacts{e.devs})
	r.inst.OnChange(r.changes.add)
	var err error
	r.d, err = NewDispatcher(DispatcherConfig{
		Server: e.srv, Instances: r.inst, Devices: e.devs, Plugins: e.reg, Clock: e.clk, Log: agentlinktest.QuietLog(),
		HubVersion: "0.2.0", WebPort: func() int { return 31415 },
	})
	must(t, err)
	t.Cleanup(r.d.Close)
	r.inst.UseRemote(r.d)
	e.srv.UseRouter(r.d)
	return r
}

// online 配对并上线一台设备，返回拨号方与在其上创建的 probe-agent 实例（已收到含它的 assign）。
func (r *relayEnv) online(t *testing.T, name string) (*peer, model.InstanceDetail) {
	t.Helper()
	code := r.newCode(t, devices.PairingOptions{Name: name, Role: model.DeviceRoleServer})
	p, err := r.pair(t, code)
	must(t, err)
	p.helloOK(t, agent.PluginManifest{Origin: "exec", Manifest: agentManifestJSON(t, "probe-agent", "2.0.0")})
	if a := recvAssign(t, p); len(a.Instances) != 0 {
		t.Fatalf("新设备首个 assign 应为空，得到 %d 个实例", len(a.Instances))
	}
	in, err := r.inst.Create(bg, model.InstanceInput{PluginID: "probe-agent", Name: name + "-probe", RunsOn: p.id, Config: map[string]any{}})
	must(t, err)
	waitFor(t, "合并计时登记", func() bool {
		r.d.mu.Lock()
		defer r.d.mu.Unlock()
		return r.d.timer != nil
	})
	r.clk.Advance(AssignWindow)
	if a := recvAssign(t, p); len(a.Instances) != 1 || a.Instances[0].ID != in.ID {
		t.Fatalf("应下发新实例: %v", a.Instances)
	}
	return p, in
}

func (r *relayEnv) summary(t *testing.T, id string) string {
	t.Helper()
	d, err := r.inst.Get(bg, id)
	must(t, err)
	if d.Report == nil {
		return ""
	}
	return d.Report.Summary
}

func TestRelay端到端_报告入库去重与归属(t *testing.T) {
	r := newRelayEnv(t)
	p1, in1 := r.online(t, "nas-a")
	p2, in2 := r.online(t, "nas-b")
	before := r.changes.count(in1.ID)

	must(t, p1.sess.Send(okReport(in1.ID, 1, "第一次")))
	waitFor(t, "报告入库", func() bool { return r.summary(t, in1.ID) == "第一次" })
	if r.changes.count(in1.ID) <= before {
		t.Fatal("报告入库应触发实例变化通知（UI patch）")
	}

	// 重复序号丢弃；别的设备发来的同一实例报告被拒；随后的新序号照常入库
	must(t, p1.sess.Send(okReport(in1.ID, 1, "重复序号")))
	must(t, p2.sess.Send(okReport(in1.ID, 5, "别的设备")))
	must(t, p1.sess.Send(okReport(in1.ID, 2, "第二次")))
	// 栅栏：p2 随后发一条自己实例的报告，同一连接按序处理，它入库时前一条已被处理
	must(t, p2.sess.Send(okReport(in2.ID, 1, "p2 自己的")))
	waitFor(t, "新序号入库", func() bool { return r.summary(t, in1.ID) == "第二次" })
	waitFor(t, "p2 栅栏入库", func() bool { return r.summary(t, in2.ID) == "p2 自己的" })
	for _, s := range r.hist.list() {
		if s == in1.ID+"|重复序号" || s == in1.ID+"|别的设备" {
			t.Fatalf("不应入库: %v", r.hist.list())
		}
	}

	// 失败报告：保留旧值、计数
	must(t, p1.sess.Send(errReport(in1.ID, 3, agent.ErrorKindFailed, "exit 3")))
	waitFor(t, "失败入库", func() bool {
		v, err := r.inst.View(bg, in1.ID)
		return err == nil && v.Failures == 1 && v.ReportStale
	})
}

func TestRelay端到端_run_now成功超时与离线(t *testing.T) {
	r := newRelayEnv(t)
	p, in := r.online(t, "nas-a")

	type runRes struct {
		res model.InstanceRunResult
		err error
	}
	run := func() chan runRes {
		ch := make(chan runRes, 1)
		go func() {
			res, err := r.inst.Run(bg, in.ID)
			ch <- runRes{res, err}
		}()
		return ch
	}
	wait := func(ch chan runRes) runRes {
		t.Helper()
		select {
		case x := <-ch:
			return x
		case <-time.After(5 * time.Second):
			t.Fatal("等待 Run 返回超时")
			return runRes{}
		}
	}

	// 成功：先改名（内容变化）再立即测试，agent 先收到新 assign 再收到 run_now
	_, err := r.inst.Update(bg, in.ID, model.InstanceInput{Name: "改名", Config: map[string]any{}})
	must(t, err)
	ch := run()
	a := recvAssign(t, p)
	if len(a.Instances) != 1 {
		t.Fatalf("测试前应立即下发: %v", a.Instances)
	}
	rn, ok := p.recv(t).(*agent.RunNow)
	if !ok || rn.InstanceID != in.ID {
		t.Fatalf("应收到 run_now: %#v", rn)
	}
	must(t, p.sess.Send(&agent.RunResult{RequestID: rn.RequestID, InstanceID: in.ID, RunOutcome: agent.RunOutcome{
		Report: &report.Report{Status: report.StatusOK, Summary: "测试成功"}, ConfigHash: a.Instances[0].ConfigHash,
	}}))
	x := wait(ch)
	if x.err != nil || x.res.Report == nil || x.res.Report.Summary != "测试成功" {
		t.Fatalf("Run = %+v, %v", x.res.Report, x.err)
	}
	if r.summary(t, in.ID) != "测试成功" {
		t.Fatal("测试结果应入库")
	}

	// 超时：agent 不应答，推进插件超时 + 5 秒
	ch = run()
	if _, ok := p.recv(t).(*agent.RunNow); !ok {
		t.Fatal("应收到 run_now")
	}
	r.clk.Advance(manifest.DefaultTimeout + RunNowGrace)
	x = wait(ch)
	if !errors.Is(x.err, runtime.ErrTimeout) {
		t.Fatalf("应超时: %v", x.err)
	}
	if v, _ := r.inst.View(bg, in.ID); v.Failures != 0 {
		t.Fatal("中枢等待超时不应计入失败")
	}

	// 离线
	p.sess.Close("")
	waitFor(t, "会话摘除", func() bool { _, ok := r.srv.Session(p.id); return !ok })
	if _, err := r.inst.Run(bg, in.ID); !errors.Is(err, instances.ErrDeviceOffline) {
		t.Fatalf("离线应返回 ErrDeviceOffline: %v", err)
	}
}

func TestRelay端到端_日志与升级信息(t *testing.T) {
	r := newRelayEnv(t)
	p, _ := r.online(t, "nas-a")

	type logsRes struct {
		entries []model.LogEntry
		err     error
	}
	ch := make(chan logsRes, 1)
	go func() {
		es, err := r.d.Logs(context.Background(), p.id, 20, "info")
		ch <- logsRes{es, err}
	}()
	lg, ok := p.recv(t).(*agent.LogsGet)
	if !ok || lg.Limit != 20 || lg.MinLevel != "info" {
		t.Fatalf("应收到 logs_get: %#v", lg)
	}
	must(t, p.sess.Send(&agent.Logs{RequestID: lg.RequestID, Entries: []model.LogEntry{
		{Time: t0, Level: "info", Message: "agent 已启动"},
	}}))
	select {
	case x := <-ch:
		if x.err != nil || len(x.entries) != 1 || x.entries[0].Message != "agent 已启动" {
			t.Fatalf("Logs = %v, %v", x.entries, x.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待日志超时")
	}

	resp, err := p.sess.Request(context.Background(), &agent.UpgradeInfoGet{}, 0)
	must(t, err)
	ui, ok := resp.(*agent.UpgradeInfo)
	if !ok || ui.HubVersion != "0.2.0" || ui.WebPort != 31415 || len(ui.Agents) != 0 {
		t.Fatalf("upgrade_info = %#v", resp)
	}
}
