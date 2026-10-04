package plugins

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

// agentManifest 返回可在 agent 上运行的 manifest。
func agentManifest(t *testing.T, id, version string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte("id: " + id + "\nversion: " + version +
		"\napi_version: 1\nname: " + id + "\nkind: source\nruntime: exec\nruns_on: [agent]\n" +
		"config_schema:\n  - {key: host, type: string, title: Host, pattern: \"^[a-z]+$\"}\n" +
		"outputs:\n  - {key: v, type: number, title: V}\n"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type row struct {
	machine, id, version, origin string
	available                    int
}

func (f *fixture) rows(t *testing.T) []row {
	t.Helper()
	rs, err := f.db.QueryContext(context.Background(),
		`SELECT machine, plugin_id, version, origin, available FROM manifests ORDER BY machine, plugin_id, version`)
	must(t, err)
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var r row
		must(t, rs.Scan(&r.machine, &r.id, &r.version, &r.origin, &r.available))
		out = append(out, r)
	}
	must(t, rs.Err())
	return out
}

func pluginIDs(ps []Plugin) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID+"@"+p.Manifest.Version+"/"+string(p.Origin))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestSyncMachine入库并标记不可用(t *testing.T) {
	f := newFixture(t, builtin(t, "core"))
	f.scan(t)
	ctx := context.Background()
	must(t, f.reg.SyncMachine(ctx, "dev1", []MachineManifest{
		{Origin: OriginBuiltin, Manifest: agentManifest(t, "host-metrics", "1.0.0")},
		{Origin: OriginExec, Manifest: agentManifest(t, "smart", "0.1.0")},
	}))
	ps, err := f.reg.MachinePlugins(ctx, "dev1")
	must(t, err)
	if got := pluginIDs(ps); got != "host-metrics@1.0.0/builtin,smart@0.1.0/exec" {
		t.Fatalf("可用插件 = %s", got)
	}
	// 第二次同步：host-metrics 升级、smart 消失。
	must(t, f.reg.SyncMachine(ctx, "dev1", []MachineManifest{
		{Origin: OriginBuiltin, Manifest: agentManifest(t, "host-metrics", "1.1.0")},
	}))
	ps, err = f.reg.MachinePlugins(ctx, "dev1")
	must(t, err)
	if got := pluginIDs(ps); got != "host-metrics@1.1.0/builtin" {
		t.Fatalf("同步后缓存应失效，可用插件 = %s", got)
	}
	want := []row{
		{"dev1", "host-metrics", "1.0.0", "builtin", 0},
		{"dev1", "host-metrics", "1.1.0", "builtin", 1},
		{"dev1", "smart", "0.1.0", "exec", 0},
		{"hub", "core", "1.0.0", "builtin", 1},
	}
	got := f.rows(t)
	if len(got) != len(want) {
		t.Fatalf("manifests 行 = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 行 = %+v，期望 %+v", i, got[i], want[i])
		}
	}
	p, ok := f.reg.MachinePlugin("dev1", "host-metrics")
	if !ok || p.Manifest.Version != "1.1.0" || p.Source != nil {
		t.Fatalf("MachinePlugin = %+v %v", p, ok)
	}
	if _, ok := f.reg.MachinePlugin("dev1", "smart"); ok {
		t.Fatal("不可用的插件不应返回")
	}
	// 从库中读回的 manifest 保留 config_schema。
	if fields := p.Manifest.ConfigSchema; len(fields) != 1 || fields[0].Pattern == "" {
		t.Fatalf("config_schema = %+v", fields)
	}
}

func TestSyncMachine空集合把该机器全部标为不可用(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	must(t, f.reg.SyncMachine(ctx, "dev1", []MachineManifest{{Origin: OriginExec, Manifest: agentManifest(t, "smart", "0.1.0")}}))
	must(t, f.reg.SyncMachine(ctx, "dev2", []MachineManifest{{Origin: OriginExec, Manifest: agentManifest(t, "smart", "0.1.0")}}))
	must(t, f.reg.SyncMachine(ctx, "dev1", nil))
	ps, err := f.reg.MachinePlugins(ctx, "dev1")
	must(t, err)
	if len(ps) != 0 {
		t.Fatalf("dev1 不应有可用插件: %s", pluginIDs(ps))
	}
	ps, err = f.reg.MachinePlugins(ctx, "dev2")
	must(t, err)
	if pluginIDs(ps) != "smart@0.1.0/exec" {
		t.Fatalf("dev2 不受影响: %s", pluginIDs(ps))
	}
}

func TestSyncMachine拒绝hub与非法来源(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reg.SyncMachine(ctx, MachineHub, nil); err == nil {
		t.Fatal("不允许用 SyncMachine 写 hub")
	}
	if err := f.reg.SyncMachine(ctx, "dev1", []MachineManifest{{Origin: "http", Manifest: agentManifest(t, "x", "1.0.0")}}); err == nil {
		t.Fatal("非法来源应报错")
	}
	if err := f.reg.SyncMachine(ctx, "dev1", []MachineManifest{
		{Origin: OriginExec, Manifest: agentManifest(t, "x", "1.0.0")},
		{Origin: OriginExec, Manifest: agentManifest(t, "x", "1.1.0")},
	}); err == nil {
		t.Fatal("同一插件出现两次应报错")
	}
}

func TestMachinePlugins缓存与失效(t *testing.T) {
	f := newFixture(t, builtin(t, "core"))
	f.scan(t)
	ctx := context.Background()
	must(t, f.reg.SyncMachine(ctx, "dev1", []MachineManifest{{Origin: OriginExec, Manifest: agentManifest(t, "smart", "0.1.0")}}))
	if _, err := f.reg.MachinePlugins(ctx, "dev1"); err != nil {
		t.Fatal(err)
	}
	// 绕过注册表删除行（如吊销事务），缓存仍在，直到 DropMachine。
	_, err := f.db.ExecContext(ctx, `DELETE FROM manifests WHERE machine = 'dev1'`)
	must(t, err)
	if _, ok := f.reg.MachinePlugin("dev1", "smart"); !ok {
		t.Fatal("失效前应命中缓存")
	}
	// 模拟与吊销并发的写入：DropMachine 应删掉残留行并丢弃缓存。
	must(t, f.reg.SyncMachine(ctx, "dev1", []MachineManifest{{Origin: OriginExec, Manifest: agentManifest(t, "smart", "0.1.0")}}))
	must(t, f.reg.DropMachine(ctx, "dev1"))
	if _, ok := f.reg.MachinePlugin("dev1", "smart"); ok {
		t.Fatal("DropMachine 后不应再有可用插件")
	}
	for _, r := range f.rows(t) {
		if r.machine == "dev1" {
			t.Fatalf("dev1 的 manifest 行应被删除: %+v", r)
		}
	}
	if err := f.reg.DropMachine(ctx, MachineHub); err == nil {
		t.Fatal("不允许删除 hub")
	}
	// hub 走内存快照。
	ps, err := f.reg.MachinePlugins(ctx, MachineHub)
	must(t, err)
	if len(ps) != 1 || ps[0].ID != "core" {
		t.Fatalf("hub 插件 = %+v", ps)
	}
	if p, ok := f.reg.MachinePlugin(MachineHub, "core"); !ok || p.Source == nil {
		t.Fatal("hub 插件应带 Source")
	}
}
