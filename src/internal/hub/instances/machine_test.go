package instances

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// agentOnlyManifest 是只能在 agent 上运行的插件（interval 45s 便于和 hub 侧区分）。
const agentOnlyManifest = `id: %ID%
version: 1.0.0
api_version: 1
name: AgentOnly
kind: source
runtime: exec
runs_on: [%RUNS%]
interval: 45s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, required: true}
  - {key: via, type: proxy, title: Proxy}
outputs:
  - {key: temp, type: number, title: Temp}
`

func agentManifest(t *testing.T, id, runs string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(strings.NewReplacer("%ID%", id, "%RUNS%", runs).Replace(agentOnlyManifest)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// fakeMachines 是按机器取插件的替身：machine → 插件 id → 插件。
type fakeMachines struct {
	mu sync.Mutex
	m  map[string]map[string]plugins.Plugin
}

func (f *fakeMachines) put(machine string, m *manifest.Manifest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m[machine] == nil {
		f.m[machine] = map[string]plugins.Plugin{}
	}
	f.m[machine][m.ID] = plugins.Plugin{ID: m.ID, Origin: plugins.OriginExec, Manifest: m}
}

func (f *fakeMachines) MachinePlugin(machine, id string) (plugins.Plugin, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[machine][id]
	return p, ok
}

// fakeDevices 固定返回一台 vps 和一台 server（离线与否不影响校验，替身里也不体现）。
type fakeDevices map[string]DeviceFacts

func (f fakeDevices) Device(id string) (DeviceFacts, bool) {
	d, ok := f[id]
	return d, ok
}

// fakeRemote 记录 MachineChanged 的调用。
type fakeRemote struct {
	mu      sync.Mutex
	changed []string
}

func (r *fakeRemote) MachineChanged(machine string) {
	r.mu.Lock()
	r.changed = append(r.changed, machine)
	r.mu.Unlock()
}

func (r *fakeRemote) RunNow(context.Context, string, string, time.Duration) (*report.Report, error) {
	return nil, errors.New("未实现")
}

// take 返回并清空已记录的机器（去重、排序）。
func (r *fakeRemote) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.changed)
	r.changed = nil
	slices.Sort(out)
	return slices.Compact(out)
}

type machineFx struct {
	*fx
	machines *fakeMachines
	remote   *fakeRemote
}

const (
	devServer = "srv1"
	devVPS    = "vps1"
)

// newMachineFx 在 newFx 之上接入按机器取插件、设备状态与远程下发的替身：
// 两台设备都上报了 agent-only 插件 agentonly 与双栖插件 dual；hub 上另有 exec 插件
// hubagent（runs_on: [agent]）与 dual（runs_on: [hub, agent]）。
func newMachineFx(t *testing.T) *machineFx {
	t.Helper()
	f := newFx(t)
	f.putExecYAML("hubagent", agentManifestYAML("hubagent", "agent"))
	f.putExecYAML("dual", agentManifestYAML("dual", "hub, agent"))
	f.rescan()
	mf := &machineFx{fx: f, machines: &fakeMachines{m: map[string]map[string]plugins.Plugin{}}, remote: &fakeRemote{}}
	for _, dev := range []string{devServer, devVPS} {
		mf.machines.put(dev, agentManifest(t, "agentonly", "agent"))
		mf.machines.put(dev, agentManifest(t, "dual", "hub, agent"))
	}
	f.svc = New(Config{
		DB: f.db, Box: f.box, Clock: f.clk, Plugins: f.reg, History: f.hist, Machines: mf.machines,
		Jitter: func(time.Duration) time.Duration { return 0 },
	})
	f.svc.UseProxies(f.px)
	f.svc.UseDevices(fakeDevices{devServer: {Role: model.DeviceRoleServer}, devVPS: {Role: model.DeviceRoleVPS}})
	f.svc.UseRemote(mf.remote)
	return mf
}

func agentManifestYAML(id, runs string) string {
	return strings.NewReplacer("%ID%", id, "%RUNS%", runs).Replace(agentOnlyManifest)
}

// putExecYAML 在插件目录下放一个指定 manifest 的 exec 插件。
func (f *fx) putExecYAML(id, y string) {
	f.t.Helper()
	d := filepath.Join(f.plugDir, id)
	must(f.t, os.MkdirAll(d, 0o755))
	must(f.t, os.Chmod(d, 0o755))
	must(f.t, os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(y), 0o644))
	must(f.t, os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\necho '{\"status\":\"ok\"}'\n"), 0o755))
	must(f.t, os.Chmod(filepath.Join(d, "run"), 0o755))
}

func (mf *machineFx) createOn(machine, plugin, name string, cfg map[string]any) (model.InstanceDetail, error) {
	return mf.svc.Create(bg, model.InstanceInput{PluginID: plugin, Name: name, Config: cfg, RunsOn: machine})
}

func machineReason(t *testing.T, err error) string {
	t.Helper()
	var mi *MachineInvalidError
	if !errors.As(err, &mi) {
		t.Fatalf("应为 MachineInvalidError: %v", err)
	}
	return mi.Reason
}

func TestMachineInvalid各原因(t *testing.T) {
	mf := newMachineFx(t)
	cfg := map[string]any{"host": "a"}

	_, err := mf.createOn("ghost", "agentonly", "x", cfg)
	if r := machineReason(t, err); r != ReasonDeviceNotFound {
		t.Fatalf("设备不存在: reason=%s", r)
	}
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("device_not_found 应可用 errors.Is(ErrDeviceNotFound) 判断: %v", err)
	}

	// hub 上的 probe 只声明 runs_on: [hub]，设备上也没有它
	_, err = mf.createOn(devServer, "probe", "x", probeCfg("a"))
	if r := machineReason(t, err); r != ReasonRunsOnMismatch {
		t.Fatalf("hub-only 插件放到设备: reason=%s", r)
	}

	// 插件声明可在 agent 运行，但该设备没有上报
	mf.putExecYAML("lonely", agentManifestYAML("lonely", "hub, agent"))
	mf.rescan()
	_, err = mf.createOn(devServer, "lonely", "x", cfg)
	if r := machineReason(t, err); r != ReasonPluginUnavailable {
		t.Fatalf("设备未上报该插件: reason=%s", r)
	}
	// 哪里都没有的插件放到设备上同样是 plugin_unavailable
	_, err = mf.createOn(devServer, "nowhere", "x", cfg)
	if r := machineReason(t, err); r != ReasonPluginUnavailable {
		t.Fatalf("不存在的插件放到设备: reason=%s", r)
	}

	// agent-only 插件不能再建成 hub 实例（显式 hub 与缺省都一样）
	for _, runsOn := range []string{"hub", ""} {
		_, err = mf.createOn(runsOn, "hubagent", "x", cfg)
		if r := machineReason(t, err); r != ReasonRunsOnMismatch {
			t.Fatalf("agent-only 插件建为 hub 实例(runs_on=%q): reason=%s", runsOn, r)
		}
	}
	// hub 上不存在的插件仍是 ErrPluginNotFound
	if _, err := mf.createOn("hub", "nowhere", "x", cfg); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("hub 上不存在的插件: %v", err)
	}
	if n := countRows(t, mf.fx); n != 0 {
		t.Fatalf("校验失败不应入库，行数 %d", n)
	}
}

func countRows(t *testing.T, f *fx) int {
	t.Helper()
	var n int
	must(t, f.db.QueryRow(`SELECT COUNT(*) FROM plugin_instances`).Scan(&n))
	return n
}

func TestRemoteInstance按设备manifest校验且不进调度(t *testing.T) {
	mf := newMachineFx(t)
	mf.start()

	// 配置按设备上报的 schema 校验：缺少必填 host
	var fe model.FieldErrors
	if _, err := mf.createOn(devServer, "agentonly", "x", map[string]any{}); !errors.As(err, &fe) || fe["host"] != model.FieldRequired {
		t.Fatalf("应按设备 manifest 校验配置: %v", err)
	}

	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if d.RunsOn != devServer {
		t.Fatalf("RunsOn = %q", d.RunsOn)
	}
	var machine string
	must(t, mf.db.QueryRow(`SELECT machine FROM plugin_instances WHERE id = ?`, d.ID).Scan(&machine))
	if machine != devServer {
		t.Fatalf("machine 列 = %q", machine)
	}
	if mf.svc.isScheduled(d.ID) {
		t.Fatal("远程实例不应进入本机调度器")
	}
	if got := mf.remote.take(); !slices.Equal(got, []string{devServer}) {
		t.Fatalf("应通知设备重新下发: %v", got)
	}
	// 展示用设备 manifest：间隔取 45s，不是引用失效
	if d.EffectiveIntervalSeconds != 45 || d.DisplayState == "broken" || d.Issue != "" {
		t.Fatalf("远程实例视图 = %+v", d.Instance)
	}

	// 重新排程（插件重扫等）同样只通知设备，不进调度器
	mf.svc.Resync(bg)
	if mf.svc.isScheduled(d.ID) {
		t.Fatal("重新排程后远程实例不应进入调度器")
	}
	if got := mf.remote.take(); !slices.Equal(got, []string{devServer}) {
		t.Fatalf("重新排程应通知设备: %v", got)
	}

	// 「保存并测试」不在本机运行
	if _, err := mf.svc.Run(bg, d.ID); !errors.Is(err, runtime.ErrFailed) {
		t.Fatalf("远程实例 Run 应返回 ErrFailed（RunNow 未接入）: %v", err)
	}
}

func TestProxyLocation按运行位置校验(t *testing.T) {
	mf := newMachineFx(t)
	px, _ := proxy.Parse("http://127.0.0.1:8080")
	for id, loc := range map[string]string{"p-hub": model.ProxyLocationHub, "p-lan": model.ProxyLocationLAN, "p-any": model.ProxyLocationAny} {
		mf.px.known[id] = px
		mf.px.locs = mapPut(mf.px.locs, id, loc)
	}
	cases := []struct {
		machine, proxy string
		ok             bool
	}{
		{"hub", "p-hub", true}, {"hub", "p-lan", true}, {"hub", "p-any", true},
		{devServer, "p-hub", false}, {devServer, "p-lan", true}, {devServer, "p-any", true},
		{devVPS, "p-hub", false}, {devVPS, "p-lan", false}, {devVPS, "p-any", true},
	}
	for _, c := range cases {
		_, err := mf.createOn(c.machine, "dual", c.machine+c.proxy, map[string]any{"host": "a", "via": c.proxy})
		if c.ok {
			if err != nil {
				t.Fatalf("%s 上用 %s 应允许: %v", c.machine, c.proxy, err)
			}
			continue
		}
		var pe *ProxyLocationError
		if !errors.As(err, &pe) {
			t.Fatalf("%s 上用 %s 应拒绝: %v", c.machine, c.proxy, err)
		}
		if pe.Field != "via" || pe.Location != mf.px.locs[c.proxy] {
			t.Fatalf("错误详情 = %+v", pe)
		}
	}

	// 更新时把实例搬到 VPS 上，原本合法的 lan 代理被拒，且库里不变
	d, err := mf.createOn(devServer, "dual", "搬家", map[string]any{"host": "a", "via": "p-lan"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mf.svc.Update(bg, d.ID, model.InstanceInput{Name: "搬家", Config: map[string]any{"host": "a", "via": "p-lan"}, RunsOn: devVPS})
	var pe *ProxyLocationError
	if !errors.As(err, &pe) || pe.Location != model.ProxyLocationLAN {
		t.Fatalf("搬到 VPS 时应拒绝 lan 代理: %v", err)
	}
	if g, _ := mf.svc.Get(bg, d.ID); g.RunsOn != devServer {
		t.Fatalf("被拒后运行位置不应改变: %s", g.RunsOn)
	}
}

func mapPut(m map[string]string, k, v string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[k] = v
	return m
}

func TestChangeMachine两台机器都重新下发(t *testing.T) {
	mf := newMachineFx(t)
	mf.start()
	d, err := mf.createOn("hub", "dual", "双栖", map[string]any{"host": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !mf.svc.isScheduled(d.ID) || d.RunsOn != "hub" {
		t.Fatalf("hub 实例应在调度器里: runs_on=%s", d.RunsOn)
	}
	if got := mf.remote.take(); len(got) != 0 {
		t.Fatalf("hub 实例不应通知设备: %v", got)
	}
	// 先跑出一份报告：换机器后旧机器的数据不应沿用
	if _, err := mf.svc.Run(bg, d.ID); err != nil {
		t.Fatal(err)
	}

	update := func(machine string) model.InstanceDetail {
		t.Helper()
		g, err := mf.svc.Update(bg, d.ID, model.InstanceInput{Name: "双栖", Config: map[string]any{"host": "a"}, RunsOn: machine})
		if err != nil {
			t.Fatalf("改到 %s: %v", machine, err)
		}
		return g
	}

	// hub → 设备：本机不再运行，通知新设备，旧报告清空
	g := update(devServer)
	if mf.svc.isScheduled(d.ID) || g.RunsOn != devServer {
		t.Fatal("改到设备后 hub 不应再运行该实例")
	}
	if g.Report != nil {
		t.Fatalf("换机器后旧报告应清空: %+v", g.Report)
	}
	if got := mf.remote.take(); !slices.Equal(got, []string{devServer}) {
		t.Fatalf("hub→设备 通知 = %v", got)
	}

	// 设备 → 设备：两台都通知
	update(devVPS)
	if got := mf.remote.take(); !slices.Equal(got, []string{devServer, devVPS}) {
		t.Fatalf("设备→设备 通知 = %v", got)
	}

	// RunsOn 缺省表示保留原运行位置
	g, err = mf.svc.Update(bg, d.ID, model.InstanceInput{Name: "改名", Config: map[string]any{"host": "a"}})
	if err != nil || g.RunsOn != devVPS {
		t.Fatalf("缺省 RunsOn 应保留原位置: %s %v", g.RunsOn, err)
	}
	mf.remote.take()

	// 设备 → hub：通知旧设备，本机重新调度
	update("hub")
	if !mf.svc.isScheduled(d.ID) {
		t.Fatal("改回 hub 后应重新调度")
	}
	if got := mf.remote.take(); !slices.Equal(got, []string{devVPS}) {
		t.Fatalf("设备→hub 通知 = %v", got)
	}
}

func TestRemoteInstance删除复制与摘除代理(t *testing.T) {
	mf := newMachineFx(t)
	mf.start()
	px, _ := proxy.Parse("http://127.0.0.1:8080")
	mf.px.known["p-any"] = px
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a", "via": "p-any"})
	if err != nil {
		t.Fatal(err)
	}

	// 复制保留运行位置
	c, err := mf.svc.Copy(bg, d.ID, "副本")
	if err != nil || c.RunsOn != devServer {
		t.Fatalf("复制应保留运行位置: %s %v", c.RunsOn, err)
	}
	mf.remote.take()

	// 摘除代理：按设备 manifest 的代理字段改为直连，暂停并通知设备
	must(t, mf.svc.DetachAndPause(bg, "p-any"))
	g, err := mf.svc.Get(bg, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Paused || g.Config["via"] != proxy.DirectValue {
		t.Fatalf("摘除代理后 = paused %v via %v", g.Paused, g.Config["via"])
	}
	if got := mf.remote.take(); !slices.Equal(got, []string{devServer}) {
		t.Fatalf("摘除代理应通知设备: %v", got)
	}

	// 删除通知旧设备
	if _, err := mf.svc.Delete(bg, d.ID); err != nil {
		t.Fatal(err)
	}
	if got := mf.remote.take(); !slices.Equal(got, []string{devServer}) {
		t.Fatalf("删除应通知设备: %v", got)
	}
}

func TestListByMachine(t *testing.T) {
	mf := newMachineFx(t)
	cfg := map[string]any{"host": "a"}
	for _, c := range []struct{ machine, name string }{
		{devServer, "b"}, {devServer, "a"}, {devVPS, "c"}, {"hub", "d"},
	} {
		if _, err := mf.createOn(c.machine, "dual", c.name, cfg); err != nil {
			t.Fatal(err)
		}
	}
	list, err := mf.svc.ListByMachine(bg, devServer)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, in := range list {
		if in.RunsOn != devServer {
			t.Fatalf("混入了其他机器的实例: %+v", in)
		}
		names = append(names, in.Name)
	}
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("ListByMachine = %v", names)
	}
	if empty, err := mf.svc.ListByMachine(bg, "ghost"); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("无实例的机器应返回空列表: %v %v", empty, err)
	}
}

func TestRemoteNil时远程实例只是不调度(t *testing.T) {
	mf := newMachineFx(t)
	mf.svc.UseRemote(nil)
	mf.start()
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if mf.svc.isScheduled(d.ID) {
		t.Fatal("远程实例不应进入调度器")
	}
}
