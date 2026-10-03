// Package plugintest 是内置插件测试共用的断言工具。
package plugintest

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// templateSizes 是各前端模板支持的尺寸，与 web/src/templates/registry.ts 保持一致。
// quota、quota-multi 尚未实现，不在表内，声明它们会被判为不支持。
var templateSizes = map[string][]string{
	"value":       {"1x1", "2x1", "2x2"},
	"gauge":       {"1x1", "2x1", "2x2"},
	"state":       {"1x1", "2x1"},
	"status-grid": {"2x2", "4x2", "4x3", "6x2"},
	"list":        {"2x1", "2x2", "2x3", "4x2", "4x3"},
	"table":       {"2x2", "4x2", "4x3", "6x1"},
	"chart":       {"2x1", "2x2", "4x2"},
	"clock":       {"1x1", "2x1", "4x2"},
	"weather":     {"2x1", "2x2", "4x2"},
	"text":        {"2x1", "2x2", "4x1", "4x2"},
}

// AssertWidgetSizes 断言 manifest 每个小组件尺寸都在其模板的支持表内。
func AssertWidgetSizes(t testing.TB, m *manifest.Manifest) {
	t.Helper()
	for _, w := range m.Widgets {
		for _, sz := range w.Sizes {
			supported := false
			for _, s := range templateSizes[sz.Template] {
				if s == sz.Size {
					supported = true
					break
				}
			}
			if !supported {
				t.Errorf("小组件 %s 的尺寸 %s 不被模板 %s 支持", w.ID, sz.Size, sz.Template)
			}
		}
	}
}

// AssertValidAgainstManifest 断言报告通过 Validate，且每个键都落在 manifest outputs 声明内
// （动态集合按前缀匹配），类型一致。
func AssertValidAgainstManifest(t testing.TB, m *manifest.Manifest, rep *report.Report) {
	t.Helper()
	if err := rep.Validate(nil); err != nil {
		t.Fatalf("报告应通过 Validate: %v", err)
	}
	for _, it := range rep.Items {
		ok := false
		for _, o := range m.Outputs {
			k, err := report.ParseKey(o.Key)
			if err != nil {
				t.Fatalf("outputs 键不合法: %v", err)
			}
			if k.Matches(it.Key) || (!k.Dynamic && o.Key == it.Key) {
				ok = o.Type == it.Type
				break
			}
		}
		if !ok {
			t.Errorf("键 %s（%s）不在 manifest outputs 内或类型不符", it.Key, it.Type)
		}
	}
}
