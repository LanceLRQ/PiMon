package manifest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

const patternExample = `id: host-check
version: 1.2.3
api_version: 1
name: Host check
kind: source
runtime: exec
runs_on: [hub, agent]
interval: 60s
min_interval: 10s
timeout: 5s
config_schema:
  - {key: host, type: string, title: Host, pattern: "^[a-z0-9.-]+$"}
  - key: rows
    type: object_list
    title: Rows
    fields:
      - {key: name, type: string, title: Name, pattern: "^[a-z]+$"}
outputs:
  - {key: up, type: state, title: Up}
`

// manifestJSON 把 YAML manifest 解析后序列化成 JSON（agent 上报的形式），mutate 可在序列化前修改字段。
func manifestJSON(t *testing.T, src string, mutate func(m map[string]any)) []byte {
	t.Helper()
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if mutate == nil {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	mutate(obj)
	raw, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFromJSON往返并预编译正则(t *testing.T) {
	m, err := FromJSON(manifestJSON(t, patternExample, nil))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if m.ID != "host-check" || m.Version != "1.2.3" || m.Runtime != RuntimeExec || m.Interval != 60*time.Second ||
		m.MinInterval != 10*time.Second || m.Timeout != 5*time.Second || len(m.Outputs) != 1 {
		t.Fatalf("字段不符: %+v", m)
	}
	// 预编译后的正则生效：不合法的值被拒绝。
	errs := schema.Validate(m.ConfigSchema, map[string]any{"host": "BAD HOST", "rows": []any{map[string]any{"name": "X1"}}})
	if errs["host"] == "" || errs["rows[0].name"] == "" {
		t.Fatalf("pattern 应生效，得到 %v", errs)
	}
}

func TestFromJSON拒绝不合法的manifest(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"id 非法", func(m map[string]any) { m["ID"] = "Bad_ID" }, "id"},
		{"version 非法", func(m map[string]any) { m["Version"] = "1.0" }, "version"},
		{"api_version 不支持", func(m map[string]any) { m["APIVersion"] = 2 }, "api_version"},
		{"kind 非法", func(m map[string]any) { m["Kind"] = "sink" }, "kind"},
		{"runtime 非法", func(m map[string]any) { m["Runtime"] = "wasm" }, "runtime"},
		{"runs_on 不含 agent", func(m map[string]any) { m["RunsOn"] = []string{"hub"} }, "runs_on"},
		{"runs_on 取值非法", func(m map[string]any) { m["RunsOn"] = []string{"agent", "cloud"} }, "runs_on"},
		{"interval 低于下限", func(m map[string]any) { m["Interval"] = int64(time.Second) }, "interval"},
		{"timeout 非正", func(m map[string]any) { m["Timeout"] = 0 }, "timeout"},
		{"min_interval 大于 interval", func(m map[string]any) { m["MinInterval"] = int64(2 * time.Minute) }, "min_interval"},
		{"pattern 非法", func(m map[string]any) {
			m["ConfigSchema"].([]any)[0].(map[string]any)["Pattern"] = "("
		}, "pattern"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := FromJSON(manifestJSON(t, patternExample, c.mutate))
			if err == nil {
				t.Fatal("应返回错误")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误应提到 %q，得到 %v", c.want, err)
			}
		})
	}
}

func TestFromJSON拒绝非JSON(t *testing.T) {
	for _, raw := range []string{``, `null`, `[1]`, `{"ID":`} {
		if _, err := FromJSON([]byte(raw)); err == nil {
			t.Fatalf("%q 应返回错误", raw)
		}
	}
}
