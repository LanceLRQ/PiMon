package runner

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"

	_ "github.com/LanceLRQ/PiMon/src/plugins/tcpcheck"
)

func TestAssign_新增修改删除与未变不重启(t *testing.T) {
	src := newCountSource(t)
	src.block.Store(true)
	f := newFixture(t, 0, src)
	b, s := f.attach(t)

	b.Assign(&agent.Assign{Version: 7, Instances: []agent.AssignedInstance{
		inst("a", "a", "ha"), inst("b", "b", "hb"), inst("c", "c", "hc"),
	}})
	res := expect[*agent.AssignResult](t, s)
	if res.Version != 7 || len(res.Results) != 3 {
		t.Fatalf("assign_result = %+v", res)
	}
	for i, id := range []string{"a", "b", "c"} {
		if r := res.Results[i]; r.InstanceID != id || r.Status != agent.AssignStarted {
			t.Fatalf("results[%d] = %+v", i, r)
		}
	}
	running := map[string]call{}
	for range 3 {
		cl := src.next(t)
		running[cl.name] = cl
	}

	// 新会话版本号可以任意（hub 重启后从 1 开始），回执原样回显
	b.Assign(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{
		inst("a", "a", "ha"), inst("b", "b2", "hb2"), inst("d", "d", "hd"),
	}})
	// Assign 同步应用：返回时被修改、被删除的运行已取消，未变的不受影响
	if running["a"].ctx.Err() != nil {
		t.Error("未变的实例不应被重启")
	}
	if running["b"].ctx.Err() == nil || running["c"].ctx.Err() == nil {
		t.Error("修改与删除的实例应立即取消")
	}
	if res := expect[*agent.AssignResult](t, s); res.Version != 1 || len(res.Results) != 3 {
		t.Fatalf("第二次 assign_result = %+v", res)
	}
	got := map[string]bool{}
	for range 2 {
		got[src.next(t).name] = true
	}
	if !got["b2"] || !got["d"] {
		t.Fatalf("应启动修改后的 b 与新增的 d，实际 %v", got)
	}
	if src.count("a") != 1 || src.count("c") != 1 {
		t.Fatalf("a、c 调用次数 = %d、%d，均应为 1", src.count("a"), src.count("c"))
	}
	close(src.gate)
}

func TestAssign_三种失败回执(t *testing.T) {
	src := newCountSource(t)
	bad := newCountSource(t)
	m := *bad.m
	m.ID, m.APIVersion = "test-future", manifest.SupportedAPIVersion+1
	bad.m = &m
	f := newFixture(t, 0, src, bad)
	b, s := f.attach(t)

	missing := inst("m", "m", "h1")
	missing.PluginID = "nope"
	incompat := inst("x", "x", "h2")
	incompat.PluginID = "test-future"
	badCfg := inst("v", "", "h3")
	badCfg.Config = map[string]any{"n": float64(99)}
	badCfg.Secrets = map[string]string{"token": "SECRET-VALUE"}
	badProxy := inst("p", "p", "h4")
	badProxy.ProxyURL = "ftp://user:PROXY-PW@127.0.0.1:21"

	b.Assign(&agent.Assign{Version: 3, Instances: []agent.AssignedInstance{missing, incompat, badCfg, badProxy}})
	res := expect[*agent.AssignResult](t, s)
	want := []struct{ id, status, msg string }{
		{"m", agent.AssignPluginMissing, ""},
		{"x", agent.AssignIncompatible, "api_version"},
		{"v", agent.AssignInvalidConfig, "n:out_of_range, name:required"},
		{"p", agent.AssignInvalidConfig, "proxy_url:invalid"},
	}
	if len(res.Results) != len(want) {
		t.Fatalf("results = %+v", res.Results)
	}
	for i, w := range want {
		r := res.Results[i]
		if r.InstanceID != w.id || r.Status != w.status || !strings.Contains(r.Message, w.msg) {
			t.Errorf("results[%d] = %+v，期望 %+v", i, r, w)
		}
		for _, leak := range []string{"SECRET-VALUE", "PROXY-PW", "99"} {
			if strings.Contains(r.Message, leak) {
				t.Errorf("回执文字泄露 %q: %s", leak, r.Message)
			}
		}
	}
	if c := src.count("m") + src.count("x") + src.count("p"); c != 0 {
		t.Fatalf("回执失败的实例不应运行，调用 %d 次", c)
	}

	// 回执失败的实例 run_now 回 failed，Error 用回执原因
	b.RunNow(&agent.RunNow{RequestID: "q1", InstanceID: "m"})
	rr := expect[*agent.RunResult](t, s)
	if rr.RequestID != "q1" || rr.ErrorKind != agent.ErrorKindFailed || !strings.Contains(rr.Error, agent.AssignPluginMissing) ||
		rr.ConfigHash != "h1" {
		t.Fatalf("run_result = %+v", rr)
	}
}

func TestAssign_JSON数字配置可跑内置插件(t *testing.T) {
	tcp, ok := runtime.Builtin("tcp-check")
	if !ok {
		t.Fatal("tcp-check 未注册")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	f := newFixture(t, time.Hour, tcp)
	b, s := f.attach(t)

	in := agent.AssignedInstance{
		ID: "t", PluginID: "tcp-check", IntervalMs: 60000, TimeoutMs: 30000, ConfigHash: "ht",
		Config: map[string]any{"host": "127.0.0.1", "port": ln.Addr().(*net.TCPAddr).Port, "timeout": "5s"},
	}
	// 经过协议编解码：数字变成 float64，与 hub 实际下发一致
	data, err := agent.Encode(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{in}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := agent.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	a := msg.(*agent.Assign)
	if _, isFloat := a.Instances[0].Config["port"].(float64); !isFloat {
		t.Fatalf("port 类型 = %T", a.Instances[0].Config["port"])
	}
	b.Assign(a)
	if r := expect[*agent.AssignResult](t, s).Results[0]; r.Status != agent.AssignStarted {
		t.Fatalf("回执 = %+v", r)
	}
	b.RunNow(&agent.RunNow{RequestID: "q", InstanceID: "t"})
	rr := expect[*agent.RunResult](t, s)
	if rr.Report == nil || rr.Report.Status != report.StatusOK {
		t.Fatalf("run_result = %+v", rr)
	}
}
