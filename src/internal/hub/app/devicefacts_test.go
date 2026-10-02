package app

import (
	"context"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
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
	if f, ok := (deviceStates{a.devices}).Device(dev.ID); !ok || f.Role != model.DeviceRoleVPS {
		t.Fatalf("适配器 = %+v %v", f, ok)
	}
	if _, ok := (deviceStates{a.devices}).Device("ghost"); ok {
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
