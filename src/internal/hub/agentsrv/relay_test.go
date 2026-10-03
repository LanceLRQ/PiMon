package agentsrv

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// readyDFX 返回已向 dev1 下发 i1、i2 的下发器与 dev1 的会话替身。
func readyDFX(t *testing.T) (*dfx, *fakeSender) {
	t.Helper()
	f := newDFX(t)
	s := f.online("dev1")
	f.inst.setSpecs("dev1", spec("i1", "p1"), spec("i2", "p1"))
	f.d.OnReady("dev1")
	waitAssigns(t, s, 1)
	return f, s
}

func okReport(id string, seq int64, summary string) *agent.Report {
	return &agent.Report{InstanceID: id, Seq: seq, RunOutcome: agent.RunOutcome{
		Report: &report.Report{Status: report.StatusOK, Summary: summary}, CollectedAt: t0,
	}}
}

func errReport(id string, seq int64, kind, msg string) *agent.Report {
	return &agent.Report{InstanceID: id, Seq: seq, RunOutcome: agent.RunOutcome{Error: msg, ErrorKind: kind, CollectedAt: t0}}
}

func summaries(as []applied) []string {
	out := make([]string, len(as))
	for i, a := range as {
		if a.rep != nil {
			out[i] = a.rep.Summary
		} else if a.err != nil {
			out[i] = "err"
		}
	}
	return out
}

// ---- report ----

func TestOnReport_入库并带下发时的内容摘要(t *testing.T) {
	f, s := readyDFX(t)
	f.d.onReport(s, "dev1", okReport("i1", 1, "a"))
	got := f.inst.appliedList()
	if len(got) != 1 || got[0].id != "i1" || got[0].hash != "c-i1" || got[0].err != nil || got[0].rep.Summary != "a" {
		t.Fatalf("入库 = %+v", got)
	}
}

func TestOnReport_归属校验(t *testing.T) {
	f, s := readyDFX(t)
	s2 := f.online("dev2")
	f.inst.setSpecs("dev2", spec("j1", "p1"))
	f.d.OnReady("dev2")
	waitAssigns(t, s2, 1)

	f.d.onReport(s2, "dev2", okReport("i1", 1, "别的设备的实例"))
	f.d.onReport(s, "dev1", okReport("ghost", 1, "未下发的实例"))
	f.d.onReport(newFakeSender(), "dev3", okReport("i1", 1, "从未下发的设备"))
	if got := f.inst.appliedList(); len(got) != 0 {
		t.Fatalf("不属于该设备的报告不应入库: %v", summaries(got))
	}
}

func TestOnReport_按会话与实例去重(t *testing.T) {
	f, s := readyDFX(t)
	f.d.onReport(s, "dev1", okReport("i1", 1, "a"))
	f.d.onReport(s, "dev1", okReport("i1", 1, "重复"))
	f.d.onReport(s, "dev1", okReport("i2", 1, "i2 独立计数"))
	f.d.onReport(s, "dev1", okReport("i1", 3, "b"))
	f.d.onReport(s, "dev1", okReport("i1", 2, "乱序旧报告"))
	want := []string{"a", "i2 独立计数", "b"}
	if got := summaries(f.inst.appliedList()); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("去重结果 = %v，应为 %v", got, want)
	}

	// 新会话的序号从 1 开始
	ns := f.online("dev1")
	f.d.onReport(ns, "dev1", okReport("i1", 1, "新会话"))
	if got := summaries(f.inst.appliedList()); got[len(got)-1] != "新会话" {
		t.Fatalf("新会话的报告应入库: %v", got)
	}
	// 被顶替的旧会话在关闭前发来的报告丢弃
	f.d.onReport(s, "dev1", okReport("i1", 9, "旧会话"))
	if got := summaries(f.inst.appliedList()); got[len(got)-1] == "旧会话" {
		t.Fatal("非当前会话的报告不应入库")
	}
}

func TestOnReport_会话结束清理去重状态(t *testing.T) {
	f, s := readyDFX(t)
	f.d.onReport(s, "dev1", okReport("i1", 1, "a"))
	f.d.mu.Lock()
	n := len(f.d.seqs)
	f.d.mu.Unlock()
	if n != 1 {
		t.Fatalf("应记录 1 个会话的去重状态，得到 %d", n)
	}
	close(s.done)
	waitFor(t, "会话结束后清理去重状态", func() bool {
		f.d.mu.Lock()
		defer f.d.mu.Unlock()
		return len(f.d.seqs) == 0
	})
}

func TestOnReport_ConfigHash比对(t *testing.T) {
	f, s := readyDFX(t)
	r1 := okReport("i1", 1, "旧配置")
	r1.ConfigHash = "h-old"
	r2 := okReport("i1", 2, "当前配置")
	r2.ConfigHash = "h-i1"
	r3 := okReport("i1", 3, "未填写")
	f.d.onReport(s, "dev1", r1)
	f.d.onReport(s, "dev1", r2)
	f.d.onReport(s, "dev1", r3)
	want := "当前配置,未填写"
	if got := strings.Join(summaries(f.inst.appliedList()), ","); got != want {
		t.Fatalf("入库 = %s，应为 %s", got, want)
	}
}

func TestOnReport_错误类别映射(t *testing.T) {
	f, s := readyDFX(t)
	f.d.onReport(s, "dev1", errReport("i1", 1, agent.ErrorKindBusy, "busy"))
	f.d.onReport(s, "dev1", errReport("i1", 2, agent.ErrorKindNotAssigned, "na"))
	if got := f.inst.appliedList(); len(got) != 0 {
		t.Fatalf("busy、not_assigned 不应入库: %v", summaries(got))
	}
	f.d.onReport(s, "dev1", errReport("i1", 3, agent.ErrorKindTimeout, "跑了 7 秒"))
	f.d.onReport(s, "dev1", errReport("i1", 4, agent.ErrorKindFailed, strings.Repeat("长", 3000)))
	got := f.inst.appliedList()
	if len(got) != 2 {
		t.Fatalf("应入库 2 条，得到 %d", len(got))
	}
	if !errors.Is(got[0].err, runtime.ErrTimeout) || !strings.Contains(got[0].err.Error(), "跑了 7 秒") || got[0].rep != nil {
		t.Fatalf("timeout 映射 = %v", got[0].err)
	}
	if !errors.Is(got[1].err, runtime.ErrFailed) {
		t.Fatalf("failed 映射 = %v", got[1].err)
	}
	if n := len(got[1].err.Error()); n > maxErrorText+64 {
		t.Fatalf("错误文字应截断到 2 KiB，得到 %d 字节", n)
	}
}

func TestOnReport_清空State(t *testing.T) {
	f, s := readyDFX(t)
	r := okReport("i1", 1, "a")
	r.Report.State = "x"
	f.d.onReport(s, "dev1", r)
	if got := f.inst.appliedList(); len(got) != 1 || got[0].rep.State != "" {
		t.Fatal("报告的 state 应被清空")
	}
}

// ---- run_now ----

func runResult(req agent.Requester, o agent.RunOutcome) agent.Message {
	rn := req.(*agent.RunNow)
	return &agent.RunResult{RequestID: rn.RequestID, InstanceID: rn.InstanceID, RunOutcome: o}
}

func TestRunNow_离线与不兼容(t *testing.T) {
	f := newDFX(t)
	f.devs.devs["dev1"] = model.Device{ID: "dev1"}
	if _, err := f.d.RunNow(bg, "dev1", "i1", time.Second); !errors.Is(err, instances.ErrDeviceOffline) {
		t.Fatalf("离线应返回 ErrDeviceOffline: %v", err)
	}
	f.devs.devs["dev1"] = model.Device{ID: "dev1", Incompatible: &model.DeviceIncompatible{Reason: agent.ReasonVersionTooOld, RequiredMin: 2}}
	_, err := f.d.RunNow(bg, "dev1", "i1", time.Second)
	var ie *instances.DeviceIncompatibleError
	if !errors.As(err, &ie) || ie.Reason != agent.ReasonVersionTooOld || ie.RequiredMin != 2 || ie.HubVersion != "0.2.0" {
		t.Fatalf("不兼容应带详情: %v", err)
	}
}

func TestRunNow_先立即下发待发的assign(t *testing.T) {
	f, s := readyDFX(t)
	var gotTimeout time.Duration
	s.setReply(func(req agent.Requester, timeout time.Duration) (agent.Message, error) {
		gotTimeout = timeout
		return runResult(req, agent.RunOutcome{Report: &report.Report{Status: report.StatusOK, Summary: "ok"}}), nil
	})
	f.inst.setSpecs("dev1", spec("i1", "p1"), spec("i2", "p1"), spec("i3", "p1"))
	f.d.MachineChanged("dev1") // 合并窗口未到
	rep, err := f.d.RunNow(bg, "dev1", "i3", 7*time.Second)
	if err != nil || rep == nil || rep.Summary != "ok" {
		t.Fatalf("RunNow = %+v, %v", rep, err)
	}
	if gotTimeout != 7*time.Second+RunNowGrace {
		t.Fatalf("请求超时 = %v", gotTimeout)
	}
	msgs := s.all()
	if len(msgs) != 3 {
		t.Fatalf("应依次发出 assign v1、assign v2、run_now，得到 %d 条", len(msgs))
	}
	if a, ok := msgs[1].(*agent.Assign); !ok || a.Version != 2 || len(a.Instances) != 3 {
		t.Fatalf("run_now 之前应先发出新的 assign: %#v", msgs[1])
	}
	if rn, ok := msgs[2].(*agent.RunNow); !ok || rn.InstanceID != "i3" {
		t.Fatalf("最后应为 run_now: %#v", msgs[2])
	}
	if got := f.inst.appliedList(); len(got) != 0 {
		t.Fatal("RunNow 不自行入库（由实例服务按运行开始时的 hash 入库）")
	}
	// 没有待发变更时不重复下发
	if _, err := f.d.RunNow(bg, "dev1", "i3", time.Second); err != nil {
		t.Fatal(err)
	}
	if n := len(s.assigns()); n != 2 {
		t.Fatalf("没有待发变更时不应再下发，assign 共 %d 次", n)
	}
}

func TestRunNow_结果映射(t *testing.T) {
	f, s := readyDFX(t)
	cases := []struct {
		name     string
		reply    func(agent.Requester) (agent.Message, error)
		is       error
		recorded bool
		text     string
	}{
		{"会话关闭", func(agent.Requester) (agent.Message, error) { return nil, agentlink.ErrSessionClosed },
			instances.ErrDeviceOffline, false, ""},
		{"请求超时", func(agent.Requester) (agent.Message, error) { return nil, agentlink.ErrRequestTimeout },
			runtime.ErrTimeout, false, ""},
		{"插件超时", func(r agent.Requester) (agent.Message, error) {
			return runResult(r, agent.RunOutcome{Error: "7s", ErrorKind: agent.ErrorKindTimeout}), nil
		}, runtime.ErrTimeout, true, "7s"},
		{"失败", func(r agent.Requester) (agent.Message, error) {
			return runResult(r, agent.RunOutcome{Error: "exit 1", ErrorKind: agent.ErrorKindFailed}), nil
		}, runtime.ErrFailed, true, "exit 1"},
		{"忙", func(r agent.Requester) (agent.Message, error) {
			return runResult(r, agent.RunOutcome{Error: "busy", ErrorKind: agent.ErrorKindBusy}), nil
		}, instances.ErrRunBusy, false, ""},
		{"尚未下发", func(r agent.Requester) (agent.Message, error) {
			return runResult(r, agent.RunOutcome{Error: "x", ErrorKind: agent.ErrorKindNotAssigned}), nil
		}, runtime.ErrFailed, false, "实例尚未下发到设备"},
		{"旧配置的结果", func(r agent.Requester) (agent.Message, error) {
			return runResult(r, agent.RunOutcome{Report: &report.Report{Status: report.StatusOK}, ConfigHash: "h-old"}), nil
		}, runtime.ErrFailed, false, ""},
		{"实例不符", func(r agent.Requester) (agent.Message, error) {
			return &agent.RunResult{RequestID: r.GetRequestID(), InstanceID: "i2",
				RunOutcome: agent.RunOutcome{Report: &report.Report{Status: report.StatusOK}}}, nil
		}, runtime.ErrFailed, false, ""},
		{"应答类型不符", func(r agent.Requester) (agent.Message, error) {
			return &agent.Logs{RequestID: r.GetRequestID()}, nil
		}, runtime.ErrFailed, false, ""},
	}
	for _, c := range cases {
		s.setReply(func(r agent.Requester, _ time.Duration) (agent.Message, error) { return c.reply(r) })
		rep, err := f.d.RunNow(bg, "dev1", "i1", time.Second)
		if rep != nil || !errors.Is(err, c.is) {
			t.Fatalf("%s: rep=%v err=%v", c.name, rep, err)
		}
		// 由实例服务按 Recordable 判断：未被 NotRecorded 标记的超时/失败入库
		if rec := instances.Recordable(err); rec != c.recorded {
			t.Fatalf("%s: 是否入库 = %v，应为 %v（%v）", c.name, rec, c.recorded, err)
		}
		if c.text != "" && !strings.Contains(err.Error(), c.text) {
			t.Fatalf("%s: 错误文字 = %q", c.name, err.Error())
		}
	}
}

var bg = context.Background()

// ---- logs ----

func TestLogs_转发与映射(t *testing.T) {
	f, s := readyDFX(t)
	var got *agent.LogsGet
	var gotTimeout time.Duration
	s.setReply(func(r agent.Requester, timeout time.Duration) (agent.Message, error) {
		got, gotTimeout = r.(*agent.LogsGet), timeout
		return &agent.Logs{RequestID: r.GetRequestID(), Entries: []model.LogEntry{{Time: t0, Level: "warn", Message: "m"}}}, nil
	})
	entries, err := f.d.Logs(bg, "dev1", 50, "warn")
	if err != nil || len(entries) != 1 || entries[0].Message != "m" {
		t.Fatalf("Logs = %v, %v", entries, err)
	}
	if got.Limit != 50 || got.MinLevel != "warn" || gotTimeout != LogsTimeout {
		t.Fatalf("logs_get = %+v，超时 %v", got, gotTimeout)
	}

	// 上限 2000
	if _, err := f.d.Logs(bg, "dev1", 999999, ""); err != nil || got.Limit != MaxLogLimit {
		t.Fatalf("limit 应截到 %d，得到 %d（%v）", MaxLogLimit, got.Limit, err)
	}
	// 空列表不是 null
	s.setReply(func(r agent.Requester, _ time.Duration) (agent.Message, error) {
		return &agent.Logs{RequestID: r.GetRequestID()}, nil
	})
	if entries, err := f.d.Logs(bg, "dev1", 10, ""); err != nil || entries == nil {
		t.Fatalf("空日志应为空列表: %v %v", entries, err)
	}

	s.setReply(func(agent.Requester, time.Duration) (agent.Message, error) { return nil, agentlink.ErrRequestTimeout })
	if _, err := f.d.Logs(bg, "dev1", 10, ""); !errors.Is(err, runtime.ErrTimeout) {
		t.Fatalf("请求超时应映射为 runtime.ErrTimeout: %v", err)
	}
	s.setReply(func(agent.Requester, time.Duration) (agent.Message, error) { return nil, agentlink.ErrSessionClosed })
	if _, err := f.d.Logs(bg, "dev1", 10, ""); !errors.Is(err, instances.ErrDeviceOffline) {
		t.Fatalf("会话关闭应映射为离线: %v", err)
	}
	if _, err := f.d.Logs(bg, "dev2", 10, ""); !errors.Is(err, instances.ErrDeviceOffline) {
		t.Fatalf("没有会话应为离线: %v", err)
	}
}

// ---- upgrade_info ----

func TestUpgradeInfo_回复版本与端口(t *testing.T) {
	f := newDFX(t)
	s := f.online("dev1")
	f.d.replyUpgradeInfo(s, "dev1", &agent.UpgradeInfoGet{RequestID: "r1"})
	msgs := s.all()
	if len(msgs) != 1 {
		t.Fatalf("应回复 1 条，得到 %d", len(msgs))
	}
	ui, ok := msgs[0].(*agent.UpgradeInfo)
	if !ok || ui.RequestID != "r1" || ui.HubVersion != "0.2.0" || ui.WebPort != 31415 || ui.Agents == nil || len(ui.Agents) != 0 {
		t.Fatalf("upgrade_info = %#v", msgs[0])
	}
}

func TestUpgradeInfo_回复内嵌校验和(t *testing.T) {
	f := newDFX(t)
	f.d.agentSums = func() map[string]string { return map[string]string{"linux-arm64": "deadbeef"} }
	s := f.online("dev1")
	f.d.replyUpgradeInfo(s, "dev1", &agent.UpgradeInfoGet{RequestID: "r1"})
	ui, ok := s.all()[0].(*agent.UpgradeInfo)
	if !ok || ui.Agents["linux-arm64"] != "deadbeef" || len(ui.Agents) != 1 {
		t.Fatalf("upgrade_info = %#v", s.all()[0])
	}
}

func TestUpgradeInfo_端口不合法时不回复(t *testing.T) {
	for _, port := range []int{0, 70000} {
		f := newDFX(t)
		f.d.webPort = func() int { return port }
		s := f.online("dev1")
		f.d.replyUpgradeInfo(s, "dev1", &agent.UpgradeInfoGet{RequestID: "r1"})
		if n := len(s.all()); n != 0 {
			t.Fatalf("端口 %d 不合法时不应回复，得到 %d 条", port, n)
		}
	}
}

func TestWebPortFrom(t *testing.T) {
	cases := map[string]int{"127.0.0.1:31415": 31415, "[::]:8080": 8080, "": 0, "bad": 0, ":0": 0}
	for in, want := range cases {
		if got := WebPortFrom(in); got != want {
			t.Fatalf("WebPortFrom(%q) = %d，应为 %d", in, got, want)
		}
	}
}

func TestRunNow_等待下发受ctx约束(t *testing.T) {
	f, s := readyDFX(t)
	s.setReply(func(r agent.Requester, _ time.Duration) (agent.Message, error) {
		return runResult(r, agent.RunOutcome{Report: &report.Report{Status: report.StatusOK}}), nil
	})
	block := make(chan struct{})
	f.inst.mu.Lock()
	f.inst.block = block
	f.inst.mu.Unlock()
	f.d.MachineChanged("dev1")

	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() {
		_, err := f.d.RunNow(ctx, "dev1", "i1", time.Second)
		done <- err
	}()
	waitFor(t, "工作协程开始处理 dev1", func() bool {
		f.d.mu.Lock()
		defer f.d.mu.Unlock()
		_, ok := f.d.running["dev1"]
		return ok
	})
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("应返回 ctx 的错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 RunNow 应返回")
	}
	close(block)
	f.idle(t)
	// 正在处理中的设备：等那一轮完成后再发 run_now
	if _, err := f.d.RunNow(bg, "dev1", "i1", time.Second); err != nil {
		t.Fatal(err)
	}
}
