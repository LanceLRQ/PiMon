package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

const agentOnlyYAML = `id: remote-probe
version: 2.0.0
api_version: 1
name: {zh: 远程探针, en: Remote Probe}
kind: source
runtime: exec
runs_on: [agent]
interval: 60s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, required: true}
  - {key: via, type: proxy, title: Proxy}
outputs:
  - {key: v, type: number, title: V}
`

// addDevice 建一台已上报 remote-probe 插件的设备。
func (e *env) addDevice(name, role string) model.Device {
	e.t.Helper()
	ctx := context.Background()
	dev, err := e.devices.CreateDialOut(ctx, devices.DialOutDevice{
		Name: name, Role: role, Address: "wss://" + name + ".example.com:31418", Credential: "cred-" + name,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	m, err := manifest.Parse([]byte(agentOnlyYAML))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.plugins.SyncMachine(ctx, dev.ID, []plugins.MachineManifest{{Origin: plugins.OriginExec, Manifest: m}}); err != nil {
		e.t.Fatal(err)
	}
	return dev
}

func TestPluginsByMachine(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	_, all := e.do(c, "GET", "/api/plugins", nil)
	_, hub := e.do(c, "GET", "/api/plugins?machine=hub", nil)
	if !bytes.Equal(all, hub) {
		t.Fatalf("machine=hub 应与不带参数一致:\n%s\n%s", all, hub)
	}

	dev := e.addDevice("srv", model.DeviceRoleServer)
	resp, data := e.do(c, "GET", "/api/plugins?machine="+dev.ID+"&lang=en", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("设备插件列表 = %d %s", resp.StatusCode, data)
	}
	for _, want := range []string{`"errors":[]`, `"conflicts":[]`, `"plugin_dir":""`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("响应缺少 %s: %s", want, data)
		}
	}
	l := getPlugins(t, e, c, "GET", "/api/plugins?machine="+dev.ID+"&lang=en")
	if len(l.Plugins) != 1 || l.Plugins[0].ID != "remote-probe" || l.Plugins[0].Name != "Remote Probe" ||
		l.Plugins[0].Origin != "exec" || len(l.Plugins[0].RunsOn) != 1 || l.Plugins[0].RunsOn[0] != "agent" {
		t.Fatalf("设备插件 = %+v", l.Plugins)
	}

	resp, data = e.do(c, "GET", "/api/plugins?machine=ghost", nil)
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
}

func TestInstanceMachineErrors(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	vps := e.addDevice("vps", model.DeviceRoleVPS)
	create := func(machine, plugin string, cfg map[string]any) (*http.Response, []byte) {
		t.Helper()
		return e.do(c, "POST", "/api/instances", map[string]any{"plugin_id": plugin, "name": "x", "config": cfg, "runs_on": machine})
	}
	cfg := map[string]any{"host": "a"}

	resp, data := create("ghost", "remote-probe", cfg)
	er := e.expectError(resp, data, http.StatusUnprocessableEntity, "instance.machine_invalid")
	if er.Error.Details["reason"] != "device_not_found" {
		t.Fatalf("details = %v", er.Error.Details)
	}
	resp, data = create(vps.ID, "plain", map[string]any{"name": "n"})
	er = e.expectError(resp, data, http.StatusUnprocessableEntity, "instance.machine_invalid")
	if er.Error.Details["reason"] != "runs_on_mismatch" {
		t.Fatalf("details = %v", er.Error.Details)
	}
	// agent-only 插件不能建成 hub 实例（hub 注册表里没有它时仍是 plugin.not_found）
	resp, data = create("hub", "remote-probe", cfg)
	e.expectError(resp, data, http.StatusNotFound, "plugin.not_found")

	ctx := context.Background()
	lan, err := e.deps.Proxies.Create(ctx, model.ProxyInput{Name: "lan", Scheme: "http", Address: "10.0.0.1:8080", Location: model.ProxyLocationLAN})
	must(t, err)
	anyP, err := e.deps.Proxies.Create(ctx, model.ProxyInput{Name: "any", Scheme: "http", Address: "10.0.0.2:8080"})
	must(t, err)
	resp, data = create(vps.ID, "remote-probe", map[string]any{"host": "a", "via": lan.ID})
	er = e.expectError(resp, data, http.StatusUnprocessableEntity, "proxy.location_mismatch")
	if er.Error.Details["field"] != "via" || er.Error.Details["location"] != "lan" {
		t.Fatalf("details = %v", er.Error.Details)
	}

	resp, data = create(vps.ID, "remote-probe", map[string]any{"host": "a", "via": anyP.ID})
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(data), `"runs_on":"`+vps.ID+`"`) {
		t.Fatalf("VPS 上用 any 代理应成功: %d %s", resp.StatusCode, data)
	}
}
