package instances

import (
	"slices"
	"sync"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// setupRemoteOK 创建运行在 devServer 上的实例，并让它有一份成功报告。
func (mf *machineFx) setupRemoteOK(t *testing.T) model.InstanceDetail {
	t.Helper()
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	must(t, err)
	mf.svc.ApplyRemoteResult(d.ID, mf.contentHash(t, d.ID), &report.Report{Status: report.StatusOK, Summary: "温度"}, nil)
	return d
}

func (mf *machineFx) stateOf(t *testing.T, id string) model.Instance {
	t.Helper()
	v, err := mf.svc.View(bg, id)
	must(t, err)
	return v
}

func TestDevice状态离线变offline恢复后回到原状态(t *testing.T) {
	mf := newMachineFx(t)
	d := mf.setupRemoteOK(t)
	devs := fakeDevices{devServer: {Role: model.DeviceRoleServer}}
	mf.svc.UseDevices(devs)
	if v := mf.stateOf(t, d.ID); v.DisplayState != "ok" {
		t.Fatalf("在线时 = %s", v.DisplayState)
	}
	devs[devServer] = DeviceFacts{Role: model.DeviceRoleServer, Offline: true}
	v := mf.stateOf(t, d.ID)
	if v.DisplayState != "offline" {
		t.Fatalf("离线 = %s", v.DisplayState)
	}
	if v.Summary != "温度" {
		t.Fatalf("离线期间应保留最后的数据: %+v", v)
	}
	devs[devServer] = DeviceFacts{Role: model.DeviceRoleServer}
	if v := mf.stateOf(t, d.ID); v.DisplayState != "ok" {
		t.Fatalf("恢复后 = %s", v.DisplayState)
	}
}

func TestDevice状态允许离线变dormant(t *testing.T) {
	mf := newMachineFx(t)
	d := mf.setupRemoteOK(t)
	mf.svc.UseDevices(fakeDevices{devServer: {Role: model.DeviceRoleDev, Dormant: true}})
	v := mf.stateOf(t, d.ID)
	if v.DisplayState != "dormant" || v.Summary != "温度" {
		t.Fatalf("休眠 = %+v", v)
	}
}

func TestDevice状态维护模式(t *testing.T) {
	mf := newMachineFx(t)
	d := mf.setupRemoteOK(t)
	mf.svc.UseDevices(fakeDevices{devServer: {Role: model.DeviceRoleServer, Maintenance: true}})
	if v := mf.stateOf(t, d.ID); v.DisplayState != "maintenance" {
		t.Fatalf("维护 = %s", v.DisplayState)
	}
	// 离线优先于维护
	mf.svc.UseDevices(fakeDevices{devServer: {Role: model.DeviceRoleServer, Offline: true, Maintenance: true}})
	if v := mf.stateOf(t, d.ID); v.DisplayState != "offline" {
		t.Fatalf("离线+维护 = %s", v.DisplayState)
	}
}

func TestDevice吊销后变broken且优先于其它问题(t *testing.T) {
	mf := newMachineFx(t)
	d := mf.setupRemoteOK(t)
	mf.svc.UseDevices(fakeDevices{})
	v := mf.stateOf(t, d.ID)
	if v.DisplayState != "broken" || v.Issue != "设备已吊销或不存在" {
		t.Fatalf("吊销后 = %s / %q", v.DisplayState, v.Issue)
	}
	det, err := mf.svc.Get(bg, d.ID)
	must(t, err)
	if det.DisplayState != "broken" || det.Issue != "设备已吊销或不存在" {
		t.Fatalf("详情 = %s / %q", det.DisplayState, det.Issue)
	}
}

func TestDevice本机实例不受设备状态影响(t *testing.T) {
	mf := newMachineFx(t)
	d, err := mf.svc.Create(bg, model.InstanceInput{PluginID: "probe", Name: "本机", Config: probeCfg("a")})
	must(t, err)
	mf.svc.UseDevices(fakeDevices{})
	if v := mf.stateOf(t, d.ID); v.DisplayState == "broken" || v.Issue != "" {
		t.Fatalf("本机实例 = %s / %q", v.DisplayState, v.Issue)
	}
}

func TestNotifyMachine通知该设备全部实例(t *testing.T) {
	mf := newMachineFx(t)
	a, err := mf.createOn(devServer, "agentonly", "a", map[string]any{"host": "a"})
	must(t, err)
	b, err := mf.createOn(devServer, "agentonly", "b", map[string]any{"host": "b"})
	must(t, err)
	c, err := mf.createOn(devVPS, "agentonly", "c", map[string]any{"host": "c"})
	must(t, err)
	h, err := mf.svc.Create(bg, model.InstanceInput{PluginID: "probe", Name: "h", Config: probeCfg("a")})
	must(t, err)

	var mu sync.Mutex
	var got []string
	mf.svc.OnChange(func(id string) { mu.Lock(); got = append(got, id); mu.Unlock() })
	must(t, mf.svc.NotifyMachine(bg, devServer))
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(got)
	want := []string{a.ID, b.ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("通知 = %v, want %v（不含 %s、%s）", got, want, c.ID, h.ID)
	}
}
