package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/agentsrv"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// fakeSessions 是 agent 会话的结束信号替身：add 登记一个未结束的会话，测试自己关闭它。
type fakeSessions struct {
	mu    sync.Mutex
	done  map[string]chan struct{}
	asked chan string
}

func (f *fakeSessions) add(id string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.done[id] = ch
	return ch
}

func (f *fakeSessions) SessionDone(id string) (<-chan struct{}, bool) {
	f.mu.Lock()
	ch, ok := f.done[id]
	asked := f.asked
	f.mu.Unlock()
	if asked != nil {
		select {
		case asked <- id:
		default:
		}
	}
	return ch, ok
}

var deviceRoutes = []struct{ m, p string }{
	{"GET", "/api/devices"}, {"GET", "/api/devices/x"}, {"PUT", "/api/devices/x"}, {"DELETE", "/api/devices/x"},
	{"POST", "/api/devices/pairing"}, {"GET", "/api/devices/x/impact"}, {"POST", "/api/devices/x/repair"},
	{"GET", "/api/agent/info"},
}

func TestDevices各接口需要管理员(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	for _, rt := range deviceRoutes {
		resp, data := e.do(anon, rt.m, rt.p, map[string]any{})
		e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	}
	e.setup()
	sc := e.screenClient()
	for _, rt := range deviceRoutes {
		resp, data := e.do(sc, rt.m, rt.p, map[string]any{})
		e.expectError(resp, data, http.StatusForbidden, "auth.forbidden")
	}
}

func TestDevices列表与详情带运行时状态(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("srv", model.DeviceRoleServer)

	resp, data := e.do(c, "GET", "/api/devices", nil)
	list := decode[[]model.Device](t, data)
	if resp.StatusCode != http.StatusOK || len(list) != 1 || list[0].ID != dev.ID || list[0].Presence != model.PresencePending {
		t.Fatalf("列表 = %d %s", resp.StatusCode, data)
	}
	e.pres.Connected(dev.ID)
	resp, data = e.do(c, "GET", "/api/devices/"+dev.ID, nil)
	got := decode[model.Device](t, data)
	if resp.StatusCode != http.StatusOK || got.Name != "srv" || got.Presence != model.PresenceOnline {
		t.Fatalf("详情 = %d %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "credential") {
		t.Fatalf("响应不得含凭据: %s", data)
	}
	// 空列表是 []，不是 null
	e2 := newEnv(t)
	c2 := e2.setup()
	if _, data = e2.do(c2, "GET", "/api/devices", nil); strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("空列表 = %s", data)
	}
	for _, id := range []string{"hub", "ghost"} {
		resp, data = e.do(c, "GET", "/api/devices/"+id, nil)
		e.expectError(resp, data, http.StatusNotFound, "device.not_found")
	}
}

func TestDevices更新属性与拨出地址规范化(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("srv", model.DeviceRoleServer)
	other := e.addDevice("other", model.DeviceRoleNAS)

	body := map[string]any{"name": "新名称", "role": "nas", "allow_offline": true, "maintenance": true,
		"address": "wss://VPS.example.com", "proxy_id": ""}
	resp, data := e.do(c, "PUT", "/api/devices/"+dev.ID, body)
	got := decode[model.Device](t, data)
	if resp.StatusCode != http.StatusOK || got.Name != "新名称" || got.Role != "nas" || !got.AllowOffline || !got.Maintenance {
		t.Fatalf("更新 = %d %s", resp.StatusCode, data)
	}
	if got.Address != "wss://VPS.example.com:31418" {
		t.Fatalf("地址应规范化后入库: %q", got.Address)
	}

	// 校验失败一律 400 validation.failed
	for _, tc := range []struct {
		field string
		body  map[string]any
	}{
		{"name", map[string]any{"name": "", "role": "nas"}},
		{"name", map[string]any{"name": other.Name, "role": "nas"}},
		{"role", map[string]any{"name": "x", "role": "bogus"}},
		{"address", map[string]any{"name": "x", "role": "nas", "address": "http://bad"}},
		{"address", map[string]any{"name": "x", "role": "nas", "address": " "}},
		{"proxy_id", map[string]any{"name": "x", "role": "nas", "proxy_id": "ghost"}},
	} {
		resp, data = e.do(c, "PUT", "/api/devices/"+dev.ID, tc.body)
		er := e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
		if fields, _ := er.Error.Details["fields"].(map[string]any); fields[tc.field] == nil {
			t.Fatalf("details = %v，应含字段 %s", er.Error.Details, tc.field)
		}
	}
	resp, data = e.do(c, "PUT", "/api/devices/ghost", map[string]any{"name": "x", "role": "nas"})
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
	resp, data = e.do(c, "PUT", "/api/devices/"+dev.ID, map[string]any{"name": "x", "bogus": 1})
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")
}

// 拨入设备没有可编辑的地址：地址与代理字段被忽略，不做校验。
func TestDevices拨入设备忽略地址字段(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	code, _, err := e.devices.NewPairingCode(context.Background(), devices.PairingOptions{Name: "in"})
	must(t, err)
	pc, err := e.devices.ConsumePairingCode(context.Background(), devices.HashPairingCode(code))
	must(t, err)
	must(t, e.devices.CreatePaired(context.Background(), devices.PairedDevice{CredentialHash: "00ff00ff00ff00ff00ff00ff00ff00ff", Code: pc}))
	list, _ := e.devices.List(context.Background())
	id := list[0].ID
	resp, data := e.do(c, "PUT", "/api/devices/"+id, map[string]any{"name": "in", "role": "server", "address": "garbage", "proxy_id": "ghost"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
}

// Ruling 4：设备改为 vps 时，其上实例用了非 any 代理的会被拒绝并列出。
func TestDevices改为VPS时检查实例代理(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	ctx := context.Background()
	dev := e.addDevice("srv", model.DeviceRoleServer)
	lan, err := e.deps.Proxies.Create(ctx, model.ProxyInput{Name: "lan", Scheme: "http", Address: "10.0.0.1:8080", Location: model.ProxyLocationLAN})
	must(t, err)
	anyP, err := e.deps.Proxies.Create(ctx, model.ProxyInput{Name: "any", Scheme: "http", Address: "10.0.0.2:8080"})
	must(t, err)
	create := func(name, via string) model.InstanceDetail {
		t.Helper()
		resp, data := e.do(c, "POST", "/api/instances", map[string]any{
			"plugin_id": "remote-probe", "name": name, "runs_on": dev.ID, "config": map[string]any{"host": "a", "via": via}})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("创建实例 = %d %s", resp.StatusCode, data)
		}
		return decode[model.InstanceDetail](t, data)
	}
	bad := create("走lan", lan.ID)
	create("走any", anyP.ID)

	put := func(role string) (*http.Response, []byte) {
		return e.do(c, "PUT", "/api/devices/"+dev.ID, map[string]any{"name": "srv", "role": role})
	}
	resp, data := put("vps")
	er := e.expectError(resp, data, http.StatusUnprocessableEntity, "proxy.location_mismatch")
	insts, _ := er.Error.Details["instances"].([]any)
	if len(insts) != 1 || insts[0].(map[string]any)["id"] != bad.ID || insts[0].(map[string]any)["name"] != "走lan" {
		t.Fatalf("details = %v", er.Error.Details)
	}
	if d, _ := e.devices.Get(ctx, dev.ID); d.Role != model.DeviceRoleServer {
		t.Fatalf("被拒后角色不应改变: %s", d.Role)
	}
	// 改成 nas 不受限；把违规实例改成 any 代理后再改 vps 成功
	if resp, data = put("nas"); resp.StatusCode != http.StatusOK {
		t.Fatalf("改为 nas = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(c, "PUT", "/api/instances/"+bad.ID, map[string]any{
		"name": "走lan", "config": map[string]any{"host": "a", "via": anyP.ID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改实例代理 = %d %s", resp.StatusCode, data)
	}
	if resp, data = put("vps"); resp.StatusCode != http.StatusOK {
		t.Fatalf("改为 vps = %d %s", resp.StatusCode, data)
	}
}

// Ruling 14：代理可用位置改窄时，违规的设备实例阻止修改。
func TestProxy改窄可用位置时检查设备实例(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	vps := e.addDevice("vps", model.DeviceRoleVPS)
	srv := e.addDevice("srv", model.DeviceRoleServer)
	px := createProxy(t, e, c, map[string]any{"name": "p", "scheme": "http", "address": "10.0.0.1:8080"})
	mk := func(machine, name string) model.InstanceDetail {
		t.Helper()
		resp, data := e.do(c, "POST", "/api/instances", map[string]any{
			"plugin_id": "remote-probe", "name": name, "runs_on": machine, "config": map[string]any{"host": "a", "via": px.ID}})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("创建实例 = %d %s", resp.StatusCode, data)
		}
		return decode[model.InstanceDetail](t, data)
	}
	onVPS := mk(vps.ID, "VPS实例")
	onSrv := mk(srv.ID, "服务器实例")
	putProxy := func(loc string) (*http.Response, []byte) {
		return e.do(c, "PUT", "/api/proxies/"+px.ID, map[string]any{"name": "p", "scheme": "http", "address": "10.0.0.1:8080", "location": loc})
	}

	resp, data := putProxy("lan")
	er := e.expectError(resp, data, http.StatusUnprocessableEntity, "proxy.location_mismatch")
	insts, _ := er.Error.Details["instances"].([]any)
	if len(insts) != 1 || insts[0].(map[string]any)["id"] != onVPS.ID {
		t.Fatalf("lan 的冲突 = %v", er.Error.Details)
	}
	resp, data = putProxy("hub")
	er = e.expectError(resp, data, http.StatusUnprocessableEntity, "proxy.location_mismatch")
	if insts, _ = er.Error.Details["instances"].([]any); len(insts) != 2 {
		t.Fatalf("hub 的冲突 = %v", er.Error.Details)
	}
	if p := decode[model.Proxy](t, mustGet(t, e, c, "/api/proxies/"+px.ID)); p.Location != model.ProxyLocationAny {
		t.Fatalf("被拒后位置不应改变: %s", p.Location)
	}
	// 非法取值仍是字段校验错误，不是位置冲突
	resp, data = putProxy("moon")
	e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
	// 违规实例搬走后可以改窄
	if resp, data = e.do(c, "DELETE", "/api/instances/"+onVPS.ID+"?confirm=1", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("删除实例 = %d %s", resp.StatusCode, data)
	}
	if resp, data = putProxy("lan"); resp.StatusCode != http.StatusOK {
		t.Fatalf("改为 lan = %d %s", resp.StatusCode, data)
	}
	_ = onSrv
}

func mustGet(t *testing.T, e *env, c *http.Client, path string) []byte {
	t.Helper()
	resp, data := e.do(c, "GET", path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, resp.StatusCode, data)
	}
	return data
}

func TestDevices配对命令含地址指纹与配对码(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	resp, data := e.do(c, "POST", "/api/devices/pairing", model.PairingRequest{Name: "nas1", Role: model.DeviceRoleNAS})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("pairing = %d %s", resp.StatusCode, data)
	}
	res := decode[model.PairingResult](t, data)
	if res.Fingerprint != "SHA256:testfp" || len(res.URLs) != 1 || res.URLs[0] != "wss://192.168.7.8:31418" {
		t.Fatalf("结果 = %+v", res)
	}
	if !res.ExpiresAt.Equal(e.clk.Now().Add(devices.PairingCodeTTL)) {
		t.Fatalf("过期时间 = %v", res.ExpiresAt)
	}
	want := "curl -fsSL http://192.168.7.8:41999/agent/install.sh | sh -s -- --hub wss://192.168.7.8:31418 --fp SHA256:testfp --code " + res.Code
	if res.Command != want {
		t.Fatalf("命令 = %q\n期望 %q", res.Command, want)
	}
	if len(res.Steps) == 0 {
		t.Fatal("应有分步命令")
	}
	last := res.Steps[len(res.Steps)-1]
	for _, part := range []string{"pimon-agent install", "--hub wss://192.168.7.8:31418", "--fp SHA256:testfp", "--code " + res.Code} {
		if !strings.Contains(last, part) {
			t.Fatalf("最后一步 %q 缺少 %q", last, part)
		}
	}
	joined := strings.Join(res.Steps, "\n")
	if !strings.Contains(joined, "http://192.168.7.8:41999/agent/download/") || !strings.Contains(joined, "sha256sum") {
		t.Fatalf("分步命令应含下载与 sha256 校验: %s", joined)
	}
	// 配对码真实可用，预填信息随码保存
	pc, err := e.devices.ConsumePairingCode(context.Background(), devices.HashPairingCode(res.Code))
	if err != nil || pc.Name != "nas1" || pc.Role != model.DeviceRoleNAS || !pc.AutoHostMetrics || pc.RepairDeviceID != "" {
		t.Fatalf("配对码 = %+v %v", pc, err)
	}
}

func TestDevices配对地址优先用对外访问地址(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	s := e.deps.Settings.Get()
	s.AccessURL = "https://pimon.home"
	must(t, e.deps.Settings.Update(context.Background(), s))
	_, data := e.do(c, "POST", "/api/devices/pairing", map[string]any{})
	res := decode[model.PairingResult](t, data)
	if len(res.URLs) != 2 || res.URLs[0] != "wss://pimon.home:31418" || res.URLs[1] != "wss://192.168.7.8:31418" {
		t.Fatalf("urls = %v", res.URLs)
	}
	if !strings.Contains(res.Command, "curl -fsSL https://pimon.home/agent/install.sh") || !strings.Contains(res.Command, "--hub wss://pimon.home:31418") {
		t.Fatalf("命令 = %s", res.Command)
	}
}

func TestDevices配对校验与通道未启用(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	for _, body := range []map[string]any{{"role": "bogus"}, {"name": strings.Repeat("长", 65)}} {
		resp, data := e.do(c, "POST", "/api/devices/pairing", body)
		e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
	}
	*e.agentCh = AgentChannel{}
	resp, data := e.do(c, "POST", "/api/devices/pairing", map[string]any{})
	e.expectError(resp, data, http.StatusConflict, "agent.disabled")
	resp, data = e.do(c, "POST", "/api/devices/x/repair", map[string]any{})
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
}

func TestAgentInfo(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	resp, data := e.do(c, "GET", "/api/agent/info", nil)
	info := decode[model.AgentInfo](t, data)
	if resp.StatusCode != http.StatusOK || info.AgentPort != 31418 || info.Fingerprint != "SHA256:testfp" || info.HubVersion != "v-test" ||
		info.ProtocolVersion != 1 || info.MinCompatibleVersion != 1 || len(info.Platforms) != 1 || info.Platforms[0] != "linux-amd64" {
		t.Fatalf("info = %d %s", resp.StatusCode, data)
	}
	*e.agentCh = AgentChannel{Fingerprint: "SHA256:testfp", HubVersion: "v-test"}
	_, data = e.do(c, "GET", "/api/agent/info", nil)
	info = decode[model.AgentInfo](t, data)
	if info.AgentPort != 0 || !strings.Contains(string(data), `"platforms":[]`) {
		t.Fatalf("未启用 = %s", data)
	}
}

func TestDevices影响列表含实例与screen引用(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("srv", model.DeviceRoleServer)
	otherDev := e.addDevice("other", model.DeviceRoleServer)
	mk := func(machine, name string) model.InstanceDetail {
		t.Helper()
		resp, data := e.do(c, "POST", "/api/instances", map[string]any{
			"plugin_id": "remote-probe", "name": name, "runs_on": machine, "config": map[string]any{"host": "a"}})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("创建实例 = %d %s", resp.StatusCode, data)
		}
		return decode[model.InstanceDetail](t, data)
	}
	a, b, other := mk(dev.ID, "a"), mk(dev.ID, "b"), mk(otherDev.ID, "o")
	w := func(id string, col int, inst string) model.LayoutWidget {
		x := apiGenericValue(id, col, inst)
		x.Binding.Refs[0].Item = "v"
		return x
	}
	// 两个小组件引用同一台设备上的实例（落在同一个 screen），另一台设备的实例不计入。
	layout := apiLayout(w("w1", 0, a.ID), w("w2", 1, b.ID), w("w3", 2, other.ID))
	if resp, data := putLayout(e, c, 0, layout); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT layout = %d %s", resp.StatusCode, data)
	}

	resp, data := e.do(c, "GET", "/api/devices/"+dev.ID+"/impact", nil)
	imp := decode[model.DeviceImpact](t, data)
	if resp.StatusCode != http.StatusOK || len(imp.Instances) != 2 || imp.Instances[0].Name != "a" || imp.Instances[1].Name != "b" {
		t.Fatalf("impact = %d %s", resp.StatusCode, data)
	}
	if len(imp.Screens) != 1 || imp.Screens[0].ID != "index" || imp.Screens[0].Name != "首页" {
		t.Fatalf("screens 应按 ID 去重: %+v", imp.Screens)
	}
	if imp.Rules == nil || len(imp.Rules) != 0 || !strings.Contains(string(data), `"rules":[]`) {
		t.Fatalf("rules 应为空数组: %s", data)
	}
	// 没有实例的设备：全是空数组
	empty := e.addDevice("empty", model.DeviceRoleNAS)
	_, data = e.do(c, "GET", "/api/devices/"+empty.ID+"/impact", nil)
	if !strings.Contains(string(data), `"instances":[]`) || !strings.Contains(string(data), `"screens":[]`) {
		t.Fatalf("空影响 = %s", data)
	}
	resp, data = e.do(c, "GET", "/api/devices/ghost/impact", nil)
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
}

// 吊销要等会话真正断开才返回。
func TestDevices吊销等会话断开后才返回(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("srv", model.DeviceRoleServer)
	done := e.sessions.add(dev.ID)
	e.sessions.asked = make(chan string, 4)

	type result struct {
		resp *http.Response
		data []byte
	}
	resCh := make(chan result, 1)
	go func() {
		resp, data := e.do(c, "DELETE", "/api/devices/"+dev.ID, nil)
		resCh <- result{resp, data}
	}()
	<-e.sessions.asked
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := e.devices.Get(context.Background(), dev.ID); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("设备行应被删除")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case r := <-resCh:
		t.Fatalf("会话未断开时不应返回: %d %s", r.resp.StatusCode, r.data)
	default:
	}
	close(done)
	select {
	case r := <-resCh:
		if r.resp.StatusCode != http.StatusNoContent {
			t.Fatalf("吊销 = %d %s", r.resp.StatusCode, r.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("会话断开后应返回")
	}
	resp, data := e.do(c, "GET", "/api/devices/"+dev.ID, nil)
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
	resp, data = e.do(c, "DELETE", "/api/devices/"+dev.ID, nil)
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
}

// 会话迟迟不断开时，吊销在上限后仍然返回（设备行已删除，不能卡住请求）。
func TestDevices吊销等待有上限(t *testing.T) {
	old := revokeWait
	revokeWait = 20 * time.Millisecond
	t.Cleanup(func() { revokeWait = old })
	e := newEnv(t)
	c := e.setup()
	dev := e.addDevice("srv", model.DeviceRoleServer)
	e.sessions.add(dev.ID) // 永不关闭
	resp, data := e.do(c, "DELETE", "/api/devices/"+dev.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("吊销 = %d %s", resp.StatusCode, data)
	}
}

func TestDevices重新配对拨入设备生成绑定旧设备的新配对码(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	ctx := context.Background()
	code, _, err := e.devices.NewPairingCode(ctx, devices.PairingOptions{Name: "nas", Role: model.DeviceRoleNAS})
	must(t, err)
	pc, err := e.devices.ConsumePairingCode(ctx, devices.HashPairingCode(code))
	must(t, err)
	must(t, e.devices.CreatePaired(ctx, devices.PairedDevice{CredentialHash: "00ff00ff00ff00ff00ff00ff00ff00ff", Code: pc}))
	list, _ := e.devices.List(ctx)
	id := list[0].ID
	_, err = e.devices.Update(ctx, id, model.DeviceInput{Name: "nas", Role: model.DeviceRoleNAS, AllowOffline: true, Maintenance: true})
	must(t, err)

	resp, data := e.do(c, "POST", "/api/devices/"+id+"/repair", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("repair = %d %s", resp.StatusCode, data)
	}
	res := decode[model.PairingResult](t, data)
	if !strings.Contains(res.Command, "--hub wss://192.168.7.8:31418") || !strings.Contains(res.Command, "--code "+res.Code) {
		t.Fatalf("命令 = %s", res.Command)
	}
	got, err := e.devices.ConsumePairingCode(ctx, devices.HashPairingCode(res.Code))
	if err != nil || got.RepairDeviceID != id || got.Name != "nas" || got.Role != model.DeviceRoleNAS || !got.AllowOffline || got.AutoHostMetrics {
		t.Fatalf("配对码预填 = %+v %v", got, err)
	}
	resp, data = e.do(c, "POST", "/api/devices/ghost/repair", nil)
	e.expectError(resp, data, http.StatusNotFound, "device.not_found")
}

func TestDevices重新配对拨出设备按旧设备预填并带上目标(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	ctx := context.Background()
	px, err := e.deps.Proxies.Create(ctx, model.ProxyInput{Name: "p", Scheme: "http", Address: "10.0.0.1:8080"})
	must(t, err)
	old, err := e.devices.CreateDialOut(ctx, devices.DialOutDevice{
		Name: "vps", Role: model.DeviceRoleVPS, Address: "wss://vps.example.com:31418", Pin: "SHA256:oldpin", ProxyID: px.ID, Credential: "cred-old",
	})
	must(t, err)
	e.pairer.set(model.Device{ID: "newid", Name: "vps", Direction: model.DeviceDialOut, Presence: model.PresenceOnline}, nil)

	resp, data := e.do(c, "POST", "/api/devices/"+old.ID+"/repair", map[string]any{"code": "NEW-CODE", "fingerprint": "SHA256:newpin"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("repair = %d %s", resp.StatusCode, data)
	}
	if dev := decode[model.Device](t, data); dev.ID != "newid" {
		t.Fatalf("响应设备 = %+v", dev)
	}
	e.pairer.mu.Lock()
	req, target := e.pairer.reqs[0], e.pairer.repairs[0]
	e.pairer.mu.Unlock()
	if target != old.ID || req.Code != "NEW-CODE" || req.Fingerprint != "SHA256:newpin" ||
		req.Name != "vps" || req.Role != model.DeviceRoleVPS || req.ProxyID != px.ID || req.Address != "wss://vps.example.com:31418" ||
		req.AllowOffline == nil || *req.AllowOffline || req.AutoHostMetrics == nil || *req.AutoHostMetrics {
		t.Fatalf("目标 %q，传给配对服务的请求 = %+v", target, req)
	}
	// 没给指纹时沿用旧设备保存的指纹
	e.do(c, "POST", "/api/devices/"+old.ID+"/repair", map[string]any{"code": "C2"})
	e.pairer.mu.Lock()
	fp := e.pairer.reqs[1].Fingerprint
	e.pairer.mu.Unlock()
	if fp != "SHA256:oldpin" {
		t.Fatalf("指纹应沿用旧值: %q", fp)
	}
	// 配对失败映射同 dial-out
	e.pairer.set(model.Device{}, &agentsrv.PairingError{Reason: agent.ReasonBadCode})
	resp, data = e.do(c, "POST", "/api/devices/"+old.ID+"/repair", map[string]any{"code": "C3"})
	er := e.expectError(resp, data, http.StatusBadGateway, "pairing.failed")
	if er.Error.Details["reason"] != "bad_code" {
		t.Fatalf("details = %v", er.Error.Details)
	}
}
