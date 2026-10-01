package plugindev

import (
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

var updateSchema = flag.Bool("update-schema", false, "重新生成 docs/plugin-schema/ 下的 JSON Schema 产物")

const schemaDir = "../../../../docs/plugin-schema"

// 产物与当前 Go 结构生成的结果必须一致；过期时运行：
//
//	cd src && go test ./internal/hub/plugindev -run TestSchemaFiles -update-schema
func TestSchemaFiles(t *testing.T) {
	gens := map[string]func() ([]byte, error){
		ManifestSchemaFile: ManifestSchema,
		ReportSchemaFile:   ReportSchema,
	}
	for name, gen := range gens {
		want, err := gen()
		if err != nil {
			t.Fatalf("%s: 生成失败: %v", name, err)
		}
		path := filepath.Join(schemaDir, name)
		if *updateSchema {
			if err := os.MkdirAll(schemaDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s: %v\n请运行: cd src && go test ./internal/hub/plugindev -run TestSchemaFiles -update-schema", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s 已过期，请运行: cd src && go test ./internal/hub/plugindev -run TestSchemaFiles -update-schema", path)
		}
	}
}

// fullManifest 在每一层都用上了全部属性，供字段集合比对。
const fullManifest = `
id: full-sample
version: 1.0.0
api_version: 1
name: {zh: 全量样例, en: Full sample}
kind: source
runtime: exec
runs_on: [hub, agent]
interval: 30s
min_interval: 10s
timeout: 20s
config_schema:
  - {key: name, type: string, title: {zh: 名称, en: Name}, help: 帮助, required: true, default: x, min: 1, max: 10, pattern: "^x"}
  - key: mode
    type: enum
    options: [a, {value: b, title: {zh: 乙, en: B}}]
    default: a
  - key: extra
    type: string
    visible_when: {mode: [a, b]}
  - {key: target, type: url, allow_query: true, allow_public_http: true, follow_redirects: true}
  - {key: headers, type: kv, secret_values: true}
  - key: targets
    type: object_list
    fields:
      - {key: url, type: url}
outputs:
  - {key: cpu, type: gauge, title: {zh: CPU, en: CPU}}
  - {key: "disk[*]", type: quota}
widgets:
  - id: w1
    name: {zh: 组件, en: Widget}
    sizes:
      1x1: {template: gauge, bind: {value: {item: cpu, field: value}}}
      2x2: {template: list, bind: {items: [{item: "disk[*]"}, {item: cpu}]}}
alerts:
  - {name: {zh: 高, en: High}, item: cpu, field: value, op: ">", value: 90, severity: warning, for: 5m}
`

func schemaProps(t *testing.T, def map[string]any) map[string]bool {
	t.Helper()
	props, _ := def["properties"].(map[string]any)
	out := make(map[string]bool, len(props))
	for k := range props {
		out[k] = true
	}
	return out
}

func keysOf(ms ...map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		for k := range m {
			out[k] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func asMaps(v any) []map[string]any {
	var out []map[string]any
	for _, it := range v.([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func loadSchema(t *testing.T) (root map[string]any, defs map[string]any) {
	t.Helper()
	data, err := ManifestSchema()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	defs, _ = root["$defs"].(map[string]any)
	return root, defs
}

// 样例 manifest 每一层使用的键集合必须与 Schema 对应层的属性集合完全一致，
// 且样例能被 manifest.Parse 接受（即每个 Schema 属性都是解析器认可的）。
func TestManifestSchemaMatchesSample(t *testing.T) {
	if _, err := manifest.Parse([]byte(fullManifest)); err != nil {
		t.Fatalf("样例 manifest 应通过解析: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(fullManifest), &doc); err != nil {
		t.Fatal(err)
	}
	root, defs := loadSchema(t)
	def := func(name string) map[string]any {
		d, ok := defs[name].(map[string]any)
		if !ok {
			t.Fatalf("Schema 缺少定义 %s", name)
		}
		return d
	}
	check := func(level string, used map[string]bool, schemaDef map[string]any) {
		t.Helper()
		if want := schemaProps(t, schemaDef); !reflect.DeepEqual(used, want) {
			t.Errorf("%s 层字段不一致\n样例/解析器: %v\nSchema:      %v", level, sortedKeys(used), sortedKeys(want))
		}
	}
	check("根", keysOf(doc), root)
	check("outputs", keysOf(asMaps(doc["outputs"])...), def("outputDoc"))
	check("alerts", keysOf(asMaps(doc["alerts"])...), def("alertDoc"))
	widgets := asMaps(doc["widgets"])
	check("widgets", keysOf(widgets...), def("widgetDoc"))
	var sizes, refs []map[string]any
	for _, w := range widgets {
		for _, s := range w["sizes"].(map[string]any) {
			sm := s.(map[string]any)
			sizes = append(sizes, sm)
			for _, b := range sm["bind"].(map[string]any) {
				if l, ok := b.([]any); ok {
					for _, x := range l {
						refs = append(refs, x.(map[string]any))
					}
				} else {
					refs = append(refs, b.(map[string]any))
				}
			}
		}
	}
	check("sizes", keysOf(sizes...), def("sizeDoc"))
	check("bind 引用", keysOf(refs...), def("refDoc"))
	fields := asMaps(doc["config_schema"])
	for _, f := range append([]map[string]any(nil), fields...) {
		if sub, ok := f["fields"]; ok {
			fields = append(fields, asMaps(sub)...)
		}
	}
	check("config_schema 字段", keysOf(fields...), def("fieldDoc"))
}

// 解析器 switch 里出现的全部键名（manifest 与 config_schema 两处解析器）必须都在 Schema 中，
// 防止解析器新增字段而 Schema 没跟上。
func TestManifestSchemaCoversParserKeys(t *testing.T) {
	files := []string{
		"../../../pkg/plugin/manifest/parse.go",
		"../../../pkg/plugin/manifest/sections.go",
		"../../../pkg/plugin/schema/decode.go",
	}
	parserKeys := map[string]bool{}
	for _, f := range files {
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			sel, ok := sw.Tag.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Key" {
				return true
			}
			for _, c := range sw.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, _ := strconv.Unquote(lit.Value)
						parserKeys[s] = true
					}
				}
			}
			return true
		})
	}
	if len(parserKeys) < 20 {
		t.Fatalf("从解析器提取到的键过少（%d），提取逻辑可能失效", len(parserKeys))
	}
	root, defs := loadSchema(t)
	schemaKeys := schemaProps(t, root)
	for _, d := range defs {
		for k := range schemaProps(t, d.(map[string]any)) {
			schemaKeys[k] = true
		}
	}
	for k := range parserKeys {
		if !schemaKeys[k] {
			t.Errorf("解析器接受键 %q，但 Schema 中没有", k)
		}
	}
	for k := range schemaKeys {
		if !parserKeys[k] {
			t.Errorf("Schema 有属性 %q，但解析器不认识", k)
		}
	}
}

func TestSchemaEnumsFollowCode(t *testing.T) {
	root, defs := loadSchema(t)
	tpl := defs["sizeDoc"].(map[string]any)["properties"].(map[string]any)["template"].(map[string]any)["enum"].([]any)
	if len(tpl) != len(manifest.Templates) {
		t.Errorf("模板枚举数量不一致: %d vs %d", len(tpl), len(manifest.Templates))
	}
	pat := ""
	for p := range defs["widgetDoc"].(map[string]any)["properties"].(map[string]any)["sizes"].(map[string]any)["patternProperties"].(map[string]any) {
		pat = p
	}
	if pat != "^[1-6]x[1-4]$" {
		t.Errorf("尺寸正则 = %q，应反映 6x4 上限", pat)
	}
	// id 正则与解析器行为一致。
	for id, ok := range map[string]bool{"ok-id": true, "Bad_ID": false, "a--b": false} {
		src := "id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: n\nkind: source\nruntime: exec\nruns_on: [hub]\n"
		_, err := manifest.Parse([]byte(src))
		if (err == nil) != ok {
			t.Errorf("解析器对 id %q 的判断与预期不符: %v", id, err)
		}
	}
	if p, _ := root["properties"].(map[string]any)["id"].(map[string]any)["pattern"].(string); !strings.Contains(p, `[a-z0-9]+(-[a-z0-9]+)*`) {
		t.Error("id 正则缺失")
	}
}

// alert 的 op、severity 枚举与解析器实际接受的集合一致：候选值里只有枚举内的被接受。
func TestAlertEnumsMatchParser(t *testing.T) {
	parse := func(op, sev string) error {
		src := "id: a-t\nversion: 1.0.0\napi_version: 1\nname: n\nkind: source\nruntime: exec\nruns_on: [hub]\n" +
			"outputs:\n  - {key: v, type: gauge}\nalerts:\n  - {name: n, item: v, op: \"" + op + "\", severity: " + sev + "}\n"
		_, err := manifest.Parse([]byte(src))
		return err
	}
	in := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, op := range []string{"<", "<=", ">", ">=", "==", "!=", "=", "<>", "=>", "~", "=<"} {
		if ok := parse(op, "info") == nil; ok != in(alertOps, op) {
			t.Errorf("op %q: 解析器接受=%v，Schema 枚举包含=%v", op, ok, in(alertOps, op))
		}
	}
	for _, sev := range []string{"info", "warning", "critical", "error", "fatal", "debug"} {
		if ok := parse(">", sev) == nil; ok != in(alertSeverities, sev) {
			t.Errorf("severity %q: 解析器接受=%v，Schema 枚举包含=%v", sev, ok, in(alertSeverities, sev))
		}
	}
}
