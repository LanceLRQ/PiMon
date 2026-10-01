package schema

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// decode 把 YAML 序列解析成字段定义，要求无定义问题。
func decode(t *testing.T, src string) []Field {
	t.Helper()
	fields, issues := decodeSrc(t, src)
	if len(issues) != 0 {
		t.Fatalf("字段定义不应有问题: %+v", issues)
	}
	return fields
}

func decodeSrc(t *testing.T, src string) ([]Field, []Issue) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return DecodeFields(doc.Content[0], "config_schema")
}

// lineOf 返回 src 中首个包含 needle 的行号（从 1 起）。
func lineOf(t *testing.T, src, needle string) int {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	t.Fatalf("未找到 %q", needle)
	return 0
}
