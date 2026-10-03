package devices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

func TestViewer填入在线状态连接错误与时钟偏差(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, id := f.pairNew(t, PairingOptions{Name: "nas", Role: model.DeviceRoleNAS}, "", "")
	pres := NewPresence(f.clk, f.clk.Now(), []string{id})
	t.Cleanup(pres.Close)
	v := NewViewer(ViewerConfig{
		Store: f.s, Presence: pres,
		ConnError: func(got string) string {
			if got == id {
				return "unreachable"
			}
			return ""
		},
		ClockSkew: func(got string) (time.Duration, bool) { return 1500 * time.Millisecond, got == id },
	})

	d, err := v.View(ctx, id)
	must(t, err)
	if d.Presence != model.PresencePending || d.ConnError != "unreachable" || d.ClockSkewMs == nil || *d.ClockSkewMs != 1500 {
		t.Fatalf("view = presence %q conn %q skew %v", d.Presence, d.ConnError, d.ClockSkewMs)
	}
	pres.Connected(id)
	if d, _ = v.View(ctx, id); d.Presence != model.PresenceOnline {
		t.Fatalf("presence = %q", d.Presence)
	}
	list, err := v.List(ctx)
	must(t, err)
	if len(list) != 1 || list[0].ID != id || list[0].Presence != model.PresenceOnline {
		t.Fatalf("list = %+v", list)
	}
	if _, err := v.View(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的设备 err = %v", err)
	}
}

func TestViewer离线且允许离线显示dormant(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, quiet := f.pairNew(t, PairingOptions{Name: "dev", Role: model.DeviceRoleDev}, "", "")
	_, loud := f.pairNew(t, PairingOptions{Name: "srv", Role: model.DeviceRoleServer}, "", "")
	// 启动宽限期满仍未连上，两台都是 offline。
	pres := NewPresence(f.clk, f.clk.Now(), []string{quiet, loud})
	t.Cleanup(pres.Close)
	waitWaiters(t, f.clk, 1)
	f.clk.Advance(StartupGrace)
	waitState(t, pres, quiet, model.PresenceOffline)
	v := NewViewer(ViewerConfig{Store: f.s, Presence: pres})
	if d, _ := v.View(ctx, quiet); d.Presence != model.PresenceDormant {
		t.Fatalf("允许离线的设备 = %q，期望 dormant", d.Presence)
	}
	if d, _ := v.View(ctx, loud); d.Presence != model.PresenceOffline {
		t.Fatalf("不允许离线的设备 = %q，期望 offline", d.Presence)
	}
	// Presence 里没有记录（占位尚未 hello 且非启动时已知）视为 pending。
	cred, _ := agent.NewCredential()
	d, err := f.s.CreateDialOut(ctx, DialOutDevice{Name: "new", Credential: cred})
	must(t, err)
	if got, _ := v.View(ctx, d.ID); got.Presence != model.PresencePending {
		t.Fatalf("无记录 = %q，期望 pending", got.Presence)
	}
}

func TestConfirm重新配对继承维护模式(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, oldID := f.pairNew(t, PairingOptions{Name: "nas", Role: model.DeviceRoleNAS}, "", "")
	_, err := f.s.Confirm(ctx, oldID)
	must(t, err)
	_, err = f.s.Update(ctx, oldID, model.DeviceInput{Name: "nas", Role: model.DeviceRoleNAS, Maintenance: true})
	must(t, err)
	_, newID := f.pairNew(t, PairingOptions{Name: "nas", Role: model.DeviceRoleNAS, RepairDeviceID: oldID}, "", "")
	if d, _ := f.s.Get(ctx, newID); d.Maintenance {
		t.Fatal("占位期间不应提前继承")
	}
	_, err = f.s.Confirm(ctx, newID)
	must(t, err)
	if d, _ := f.s.Get(ctx, newID); !d.Maintenance {
		t.Fatal("确认后应继承旧设备的维护模式")
	}
}
