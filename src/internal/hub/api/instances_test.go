package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

const collectorManifest = `id: collector
version: 1.0.0
api_version: 1
name: Collector
kind: source
runtime: builtin
runs_on: [hub]
interval: 60s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, required: true}
  - {key: api_key, type: secret, title: Key, required: true}
outputs:
  - {key: temp, type: number, title: Temp}
`

// collectorSource 按 host 取值决定行为：fail 失败、slow 超时，其余成功。
type collectorSource struct{ m *manifest.Manifest }

func (c collectorSource) Manifest() *manifest.Manifest { return c.m }

func (collectorSource) Collect(_ context.Context, in runtime.Input) (*report.Report, error) {
	switch in.Config["host"] {
	case "fail":
		return nil, fmt.Errorf("连接失败 key=%s", in.Secrets["api_key"])
	case "slow":
		return nil, fmt.Errorf("慢: %w", context.DeadlineExceeded)
	}
	v := 20.0
	return &report.Report{Status: report.StatusOK, Summary: fmt.Sprint("host=", in.Config["host"]),
		Items: []report.Item{{Key: "temp", Type: report.TypeNumber, Value: &v}}}, nil
}

func newInstEnv(t *testing.T) (*env, *http.Client) {
	t.Helper()
	m, err := manifest.Parse([]byte(collectorManifest))
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, collectorSource{m})
	return e, e.setup()
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("响应解析失败: %v: %s", err, data)
	}
	return v
}

func createInst(t *testing.T, e *env, c *http.Client, host string) model.InstanceDetail {
	t.Helper()
	resp, data := e.do(c, "POST", "/api/instances", model.InstanceInput{
		PluginID: "collector", Name: "实例 " + host, Config: map[string]any{"host": host, "api_key": "s3cret-key"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d %s", resp.StatusCode, data)
	}
	return decode[model.InstanceDetail](t, data)
}

func TestInstancesRequireAdmin(t *testing.T) {
	e := newEnv(t)
	anon := e.newClient()
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/instances"}, {"POST", "/api/instances"}, {"GET", "/api/instances/x"},
		{"PUT", "/api/instances/x"}, {"DELETE", "/api/instances/x"}, {"POST", "/api/instances/x/run"},
		{"POST", "/api/instances/x/pause"}, {"POST", "/api/instances/x/resume"}, {"POST", "/api/instances/x/copy"},
	} {
		resp, data := e.do(anon, c.m, c.p, map[string]any{})
		e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	}
}

func TestInstanceCRUDAndSecrets(t *testing.T) {
	e, admin := newInstEnv(t)
	d := createInst(t, e, admin, "a")
	if d.ID == "" || d.RunsOn != "hub" || d.DisplayState != "unknown" || d.EffectiveIntervalSeconds != 60 {
		t.Fatalf("新建结果 = %+v", d.Instance)
	}

	// GET / 列表都不回显密钥
	for _, path := range []string{"/api/instances/" + d.ID, "/api/instances"} {
		resp, data := e.do(admin, "GET", path, nil)
		if resp.StatusCode != http.StatusOK || strings.Contains(string(data), "s3cret-key") {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, data)
		}
	}
	_, data := e.do(admin, "GET", "/api/instances/"+d.ID, nil)
	got := decode[model.InstanceDetail](t, data)
	if m, ok := got.Config["api_key"].(map[string]any); !ok || m["set"] != true {
		t.Fatalf("api_key 应回显 {set:true}: %v", got.Config)
	}

	// PUT 密钥留空保留原值：运行后不报错（required 不触发）且库里密文不变
	var before string
	must(t, e.db.QueryRow(`SELECT secrets_enc FROM plugin_instances WHERE id=?`, d.ID).Scan(&before))
	resp, data := e.do(admin, "PUT", "/api/instances/"+d.ID, model.InstanceInput{
		Name: "改名", Config: map[string]any{"host": "a", "api_key": ""}, IntervalSeconds: 30,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d %s", resp.StatusCode, data)
	}
	upd := decode[model.InstanceDetail](t, data)
	if upd.Name != "改名" || upd.IntervalSeconds != 30 || upd.EffectiveIntervalSeconds != 30 {
		t.Fatalf("PUT 结果 = %+v", upd.Instance)
	}
	var after string
	must(t, e.db.QueryRow(`SELECT secrets_enc FROM plugin_instances WHERE id=?`, d.ID).Scan(&after))
	if before == "" || before != after {
		t.Fatal("密钥留空应保留原密文")
	}

	// 校验失败：字段错误
	resp, data = e.do(admin, "POST", "/api/instances", model.InstanceInput{PluginID: "collector", Name: "", Config: map[string]any{"host": "x"}})
	er := e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
	fields, _ := er.Error.Details["fields"].(map[string]any)
	if fields["name"] != "required" || fields["api_key"] != "required" {
		t.Fatalf("字段错误 = %v", fields)
	}
	// 未知插件、未知实例、坏 JSON
	resp, data = e.do(admin, "POST", "/api/instances", model.InstanceInput{PluginID: "nope", Name: "x", Config: map[string]any{}})
	e.expectError(resp, data, http.StatusNotFound, "plugin.not_found")
	for _, p := range []string{"GET", "PUT", "DELETE"} {
		resp, data = e.do(admin, p, "/api/instances/missing", model.InstanceInput{Name: "x"})
		e.expectError(resp, data, http.StatusNotFound, "instance.not_found")
	}
	resp, data = e.do(admin, "POST", "/api/instances", map[string]any{"name": 1})
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")

	// 删除：本期无受影响 screen，直接删除并返回空列表
	resp, data = e.do(admin, "DELETE", "/api/instances/"+d.ID, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"affected_screens":[]`) {
		t.Fatalf("DELETE = %d %s", resp.StatusCode, data)
	}
	resp, data = e.do(admin, "GET", "/api/instances/"+d.ID, nil)
	e.expectError(resp, data, http.StatusNotFound, "instance.not_found")
}

func TestInstanceRunSuccessFailureTimeout(t *testing.T) {
	e, admin := newInstEnv(t)

	ok := createInst(t, e, admin, "good")
	resp, data := e.do(admin, "POST", "/api/instances/"+ok.ID+"/run", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run = %d %s", resp.StatusCode, data)
	}
	res := decode[model.InstanceRunResult](t, data)
	if res.Report == nil || res.Report.Summary != "host=good" || res.Instance.DisplayState != "ok" {
		t.Fatalf("成功结果 = %s", data)
	}
	// 列表带 display_state、paused 与摘要
	_, data = e.do(admin, "GET", "/api/instances", nil)
	list := decode[[]model.Instance](t, data)
	if len(list) != 1 || list[0].Summary != "host=good" || list[0].DisplayState != "ok" || list[0].LastSuccessAt == nil || list[0].Paused {
		t.Fatalf("列表 = %s", data)
	}

	bad := createInst(t, e, admin, "fail")
	resp, data = e.do(admin, "POST", "/api/instances/"+bad.ID+"/run", nil)
	er := e.expectError(resp, data, http.StatusBadGateway, "run.failed")
	if msg, _ := er.Error.Details["message"].(string); msg == "" || strings.Contains(msg, "s3cret-key") {
		t.Fatalf("失败信息应已脱敏且非空: %v", er.Error.Details)
	}
	_, data = e.do(admin, "GET", "/api/instances/"+bad.ID, nil)
	if d := decode[model.InstanceDetail](t, data); d.DisplayState != "error" || d.Failures != 1 || d.LastError == "" {
		t.Fatalf("失败后状态 = %+v", d.Instance)
	}

	slow := createInst(t, e, admin, "slow")
	resp, data = e.do(admin, "POST", "/api/instances/"+slow.ID+"/run", nil)
	e.expectError(resp, data, http.StatusGatewayTimeout, "run.timeout")

	resp, data = e.do(admin, "POST", "/api/instances/missing/run", nil)
	e.expectError(resp, data, http.StatusNotFound, "instance.not_found")
}

func TestInstancePauseResumeCopy(t *testing.T) {
	e, admin := newInstEnv(t)
	d := createInst(t, e, admin, "a")

	resp, data := e.do(admin, "POST", "/api/instances/"+d.ID+"/pause", nil)
	if resp.StatusCode != http.StatusOK || !decode[model.Instance](t, data).Paused {
		t.Fatalf("pause = %d %s", resp.StatusCode, data)
	}
	_, data = e.do(admin, "GET", "/api/instances", nil)
	if l := decode[[]model.Instance](t, data); !l[0].Paused {
		t.Fatalf("列表应带 paused: %s", data)
	}
	resp, data = e.do(admin, "POST", "/api/instances/"+d.ID+"/resume", nil)
	if resp.StatusCode != http.StatusOK || decode[model.Instance](t, data).Paused {
		t.Fatalf("resume = %d %s", resp.StatusCode, data)
	}

	// 复制：名称带后缀，密钥置空，需重新填写
	resp, data = e.do(admin, "POST", "/api/instances/"+d.ID+"/copy", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy = %d %s", resp.StatusCode, data)
	}
	c := decode[model.InstanceDetail](t, data)
	if c.ID == d.ID || c.Name != "实例 a (副本)" || c.Problems["api_key"] != "required" || c.DisplayState != "unconfigured" {
		t.Fatalf("副本 = %+v %v", c.Instance, c.Problems)
	}
	if _, has := c.Config["api_key"]; has || strings.Contains(string(data), "s3cret-key") {
		t.Fatalf("副本不应带密钥: %s", data)
	}
	resp, data = e.do(admin, "POST", "/api/instances/"+c.ID+"/run", nil)
	e.expectError(resp, data, http.StatusBadGateway, "run.failed")
	resp, data = e.do(admin, "POST", "/api/instances/"+d.ID+"/copy?lang=en", nil)
	if en := decode[model.InstanceDetail](t, data); resp.StatusCode != http.StatusCreated || en.Name != "实例 a (copy)" {
		t.Fatalf("英文副本名 = %s", data)
	}
	resp, data = e.do(admin, "POST", "/api/instances/missing/copy", nil)
	e.expectError(resp, data, http.StatusNotFound, "instance.not_found")
	resp, data = e.do(admin, "POST", "/api/instances/missing/pause", nil)
	e.expectError(resp, data, http.StatusNotFound, "instance.not_found")
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
