package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 组装后实例服务能查到设备：VPS 上的 host-metrics 实例按设备上报的 manifest 建成，
// 不在本机调度；插件列表按设备返回；不存在的设备 404。
func TestRemoteInstanceWiring(t *testing.T) {
	a := openApp(t, testConfig(t))
	c, base := setupAdmin(t, a)
	ctx := context.Background()
	dev, err := a.devices.CreateDialOut(ctx, devices.DialOutDevice{
		Name: "vps", Role: model.DeviceRoleVPS, Address: "wss://vps.example.com:31418", Credential: "cred-vps",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := newDeviceStates(a.devices, a.presence).Device(dev.ID); !ok || f.Role != model.DeviceRoleVPS {
		t.Fatalf("适配器 = %+v %v", f, ok)
	}
	if _, ok := newDeviceStates(a.devices, a.presence).Device("ghost"); ok {
		t.Fatal("不存在的设备应返回 false")
	}
	hm, ok := a.plugins.Get("host-metrics")
	if !ok {
		t.Fatal("缺少 host-metrics")
	}
	if err := a.plugins.SyncMachine(ctx, dev.ID, []plugins.MachineManifest{{Origin: plugins.OriginBuiltin, Manifest: hm.Manifest}}); err != nil {
		t.Fatal(err)
	}

	resp, data := call(t, c, base, "GET", "/api/plugins?machine="+dev.ID, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(data), `"id":"host-metrics"`) {
		t.Fatalf("设备插件列表 = %d %s", resp.StatusCode, data)
	}
	if resp, data := call(t, c, base, "GET", "/api/plugins?machine=ghost", nil); resp.StatusCode != 404 || !strings.Contains(string(data), "device.not_found") {
		t.Fatalf("不存在的设备 = %d %s", resp.StatusCode, data)
	}

	resp, data = call(t, c, base, "POST", "/api/instances", map[string]any{
		"plugin_id": "host-metrics", "name": "VPS 主机", "config": map[string]any{}, "runs_on": dev.ID,
	})
	if resp.StatusCode != 201 || !strings.Contains(string(data), `"runs_on":"`+dev.ID+`"`) {
		t.Fatalf("创建远程实例 = %d %s", resp.StatusCode, data)
	}
	list, err := a.instances.ListByMachine(ctx, dev.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByMachine = %v %v", list, err)
	}
}

// 适配器：设备属性走缓存（仓库变更时失效），在线状态实时取自 Presence；
// 离线按「允许离线」区分 Offline 与 Dormant，pending 与 disconnected 不置位。
func TestDeviceStates按在线状态与设备属性推出展示事实(t *testing.T) {
	a := openApp(t, testConfig(t))
	ctx := context.Background()
	dev, err := a.devices.CreateDialOut(ctx, devices.DialOutDevice{
		Name: "nas", Role: model.DeviceRoleNAS, Address: "wss://nas.example.com:31418", Credential: "cred-nas",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	pres := devices.NewPresence(clk, start, []string{dev.ID})
	t.Cleanup(pres.Close)
	states := newDeviceStates(a.devices, pres)

	get := func() (f struct{ off, dorm, maint bool }) {
		x, ok := states.Device(dev.ID)
		if !ok {
			t.Fatal("设备应存在")
		}
		f.off, f.dorm, f.maint = x.Offline, x.Dormant, x.Maintenance
		return f
	}
	if f := get(); f.off || f.dorm || f.maint {
		t.Fatalf("pending 不应置位: %+v", f)
	}
	pres.Connected(dev.ID)
	pres.Disconnected(dev.ID, clk.Now())
	if f := get(); f.off || f.dorm {
		t.Fatalf("disconnected 不应置位: %+v", f)
	}
	clk.Advance(devices.OfflineAfter)
	eventually(t, "设备转为 offline", func() bool { s, _ := pres.State(dev.ID); return s == model.PresenceOffline })
	if f := get(); !f.off || f.dorm {
		t.Fatalf("离线且不允许离线应为 Offline: %+v", f)
	}

	// 改成允许离线并开启维护模式：缓存随仓库变更失效。
	if _, err := a.devices.Update(ctx, dev.ID, model.DeviceInput{
		Name: "nas", Role: model.DeviceRoleNAS, AllowOffline: true, Maintenance: true,
	}); err != nil {
		t.Fatal(err)
	}
	if f := get(); f.off || !f.dorm || !f.maint {
		t.Fatalf("允许离线应为 Dormant 并带维护: %+v", f)
	}

	pres.Connected(dev.ID)
	if f := get(); f.off || f.dorm {
		t.Fatalf("恢复在线后不应置位: %+v", f)
	}

	// 吊销后缓存失效，设备不存在。
	if err := a.devices.Delete(ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := states.Device(dev.ID); ok {
		t.Fatal("吊销后应不存在")
	}
}

// 设备在线状态与属性变化后，该设备上的全部实例都会收到变化通知（本机实例与其它设备不受影响）。
func TestDevice状态变化通知其上全部实例(t *testing.T) {
	a := openApp(t, testConfig(t))
	ctx := context.Background()
	dev, err := a.devices.CreateDialOut(ctx, devices.DialOutDevice{
		Name: "vps", Role: model.DeviceRoleVPS, Address: "wss://vps.example.com:31418", Credential: "cred-vps",
	})
	if err != nil {
		t.Fatal(err)
	}
	hm, ok := a.plugins.Get("host-metrics")
	if !ok {
		t.Fatal("缺少 host-metrics")
	}
	if err := a.plugins.SyncMachine(ctx, dev.ID, []plugins.MachineManifest{{Origin: plugins.OriginBuiltin, Manifest: hm.Manifest}}); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, name := range []string{"一", "二"} {
		d, err := a.instances.Create(ctx, model.InstanceInput{
			PluginID: "host-metrics", Name: name, Config: map[string]any{}, RunsOn: dev.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, d.ID)
	}

	var mu sync.Mutex
	got := map[string]bool{}
	a.instances.OnChange(func(id string) { mu.Lock(); got[id] = true; mu.Unlock() })
	seen := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, id := range want {
			if !got[id] {
				return false
			}
		}
		return true
	}

	a.presence.Connected(dev.ID)
	eventually(t, "在线状态变化通知", seen)

	mu.Lock()
	clear(got)
	mu.Unlock()
	if _, err := a.devices.Update(ctx, dev.ID, model.DeviceInput{Name: "vps", Role: model.DeviceRoleVPS, Maintenance: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "维护模式变化通知", seen)
}
