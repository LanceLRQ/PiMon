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
