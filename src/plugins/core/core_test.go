package core

import (
	"context"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

func TestRegisteredAsHubBuiltin(t *testing.T) {
	src, ok := runtime.Builtin("core")
	if !ok {
		t.Fatal("core 应在 init 中注册")
	}
	m := src.Manifest()
	if m.Runtime != manifest.RuntimeBuiltin || len(m.RunsOn) != 1 || m.RunsOn[0] != manifest.RunsOnHub {
		t.Fatalf("manifest 不符: %+v", m)
	}
	if len(m.Outputs) != 0 {
		t.Fatalf("core 占位不应声明 outputs: %+v", m.Outputs)
	}
}

func TestCollectReturnsEmptyOKReport(t *testing.T) {
	src, _ := runtime.Builtin("core")
	rep, err := src.Collect(context.Background(), runtime.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusOK || len(rep.Items) != 0 {
		t.Fatalf("应为 ok 空报告: %+v", rep)
	}
	if err := rep.Validate(nil); err != nil {
		t.Fatal(err)
	}
}

// sizesOf 返回 manifest 里某小组件声明的「尺寸 → 模板」。
func sizesOf(t *testing.T, m *manifest.Manifest, id string) map[string]string {
	t.Helper()
	for _, w := range m.Widgets {
		if w.ID != id {
			continue
		}
		out := map[string]string{}
		for _, s := range w.Sizes {
			out[s.Size] = s.Template
			if len(s.Bind) != 0 {
				t.Errorf("%s %s 应为空绑定: %+v", id, s.Size, s.Bind)
			}
		}
		return out
	}
	t.Fatalf("manifest 缺少小组件 %s", id)
	return nil
}

func TestManifestDeclaresClockAndText(t *testing.T) {
	src, _ := runtime.Builtin("core")
	m := src.Manifest()
	if len(m.Widgets) != 2 {
		t.Fatalf("core 只声明 clock 与 text 两个小组件: %d", len(m.Widgets))
	}
	clock := sizesOf(t, m, "clock")
	if len(clock) != 3 || clock["1x1"] != "clock" || clock["2x1"] != "clock" || clock["4x2"] != "clock" {
		t.Fatalf("clock 尺寸不符: %+v", clock)
	}
	text := sizesOf(t, m, "text")
	for _, k := range []string{"2x1", "2x2", "4x1", "4x2"} {
		if text[k] != "text" {
			t.Errorf("text 缺少尺寸 %s: %+v", k, text)
		}
	}
	if len(text) != 4 {
		t.Errorf("text 尺寸集应有限: %+v", text)
	}
}
