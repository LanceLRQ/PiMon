package main

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// agent 带的内置插件都必须能在 agent 运行（runs_on 含 agent），进入 agent 注册表后一个不少；
// 同时能在 hub 运行的插件须在 alsoHub 表里登记，登记的必须同时含 hub。
func TestAgent内置插件均可在agent运行(t *testing.T) {
	want := []string{"docker", "host-metrics", "http-check", "http-json", "ping", "systemd", "tcp-check"}
	alsoHub := map[string]bool{"host-metrics": true, "http-check": true, "http-json": true, "ping": true, "systemd": true, "tcp-check": true}
	var got []string
	for _, s := range runtime.Builtins() {
		m := s.Manifest()
		got = append(got, m.ID)
		if !slices.Contains(m.RunsOn, manifest.RunsOnAgent) {
			t.Errorf("%s 的 runs_on = %v，必须含 agent", m.ID, m.RunsOn)
		}
		if alsoHub[m.ID] && !slices.Contains(m.RunsOn, manifest.RunsOnHub) {
			t.Errorf("%s 的 runs_on = %v，应为 [hub, agent]", m.ID, m.RunsOn)
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("内置插件 = %v，期望 %v", got, want)
	}
	reg := plugins.New(plugins.Config{Dir: t.TempDir(), Builtins: runtime.Builtins()})
	snap := reg.Scan()
	var ids []string
	for _, p := range snap.Plugins {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("注册表插件 = %v", ids)
	}
	ms, err := reg.Manifests()
	if err != nil {
		t.Fatal(err)
	}
	for _, pm := range ms {
		if _, err := manifest.FromJSON(pm.Manifest); err != nil {
			t.Errorf("hub 无法接受上报的 manifest: %v", err)
		}
	}
}
