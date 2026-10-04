package devices

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

type fixture struct {
	s       *Store
	db      *store.DB
	clk     *clock.Fake
	changes []change
}

type change struct {
	id      string
	removed bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, clk: clock.NewFake(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))}
	f.s = New(Config{DB: db, Box: box, Clock: f.clk})
	f.s.OnChange(func(id string, removed bool) { f.changes = append(f.changes, change{id, removed}) })
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// pairNew 走完「生成配对码 → 消耗 → 占位」，返回凭据与设备 ID。
func (f *fixture) pairNew(t *testing.T, opts PairingOptions, pairName, hostname string) (cred, id string) {
	t.Helper()
	ctx := context.Background()
	code, _, err := f.s.NewPairingCode(ctx, opts)
	must(t, err)
	pc, err := f.s.ConsumePairingCode(ctx, HashPairingCode(code))
	must(t, err)
	cred, err = agent.NewCredential()
	must(t, err)
	must(t, f.s.CreatePaired(ctx, PairedDevice{CredentialHash: agent.CredentialHash(cred), Code: pc, PairName: pairName, Hostname: hostname}))
	return cred, agent.DeviceID(cred)
}

func TestDeviceID与凭据一一对应(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cred, id := f.pairNew(t, PairingOptions{Name: "nas1", Role: model.DeviceRoleNAS}, "", "")
	hash, dir, err := f.s.CredentialHash(ctx, id)
	must(t, err)
	if hash != agent.CredentialHash(cred) || dir != model.DeviceDialIn {
		t.Fatalf("hash=%q dir=%q", hash, dir)
	}
	if id != agent.DeviceIDFromHash(hash) || len(id) != 16 {
		t.Fatalf("id=%q", id)
	}
	got, err := f.s.VerifyCredential(ctx, cred)
	must(t, err)
	if got != id {
		t.Fatalf("verify id=%q", got)
	}
	other, _ := agent.NewCredential()
	if _, err := f.s.VerifyCredential(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("陌生凭据应 ErrNotFound: %v", err)
	}
	if _, _, err := f.s.CredentialHash(ctx, "hub"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hub 字面量不应存在: %v", err)
	}
}

func TestVerifyCredential拒绝拨出设备(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cred, _ := agent.NewCredential()
	_, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "vps1", Role: model.DeviceRoleVPS, Address: "wss://1.2.3.4:31418", Pin: "SHA256:x", Credential: cred})
	must(t, err)
	if _, err := f.s.VerifyCredential(ctx, cred); !errors.Is(err, ErrNotFound) {
		t.Fatalf("拨出设备不能用于拨入认证: %v", err)
	}
	if _, dir, err := f.s.CredentialHash(ctx, agent.DeviceID(cred)); err != nil || dir != model.DeviceDialOut {
		t.Fatalf("dir=%q err=%v", dir, err)
	}
}

func TestConsumePairingCode三种失败与一次性(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, exp, err := f.s.NewPairingCode(ctx, PairingOptions{Name: "a"})
	must(t, err)
	if !exp.Equal(f.clk.Now().Add(PairingCodeTTL)) || PairingCodeTTL != 15*time.Minute {
		t.Fatalf("exp=%v", exp)
	}
	// 格式：8 组 × 4 位，以 - 连接。
	parts := strings.Split(code, "-")
	if len(parts) != 8 {
		t.Fatalf("code=%q", code)
	}
	for _, p := range parts {
		if len(p) != 4 {
			t.Fatalf("code=%q", code)
		}
	}
	// 错误码。
	if _, err := f.s.ConsumePairingCode(ctx, HashPairingCode("0000-0000")); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("错误码: %v", err)
	}
	// 小写、无分隔符、易混字符仍可命中；成功消耗一次。
	loose := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	pc, err := f.s.ConsumePairingCode(ctx, HashPairingCode(loose))
	must(t, err)
	if pc.Name != "a" {
		t.Fatalf("pc=%+v", pc)
	}
	// 重复使用。
	if _, err := f.s.ConsumePairingCode(ctx, HashPairingCode(code)); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("重复使用: %v", err)
	}
	// 过期：区分于不存在，且行被删除。
	code2, _, err := f.s.NewPairingCode(ctx, PairingOptions{})
	must(t, err)
	f.clk.Advance(PairingCodeTTL)
	if _, err := f.s.ConsumePairingCode(ctx, HashPairingCode(code2)); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("过期: %v", err)
	}
	if _, err := f.s.ConsumePairingCode(ctx, HashPairingCode(code2)); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("过期码应已删除: %v", err)
	}
}

func TestPairingCode库中只存哈希且限制数量(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var first string
	for i := 0; i < 22; i++ {
		code, _, err := f.s.NewPairingCode(ctx, PairingOptions{})
		must(t, err)
		if i == 0 {
			first = code
		}
		f.clk.Advance(time.Second)
	}
	var n int
	must(t, f.db.QueryRow(`SELECT COUNT(*) FROM pairing_codes`).Scan(&n))
	if n != MaxActivePairingCodes {
		t.Fatalf("n=%d", n)
	}
	if _, err := f.s.ConsumePairingCode(ctx, HashPairingCode(first)); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("最旧的应被淘汰: %v", err)
	}
	var h string
	must(t, f.db.QueryRow(`SELECT code_hash FROM pairing_codes LIMIT 1`).Scan(&h))
	if len(h) != 64 || strings.Contains(h, "-") {
		t.Fatalf("h=%q", h)
	}
}

func TestPairingCode预填与角色默认(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, _, err := f.s.NewPairingCode(ctx, PairingOptions{Name: "dev1", Role: model.DeviceRoleDev})
	must(t, err)
	pc, err := f.s.ConsumePairingCode(ctx, HashPairingCode(code))
	must(t, err)
	if !pc.AllowOffline || !pc.AutoHostMetrics || pc.Role != model.DeviceRoleDev {
		t.Fatalf("dev 默认允许离线、自动创建默认开: %+v", pc)
	}
	no := false
	code, _, _ = f.s.NewPairingCode(ctx, PairingOptions{Role: model.DeviceRoleServer, AutoHostMetrics: &no})
	pc, _ = f.s.ConsumePairingCode(ctx, HashPairingCode(code))
	if pc.AllowOffline || pc.AutoHostMetrics {
		t.Fatalf("pc=%+v", pc)
	}
	if _, _, err := f.s.NewPairingCode(ctx, PairingOptions{Role: "bogus"}); err == nil {
		t.Fatal("非法角色应报错")
	}
}

func TestCreatePaired占位与命名规则(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 配对码名称优先。
	_, id1 := f.pairNew(t, PairingOptions{Name: "nas"}, "agentname", "host1")
	d, err := f.s.Get(ctx, id1)
	must(t, err)
	if d.Name != "nas" || d.Hostname != "host1" || d.Direction != model.DeviceDialIn {
		t.Fatalf("d=%+v", d)
	}
	// 重名加后缀。
	_, id2 := f.pairNew(t, PairingOptions{Name: "nas"}, "", "")
	_, id3 := f.pairNew(t, PairingOptions{Name: "nas"}, "", "")
	d2, _ := f.s.Get(ctx, id2)
	d3, _ := f.s.Get(ctx, id3)
	if d2.Name != "nas (2)" || d3.Name != "nas (3)" {
		t.Fatalf("%q %q", d2.Name, d3.Name)
	}
	// 其次 Pair.Name，再次 Hostname，最后设备 ID。
	_, id4 := f.pairNew(t, PairingOptions{}, "fromagent", "h")
	_, id5 := f.pairNew(t, PairingOptions{}, "", "onlyhost")
	_, id6 := f.pairNew(t, PairingOptions{}, "", "")
	for id, want := range map[string]string{id4: "fromagent", id5: "onlyhost", id6: id6} {
		d, _ := f.s.Get(ctx, id)
		if d.Name != want {
			t.Fatalf("want %q got %q", want, d.Name)
		}
	}
	// 占位：未确认，Presence 不由仓库填写。
	det, err := f.s.Detail(ctx, id1)
	must(t, err)
	if det.Confirmed || !det.AutoHostMetrics {
		t.Fatalf("det=%+v", det)
	}
}

func TestConfirm首次确认与幂等(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, id := f.pairNew(t, PairingOptions{Name: "a"}, "", "")
	f.changes = nil
	old, err := f.s.Confirm(ctx, id)
	must(t, err)
	if old != "" {
		t.Fatalf("old=%q", old)
	}
	det, _ := f.s.Detail(ctx, id)
	if !det.Confirmed {
		t.Fatal("应已确认")
	}
	if len(f.changes) != 1 || f.changes[0] != (change{id, false}) {
		t.Fatalf("changes=%v", f.changes)
	}
	f.changes = nil
	if _, err := f.s.Confirm(ctx, id); err != nil || len(f.changes) != 0 {
		t.Fatalf("重复确认应为空操作: %v %v", err, f.changes)
	}
	if _, err := f.s.Confirm(ctx, "deadbeefdeadbeef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func seedMigration(t *testing.T, f *fixture, old, other string) {
	t.Helper()
	now := store.FormatTime(f.clk.Now())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO plugin_instances (id, plugin_id, name, machine, config_json, config_hash, created_at, updated_at) VALUES ('i1','host-metrics','n1',?, '{}','h',?,?)`, []any{old, now, now}},
		{`INSERT INTO plugin_instances (id, plugin_id, name, machine, config_json, config_hash, created_at, updated_at) VALUES ('i2','host-metrics','n2','hub', '{}','h',?,?)`, []any{now, now}},
		{`INSERT INTO plugin_instances (id, plugin_id, name, machine, config_json, config_hash, created_at, updated_at) VALUES ('i3','host-metrics','n3',?, '{}','h',?,?)`, []any{other, now, now}},
		{`INSERT INTO manifests (machine, plugin_id, version, origin, manifest_json, first_seen_at, updated_at) VALUES (?, 'host-metrics','1','builtin','{}',?,?)`, []any{old, now, now}},
		{`INSERT INTO manifests (machine, plugin_id, version, origin, manifest_json, first_seen_at, updated_at) VALUES (?, 'host-metrics','1','builtin','{}',?,?)`, []any{other, now, now}},
	} {
		if _, err := f.db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func machineOf(t *testing.T, f *fixture, inst string) string {
	t.Helper()
	var m string
	must(t, f.db.QueryRow(`SELECT machine FROM plugin_instances WHERE id=?`, inst).Scan(&m))
	return m
}

func countWhere(t *testing.T, f *fixture, q string, args ...any) int {
	t.Helper()
	var n int
	must(t, f.db.QueryRow(q, args...).Scan(&n))
	return n
}

func TestConfirm重新配对迁移引用(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, oldID := f.pairNew(t, PairingOptions{Name: "nas", Role: model.DeviceRoleNAS}, "", "")
	_, err := f.s.Confirm(ctx, oldID)
	must(t, err)
	_, otherID := f.pairNew(t, PairingOptions{Name: "other"}, "", "")
	seedMigration(t, f, oldID, otherID)

	// 重新配对：新设备占位，旧设备与引用暂不动（P02）。
	_, newID := f.pairNew(t, PairingOptions{Name: "nas", Role: model.DeviceRoleNAS, RepairDeviceID: oldID}, "", "")
	if machineOf(t, f, "i1") != oldID {
		t.Fatal("确认之前不得迁移")
	}
	if _, err := f.s.Get(ctx, oldID); err != nil {
		t.Fatalf("旧设备应保留: %v", err)
	}
	nd, _ := f.s.Get(ctx, newID)
	if nd.Name != "nas (2)" {
		t.Fatalf("占位名称: %q", nd.Name)
	}

	f.changes = nil
	got, err := f.s.Confirm(ctx, newID)
	must(t, err)
	if got != oldID {
		t.Fatalf("应返回旧 ID: %q", got)
	}
	if machineOf(t, f, "i1") != newID || machineOf(t, f, "i2") != "hub" || machineOf(t, f, "i3") != otherID {
		t.Fatal("实例 machine 迁移不正确")
	}
	if countWhere(t, f, `SELECT COUNT(*) FROM manifests WHERE machine=?`, oldID) != 0 ||
		countWhere(t, f, `SELECT COUNT(*) FROM manifests WHERE machine=?`, newID) != 1 ||
		countWhere(t, f, `SELECT COUNT(*) FROM manifests WHERE machine=?`, otherID) != 1 {
		t.Fatal("manifest 迁移不正确")
	}
	if _, err := f.s.Get(ctx, oldID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("旧设备应删除: %v", err)
	}
	nd, _ = f.s.Get(ctx, newID)
	if nd.Name != "nas" {
		t.Fatalf("应回到旧名称: %q", nd.Name)
	}
	det, _ := f.s.Detail(ctx, newID)
	if det.Replaces != "" || !det.Confirmed {
		t.Fatalf("det=%+v", det)
	}
	if len(f.changes) != 2 {
		t.Fatalf("changes=%v", f.changes)
	}
	if ok, err := f.s.TakeAutoHostMetrics(ctx, newID); err != nil || ok {
		t.Fatalf("重新配对后不得再自动创建实例: ok=%v err=%v", ok, err)
	}
}

func TestRepair后拨出占位也清除自动创建标记(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, oldID := f.pairNew(t, PairingOptions{Name: "v", Role: model.DeviceRoleVPS}, "", "")
	_, err := f.s.Confirm(ctx, oldID)
	must(t, err)
	cred, _ := agent.NewCredential()
	d, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "v", Credential: cred, AutoHostMetrics: true, Replaces: oldID})
	must(t, err)
	if _, err := f.s.Confirm(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.s.TakeAutoHostMetrics(ctx, d.ID); ok {
		t.Fatal("拨出重新配对确认后应清除标记")
	}
}

func TestCreate拒绝非法角色与dev默认允许离线(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cred, _ := agent.NewCredential()
	if _, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "x", Role: "bogus", Credential: cred}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("err=%v", err)
	}
	if err := f.s.CreatePaired(ctx, PairedDevice{CredentialHash: agent.CredentialHash(cred), Code: PairingCode{Role: "bogus"}}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("err=%v", err)
	}
	d, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "x", Role: model.DeviceRoleDev, Credential: cred})
	must(t, err)
	if !d.AllowOffline {
		t.Fatal("dev 角色拨出设备应默认允许离线")
	}
}

func TestHashPairingCode易混字符归一(t *testing.T) {
	if HashPairingCode("o1il-0ABC") != HashPairingCode("0111 0abc") {
		t.Fatal("O→0、I/L→1 归一失败")
	}
}

func TestDelete吊销清理关联行(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, id := f.pairNew(t, PairingOptions{Name: "nas"}, "", "")
	_, err := f.s.Confirm(ctx, id)
	must(t, err)
	_, otherID := f.pairNew(t, PairingOptions{Name: "other"}, "", "")
	seedMigration(t, f, id, otherID)
	// 绑定该设备的配对码，及未确认的重新配对占位。
	repairCode, _, err := f.s.NewPairingCode(ctx, PairingOptions{RepairDeviceID: id})
	must(t, err)
	_, _, err = f.s.NewPairingCode(ctx, PairingOptions{RepairDeviceID: otherID})
	must(t, err)
	_, placeholder := f.pairNew(t, PairingOptions{Name: "x", RepairDeviceID: id}, "", "")

	f.changes = nil
	must(t, f.s.Delete(ctx, id))
	if _, err := f.s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if _, err := f.s.Get(ctx, placeholder); !errors.Is(err, ErrNotFound) {
		t.Fatalf("占位应一并删除: %v", err)
	}
	if countWhere(t, f, `SELECT COUNT(*) FROM manifests WHERE machine=?`, id) != 0 ||
		countWhere(t, f, `SELECT COUNT(*) FROM manifests WHERE machine=?`, otherID) != 1 {
		t.Fatal("manifest 清理不正确")
	}
	if _, err := f.s.ConsumePairingCode(ctx, HashPairingCode(repairCode)); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("绑定的配对码应删除: %v", err)
	}
	if countWhere(t, f, `SELECT COUNT(*) FROM pairing_codes WHERE repair_device_id=?`, otherID) != 1 {
		t.Fatal("他人的配对码不得删除")
	}
	if machineOf(t, f, "i1") != id {
		t.Fatal("实例行应保留且 machine 不变")
	}
	removed := map[string]bool{}
	for _, c := range f.changes {
		if !c.removed {
			t.Fatalf("changes=%v", f.changes)
		}
		removed[c.id] = true
	}
	if !removed[id] || !removed[placeholder] {
		t.Fatalf("changes=%v", f.changes)
	}
	if err := f.s.Delete(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复吊销: %v", err)
	}
}

func TestCleanup清理过期码与未确认占位(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, stale := f.pairNew(t, PairingOptions{Name: "stale"}, "", "")
	_, confirmed := f.pairNew(t, PairingOptions{Name: "ok"}, "", "")
	_, err := f.s.Confirm(ctx, confirmed)
	must(t, err)
	_, _, err = f.s.NewPairingCode(ctx, PairingOptions{})
	must(t, err)
	f.clk.Advance(10 * time.Minute)
	_, fresh := f.pairNew(t, PairingOptions{Name: "fresh"}, "", "")
	f.clk.Advance(6 * time.Minute) // stale 满 16 分钟，第一个码已过期，fresh 才 6 分钟

	f.changes = nil
	must(t, f.s.Cleanup(ctx))
	if _, err := f.s.Get(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("过期占位应删除: %v", err)
	}
	for _, id := range []string{confirmed, fresh} {
		if _, err := f.s.Get(ctx, id); err != nil {
			t.Fatalf("%s 不应删除: %v", id, err)
		}
	}
	if countWhere(t, f, `SELECT COUNT(*) FROM pairing_codes`) != 0 {
		t.Fatal("过期码应删除")
	}
	if len(f.changes) != 1 || f.changes[0] != (change{stale, true}) {
		t.Fatalf("changes=%v", f.changes)
	}
}

// 重新配对遇不兼容的拨入占位记录了不兼容原因、等待 agent 升级后接替旧设备，不随 15 分钟清理删除。
func TestCleanup豁免记录了不兼容原因的占位(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, old := f.pairNew(t, PairingOptions{Name: "old"}, "", "")
	_, err := f.s.Confirm(ctx, old)
	must(t, err)
	_, waiting := f.pairNew(t, PairingOptions{Name: "old", RepairDeviceID: old}, "", "")
	must(t, f.s.UpdateHello(ctx, waiting, HelloInfo{
		ProtocolVersion: 1, Incompatible: &model.DeviceIncompatible{Reason: "version_too_old", RequiredMin: 2},
	}))
	_, stale := f.pairNew(t, PairingOptions{Name: "stale", RepairDeviceID: old}, "", "")
	f.clk.Advance(PlaceholderTTL + time.Minute)

	must(t, f.s.Cleanup(ctx))
	if _, err := f.s.Get(ctx, waiting); err != nil {
		t.Fatalf("记录了不兼容原因的占位不应清理: %v", err)
	}
	if _, err := f.s.Get(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未 hello 的占位仍应清理: %v", err)
	}
}

func TestCreateDialOut密文与读取(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cred, _ := agent.NewCredential()
	d, err := f.s.CreateDialOut(ctx, DialOutDevice{
		Name: "vps", Role: model.DeviceRoleVPS, Address: "wss://example.com:31418",
		Pin: "SHA256:abc", ProxyID: "p1", Credential: cred, AutoHostMetrics: true,
	})
	must(t, err)
	if d.ID != agent.DeviceID(cred) || d.Direction != model.DeviceDialOut || d.Address != "wss://example.com:31418" || d.ProxyID != "p1" {
		t.Fatalf("d=%+v", d)
	}
	var enc, hash string
	must(t, f.db.QueryRow(`SELECT credential_enc, credential_hash FROM devices WHERE id=?`, d.ID).Scan(&enc, &hash))
	if enc == "" || strings.Contains(enc, cred) || hash != "" {
		t.Fatalf("enc=%q hash=%q", enc, hash)
	}
	got, err := f.s.Credential(ctx, d.ID)
	must(t, err)
	if got != cred {
		t.Fatal("解密结果不一致")
	}
	det, _ := f.s.Detail(ctx, d.ID)
	if det.Pin != "SHA256:abc" || det.Confirmed || !det.AutoHostMetrics {
		t.Fatalf("det=%+v", det)
	}
	// 拨入设备没有可解密的凭据。
	_, id := f.pairNew(t, PairingOptions{Name: "in"}, "", "")
	if _, err := f.s.Credential(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestUpdate校验与拨出专属字段(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, a := f.pairNew(t, PairingOptions{Name: "a"}, "", "")
	_, b := f.pairNew(t, PairingOptions{Name: "b"}, "", "")
	f.changes = nil
	addr, proxy := "wss://ignored:1", "px"
	d, err := f.s.Update(ctx, a, model.DeviceInput{Name: "a2", Role: model.DeviceRoleNAS, AllowOffline: true, Maintenance: true, Address: &addr, ProxyID: &proxy})
	must(t, err)
	if d.Name != "a2" || d.Role != model.DeviceRoleNAS || !d.AllowOffline || !d.Maintenance {
		t.Fatalf("d=%+v", d)
	}
	if d.Address != "" || d.ProxyID != "" {
		t.Fatalf("拨入设备不得改地址与代理: %+v", d)
	}
	if len(f.changes) != 1 || f.changes[0] != (change{a, false}) {
		t.Fatalf("changes=%v", f.changes)
	}
	_, err = f.s.Update(ctx, a, model.DeviceInput{Name: "b", Role: model.DeviceRoleNAS})
	var fe model.FieldErrors
	if !errors.As(err, &fe) || fe["name"] != model.FieldDuplicate {
		t.Fatalf("err=%v", err)
	}
	_, err = f.s.Update(ctx, a, model.DeviceInput{Name: "  ", Role: "bogus"})
	if !errors.As(err, &fe) || fe["name"] != model.FieldRequired || fe["role"] != model.FieldInvalid {
		t.Fatalf("err=%v", err)
	}
	if _, err := f.s.Update(ctx, "nope", model.DeviceInput{Name: "x", Role: model.DeviceRoleServer}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	// 拨出设备可改地址与代理。
	cred, _ := agent.NewCredential()
	vd, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "v", Role: model.DeviceRoleVPS, Address: "wss://old:1", Pin: "p", Credential: cred})
	must(t, err)
	d, err = f.s.Update(ctx, vd.ID, model.DeviceInput{Name: "v", Role: model.DeviceRoleVPS, Address: &addr, ProxyID: &proxy})
	must(t, err)
	if d.Address != addr || d.ProxyID != proxy {
		t.Fatalf("d=%+v", d)
	}
	// "direct" 与空串都是直连，存库为空串。
	direct := "direct"
	d, err = f.s.Update(ctx, vd.ID, model.DeviceInput{Name: "v", Role: model.DeviceRoleVPS, ProxyID: &direct})
	must(t, err)
	if d.ProxyID != "" {
		t.Fatalf("改回直连后 proxy_id 应为空: %+v", d)
	}
	_ = b
}

func TestUpdateHello写入握手信息(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, id := f.pairNew(t, PairingOptions{Name: "a"}, "", "")
	f.changes = nil
	must(t, f.s.UpdateHello(ctx, id, HelloInfo{
		Address: "192.168.1.5", Hostname: "box", AgentVersion: "0.2.0", OS: "linux", Arch: "arm64", ProtocolVersion: 1,
		Incompatible: &model.DeviceIncompatible{Reason: "version_too_old", RequiredMin: 2},
	}))
	d, _ := f.s.Get(ctx, id)
	if d.Address != "192.168.1.5" || d.Hostname != "box" || d.AgentVersion != "0.2.0" || d.OS != "linux" || d.Arch != "arm64" || d.ProtocolVersion != 1 {
		t.Fatalf("d=%+v", d)
	}
	if d.Incompatible == nil || d.Incompatible.RequiredMin != 2 || d.LastSeenAt == nil || !d.LastSeenAt.Equal(f.clk.Now()) {
		t.Fatalf("d=%+v", d)
	}
	if len(f.changes) != 1 {
		t.Fatalf("changes=%v", f.changes)
	}
	// 兼容后清空不兼容信息。
	must(t, f.s.UpdateHello(ctx, id, HelloInfo{ProtocolVersion: 1}))
	d, _ = f.s.Get(ctx, id)
	if d.Incompatible != nil {
		t.Fatal("不兼容信息应清空")
	}
	f.clk.Advance(time.Minute)
	f.changes = nil
	must(t, f.s.TouchLastSeen(ctx, id, f.clk.Now()))
	d, _ = f.s.Get(ctx, id)
	if !d.LastSeenAt.Equal(f.clk.Now()) || len(f.changes) != 0 {
		t.Fatalf("d=%+v changes=%v", d, f.changes)
	}
	if err := f.s.UpdateHello(ctx, "nope", HelloInfo{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestTakeAutoHostMetrics只取一次(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, id := f.pairNew(t, PairingOptions{Name: "a"}, "", "")
	ok, err := f.s.TakeAutoHostMetrics(ctx, id)
	must(t, err)
	if !ok {
		t.Fatal("首次应为 true")
	}
	ok, err = f.s.TakeAutoHostMetrics(ctx, id)
	must(t, err)
	if ok {
		t.Fatal("第二次应为 false")
	}
}

func TestList按创建时间排序(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, a := f.pairNew(t, PairingOptions{Name: "a"}, "", "")
	f.clk.Advance(time.Second)
	_, b := f.pairNew(t, PairingOptions{Name: "b"}, "", "")
	list, err := f.s.List(ctx)
	must(t, err)
	if len(list) != 2 || list[0].ID != a || list[1].ID != b {
		t.Fatalf("list=%+v", list)
	}
}

// 凭据、哈希与密文不得出现在 model 结构体或其 JSON 中。
func TestModel不含凭据类字段(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cred, _ := agent.NewCredential()
	d, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "v", Role: model.DeviceRoleVPS, Address: "wss://x:1", Pin: "SHA256:pinvalue", Credential: cred})
	must(t, err)
	icred, id := f.pairNew(t, PairingOptions{Name: "in"}, "", "")
	in, _ := f.s.Get(ctx, id)
	list, _ := f.s.List(ctx)
	var dbEnc string
	must(t, f.db.QueryRow(`SELECT credential_enc FROM devices WHERE id=?`, d.ID).Scan(&dbEnc))
	for _, v := range []any{d, in, list} {
		raw, _ := json.Marshal(v)
		s := string(raw)
		for _, bad := range []string{cred, icred, agent.CredentialHash(cred), agent.CredentialHash(icred), "SHA256:pinvalue", dbEnc} {
			if strings.Contains(s, bad) {
				t.Fatalf("JSON 泄露敏感值 %q: %s", bad, s)
			}
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(model.Device{}), reflect.TypeOf(model.DeviceInput{}), reflect.TypeOf(model.PairingResult{}), reflect.TypeOf(model.DialOutRequest{})} {
		for i := 0; i < typ.NumField(); i++ {
			n := strings.ToLower(typ.Field(i).Name)
			// DialOutRequest 与 PairingResult 的 Code 是一次性配对码，不是长期凭据。
			for _, bad := range []string{"credential", "hash", "secret", "ciphertext", "pin", "enc"} {
				exact := n == bad
				prefixed := bad != "pin" && bad != "enc" && strings.HasPrefix(n, bad)
				if exact || prefixed {
					t.Fatalf("%s.%s 不应出现在 model", typ.Name(), typ.Field(i).Name)
				}
			}
		}
	}
}

func TestLog不泄露凭据与配对码(t *testing.T) {
	// 仓库不写日志；此处只确认 Store 没有 logger 依赖，错误文本不含凭据与配对码。
	f := newFixture(t)
	ctx := context.Background()
	code, _, _ := f.s.NewPairingCode(ctx, PairingOptions{})
	_, err := f.s.ConsumePairingCode(ctx, HashPairingCode("AAAA-BBBB"))
	if err == nil || strings.Contains(err.Error(), "AAAA") {
		t.Fatalf("err=%v", err)
	}
	f.clk.Advance(PairingCodeTTL)
	_, err = f.s.ConsumePairingCode(ctx, HashPairingCode(code))
	if err == nil || strings.Contains(err.Error(), strings.ReplaceAll(code, "-", "")[:8]) {
		t.Fatalf("err=%v", err)
	}
}
