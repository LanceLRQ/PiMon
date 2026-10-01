package runtime

import (
	"context"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

type stubSource struct{ m *manifest.Manifest }

func (s stubSource) Manifest() *manifest.Manifest { return s.m }
func (stubSource) Collect(context.Context, Input) (*report.Report, error) {
	return &report.Report{}, nil
}

func resetRegistry(t *testing.T) {
	t.Helper()
	regMu.Lock()
	saved := registered
	registered = map[string]Source{}
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		registered = saved
		regMu.Unlock()
	})
}

func TestRegisterAndList(t *testing.T) {
	resetRegistry(t)
	Register(stubSource{&manifest.Manifest{ID: "b"}})
	Register(stubSource{&manifest.Manifest{ID: "a"}})
	list := Builtins()
	if len(list) != 2 || list[0].Manifest().ID != "a" || list[1].Manifest().ID != "b" {
		t.Fatalf("列表应按 id 升序: %v", list)
	}
	if s, ok := Builtin("b"); !ok || s.Manifest().ID != "b" {
		t.Fatal("按 id 取不到")
	}
	if _, ok := Builtin("zzz"); ok {
		t.Fatal("不存在的 id 不应取到")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetRegistry(t)
	Register(stubSource{&manifest.Manifest{ID: "a"}})
	defer func() {
		if recover() == nil {
			t.Fatal("重复 id 应 panic")
		}
	}()
	Register(stubSource{&manifest.Manifest{ID: "a"}})
}
