package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

const testManifest = `id: %ID%
version: 1.2.3
api_version: 1
name: {zh: 中文名, en: English Name}
kind: source
runtime: %RT%
runs_on: [hub]
interval: 60s
timeout: 10s
config_schema:
  - {key: name, type: string, title: {zh: 名称, en: Name}, required: true}
  - {key: city, type: lookup, title: {zh: 城市, en: City}, help: {zh: 搜索城市, en: Search city}}
  - {key: mode, type: enum, title: Mode, options: [{value: a, title: {zh: 甲, en: A}}]}
outputs:
  - {key: temp, type: number, title: {zh: 温度, en: Temperature}}
widgets:
  - id: w
    name: {zh: 小组件, en: Widget}
    sizes:
      1x1: {template: value, bind: {value: {item: temp}}}
`

func testManifestFor(t *testing.T, id, rt string) *manifest.Manifest {
	t.Helper()
	y := strings.NewReplacer("%ID%", id, "%RT%", rt).Replace(testManifest)
	m, err := manifest.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// plainSource 是不实现 Lookuper 的内置插件替身。
type plainSource struct{ m *manifest.Manifest }

func (s plainSource) Manifest() *manifest.Manifest { return s.m }
func (plainSource) Collect(context.Context, runtime.Input) (*report.Report, error) {
	return &report.Report{}, nil
}

// lookupSource 额外实现 Lookuper；Query 为 "boom" 时返回错误。
type lookupSource struct{ plainSource }

func (lookupSource) Lookup(_ context.Context, key, query, lang string) ([]runtime.Candidate, error) {
	if query == "boom" {
		return nil, errors.New("upstream down")
	}
	return []runtime.Candidate{{Value: key + ":" + query, Label: lang + "/" + query}}, nil
}

func testBuiltins(t *testing.T) []runtime.Source {
	return []runtime.Source{
		plainSource{testManifestFor(t, "plain", "builtin")},
		lookupSource{plainSource{testManifestFor(t, "looker", "builtin")}},
	}
}

func getPlugins(t *testing.T, e *env, c *http.Client, method, path string) model.PluginList {
	t.Helper()
	resp, data := e.do(c, method, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s = %d %s", method, path, resp.StatusCode, data)
	}
	var l model.PluginList
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("响应不是 PluginList: %s", data)
	}
	return l
}

func TestPluginsRequireAdmin(t *testing.T) {
	e := newEnv(t)
	for _, tc := range [][2]string{{"GET", "/api/plugins"}, {"POST", "/api/plugins/rescan"}, {"POST", "/api/plugins/looker/lookup/city"}} {
		resp, data := e.do(e.client, tc[0], tc[1], nil)
		e.expectError(resp, data, http.StatusUnauthorized, "auth.required")
	}
}

func TestListPluginsResolvesLanguage(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	zh := getPlugins(t, e, admin, "GET", "/api/plugins?lang=zh")
	if len(zh.Plugins) != 2 || zh.Plugins[0].ID != "looker" || zh.Plugins[1].ID != "plain" {
		t.Fatalf("插件 = %+v", zh.Plugins)
	}
	p := zh.Plugins[0]
	if p.Name != "中文名" || p.Version != "1.2.3" || p.Origin != "builtin" || p.Runtime != "builtin" ||
		p.IntervalSeconds != 60 || p.TimeoutSeconds != 10 || len(p.RunsOn) != 1 {
		t.Fatalf("基本信息不对: %+v", p)
	}
	if len(p.ConfigSchema) != 3 || p.ConfigSchema[1].Type != "lookup" || p.ConfigSchema[1].Title != "城市" ||
		p.ConfigSchema[1].Help != "搜索城市" || !p.ConfigSchema[0].Required ||
		len(p.ConfigSchema[2].Options) != 1 || p.ConfigSchema[2].Options[0].Title != "甲" {
		t.Fatalf("config_schema 不对: %+v", p.ConfigSchema)
	}
	if len(p.Outputs) != 1 || p.Outputs[0].Key != "temp" || p.Outputs[0].Title != "温度" {
		t.Fatalf("outputs 不对: %+v", p.Outputs)
	}
	if len(p.Widgets) != 1 || p.Widgets[0].Name != "小组件" || len(p.Widgets[0].Sizes) != 1 {
		t.Fatalf("widgets 不对: %+v", p.Widgets)
	}
	sz := p.Widgets[0].Sizes[0]
	if sz.Size != "1x1" || sz.Cols != 1 || sz.Rows != 1 || sz.Template != "value" ||
		len(sz.Bind) != 1 || sz.Bind[0].Slot != "value" || len(sz.Bind[0].Refs) != 1 || sz.Bind[0].Refs[0].Item != "temp" {
		t.Fatalf("尺寸不对: %+v", sz)
	}
	if zh.Errors == nil || zh.Conflicts == nil {
		t.Fatal("errors 与 conflicts 应为 [] 而非 null")
	}

	en := getPlugins(t, e, admin, "GET", "/api/plugins?lang=en")
	if en.Plugins[0].Name != "English Name" || en.Plugins[0].ConfigSchema[1].Title != "City" {
		t.Fatalf("英文解析不对: %+v", en.Plugins[0])
	}
	// 缺省用全局设置的语言（测试环境为 zh）
	def := getPlugins(t, e, admin, "GET", "/api/plugins")
	if def.Plugins[0].Name != "中文名" {
		t.Fatalf("缺省语言不对: %s", def.Plugins[0].Name)
	}
}

func TestRescanPicksUpExecAndReportsIssues(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	good := filepath.Join(e.pluginDir, "good")
	bad := filepath.Join(e.pluginDir, "bad")
	conflict := filepath.Join(e.pluginDir, "plain")
	for id, d := range map[string]string{"good": good, "bad": bad, "plain": conflict} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(d, 0o755)
		y := strings.NewReplacer("%ID%", id, "%RT%", "exec").Replace(testManifest)
		if id == "bad" {
			y = strings.Replace(y, "kind: source", "kind: zzz", 1)
		}
		if err := os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(filepath.Join(d, "run"), 0o755)
	}
	l := getPlugins(t, e, admin, "POST", "/api/plugins/rescan?lang=en")
	var got []string
	for _, p := range l.Plugins {
		got = append(got, p.ID+"/"+p.Origin)
	}
	if strings.Join(got, ",") != "good/exec,looker/builtin,plain/builtin" {
		t.Fatalf("插件 = %v", got)
	}
	if len(l.Errors) != 1 || l.Errors[0].Dir != "bad" || l.Errors[0].Kind != "invalid_manifest" ||
		len(l.Errors[0].Problems) == 0 || l.Errors[0].Problems[0].Line == 0 {
		t.Fatalf("errors = %+v", l.Errors)
	}
	if len(l.Conflicts) != 1 || l.Conflicts[0].Dir != "plain" || l.Conflicts[0].Kind != "conflict" {
		t.Fatalf("conflicts = %+v", l.Conflicts)
	}
	// 之后的 GET 反映同一份快照
	if again := getPlugins(t, e, admin, "GET", "/api/plugins"); len(again.Plugins) != 3 {
		t.Fatalf("GET 应反映重扫结果: %d", len(again.Plugins))
	}
}

func TestLookup(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()

	resp, data := e.do(admin, "POST", "/api/plugins/looker/lookup/city?lang=en", model.PluginLookupRequest{Query: "bei"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup = %d %s", resp.StatusCode, data)
	}
	var out model.PluginLookupResponse
	if err := json.Unmarshal(data, &out); err != nil || len(out.Candidates) != 1 ||
		out.Candidates[0].Value != "city:bei" || out.Candidates[0].Label != "en/bei" {
		t.Fatalf("候选 = %s", data)
	}
	// 缺省语言取全局设置
	_, data = e.do(admin, "POST", "/api/plugins/looker/lookup/city", model.PluginLookupRequest{Query: "x"})
	if !strings.Contains(string(data), `"zh/x"`) {
		t.Fatalf("缺省语言应为 zh: %s", data)
	}

	resp, data = e.do(admin, "POST", "/api/plugins/nope/lookup/city", model.PluginLookupRequest{Query: "x"})
	e.expectError(resp, data, http.StatusNotFound, "plugin.not_found")

	for name, path := range map[string]string{
		"字段不存在":          "/api/plugins/looker/lookup/missing",
		"字段不是 lookup 类型": "/api/plugins/looker/lookup/name",
		"插件未实现 Lookuper": "/api/plugins/plain/lookup/city",
	} {
		resp, data = e.do(admin, "POST", path, model.PluginLookupRequest{Query: "x"})
		er := e.expectError(resp, data, http.StatusBadRequest, "validation.failed")
		fields, _ := er.Error.Details["fields"].(map[string]any)
		key := path[strings.LastIndex(path, "/")+1:]
		if _, ok := fields[key]; !ok {
			t.Fatalf("%s: details.fields 应指向 %s: %v", name, key, er.Error.Details)
		}
	}

	resp, data = e.do(admin, "POST", "/api/plugins/looker/lookup/city", model.PluginLookupRequest{Query: "boom"})
	e.expectError(resp, data, http.StatusInternalServerError, "internal")

	resp, data = e.do(admin, "POST", "/api/plugins/looker/lookup/city", map[string]any{"query": 1})
	e.expectError(resp, data, http.StatusBadRequest, "request.invalid_json")
}
