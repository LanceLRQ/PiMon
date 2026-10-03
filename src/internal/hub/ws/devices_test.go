package ws

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

type fakeDevices struct {
	mu   sync.Mutex
	byID map[string]model.Device
	// gate 非 nil 时 View 先等它关闭，用来把推送协程卡在读取设备上。
	gate    chan struct{}
	entered chan struct{}
}

func newFakeDevices(list ...model.Device) *fakeDevices {
	f := &fakeDevices{byID: map[string]model.Device{}}
	for _, d := range list {
		f.byID[d.ID] = d
	}
	return f
}

func (f *fakeDevices) set(d model.Device) {
	f.mu.Lock()
	f.byID[d.ID] = d
	f.mu.Unlock()
}

func (f *fakeDevices) remove(id string) {
	f.mu.Lock()
	delete(f.byID, id)
	f.mu.Unlock()
}

func (f *fakeDevices) List(context.Context) ([]model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.Device{}
	for _, d := range f.byID {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeDevices) View(_ context.Context, id string) (model.Device, error) {
	f.mu.Lock()
	gate, entered := f.gate, f.entered
	f.mu.Unlock()
	if gate != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.byID[id]
	if !ok {
		return model.Device{}, devices.ErrNotFound
	}
	return d, nil
}

func dev(id, name, presence string) model.Device {
	return model.Device{ID: id, Name: name, Presence: presence}
}

func newDeviceHarness(t *testing.T, queue int, list ...model.Device) (*harness, *fakeDevices) {
	t.Helper()
	fd := newFakeDevices(list...)
	h := newHarnessFull(t, queue, nil, func(c *Config) { c.Devices = fd })
	return h, fd
}

func TestDevices订阅后收到snapshot且合并推送device补丁(t *testing.T) {
	h, fd := newDeviceHarness(t, 0, dev("d1", "nas", model.PresenceOnline))
	c := h.dial(adminToken)
	first := read(t, c)
	if _, ok := first["devices"]; ok {
		t.Fatalf("默认订阅不含 devices，snapshot 不应带设备: %v", first["devices"])
	}

	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicInstances, ui.TopicDevices}})
	snap := read(t, c)
	list, ok := snap["devices"].([]any)
	if snap["type"] != string(ui.TypeSnapshot) || !ok || len(list) != 1 || list[0].(map[string]any)["id"] != "d1" {
		t.Fatalf("订阅 devices 后的 snapshot 应带设备列表: %v", snap)
	}

	for _, p := range []string{model.PresenceDisconnected, model.PresenceOffline} {
		fd.set(dev("d1", "nas", p))
		h.hub.NotifyDevice("d1")
	}
	ping(t, c) // 窗口未到：没有 patch 先于 pong

	h.clk.Advance(time.Second)
	p := read(t, c)
	if p["type"] != string(ui.TypePatch) || p["entity"] != ui.EntityDevice {
		t.Fatalf("应收到 device patch: %v", p)
	}
	if got := p["device"].(map[string]any)["presence"]; got != model.PresenceOffline {
		t.Fatalf("patch 应带合并后的最新状态，得到 %v", got)
	}
	ping(t, c) // 合并成一个

	// 状态没有实际变化时不推。
	h.hub.NotifyDevice("d1")
	h.clk.Advance(time.Second)
	ping(t, c)
}

func TestDevices吊销推送device_removed且新设备推送device(t *testing.T) {
	h, fd := newDeviceHarness(t, 0, dev("d1", "nas", model.PresenceOnline))
	c := h.dial(adminToken)
	read(t, c)
	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicDevices}})
	read(t, c)

	fd.remove("d1")
	h.hub.NotifyDevice("d1")
	fd.set(dev("d2", "vps", model.PresencePending))
	h.hub.NotifyDevice("d2")
	h.clk.Advance(time.Second)

	got := map[string]map[string]any{}
	for i := 0; i < 2; i++ {
		p := read(t, c)
		got[p["entity"].(string)] = p
	}
	if got[ui.EntityDeviceRemoved]["id"] != "d1" {
		t.Fatalf("应收到 d1 的 device_removed: %v", got)
	}
	if got[ui.EntityDevice]["device"].(map[string]any)["id"] != "d2" {
		t.Fatalf("新设备应作为 device patch: %v", got)
	}
}

func TestDevices未订阅的管理员与屏幕会话不收设备推送(t *testing.T) {
	h, fd := newDeviceHarness(t, 0, dev("d1", "nas", model.PresenceOnline))
	admin := h.dial(adminToken)
	read(t, admin)
	screen := h.dial(screenToken)
	read(t, screen)

	send(t, screen, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicDevices}})
	e := read(t, screen)
	if e["type"] != string(ui.TypeError) || e["error"].(map[string]any)["code"] != ui.ErrSubscribeDenied {
		t.Fatalf("屏幕会话订阅 devices 应被拒: %v", e)
	}

	fd.set(dev("d1", "nas", model.PresenceOffline))
	h.hub.NotifyDevice("d1")
	h.clk.Advance(time.Second)
	ping(t, admin) // 管理员没订阅 devices
	ping(t, screen)
}

func TestDevices对账纠正漏推的变化与删除(t *testing.T) {
	h, fd := newDeviceHarness(t, 0, dev("d1", "nas", model.PresenceOnline), dev("d2", "vps", model.PresenceOnline))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.hub.Start(ctx)
	c := h.dial(adminToken)
	read(t, c)
	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicDevices}})
	read(t, c)

	// 没有任何通知：d1 变化、d2 被删除。
	fd.set(dev("d1", "nas", model.PresenceOffline))
	fd.remove("d2")
	h.clk.Advance(reconcileInterval)
	got := map[string]map[string]any{}
	for i := 0; i < 2; i++ {
		p := read(t, c)
		got[p["entity"].(string)] = p
	}
	if got[ui.EntityDevice]["device"].(map[string]any)["presence"] != model.PresenceOffline || got[ui.EntityDeviceRemoved]["id"] != "d2" {
		t.Fatalf("对账应推送变化与删除: %v", got)
	}
	h.clk.Advance(reconcileInterval)
	ping(t, c) // 没变化就不推
}

// 设备变化通知只做内存登记：即便推送协程正卡在读取设备上，登记方（可能正持有 agentsrv 的锁）也不会被阻塞。
func TestDevices通知不被进行中的读取阻塞(t *testing.T) {
	h, fd := newDeviceHarness(t, 0, dev("d1", "nas", model.PresenceOnline))
	c := h.dial(adminToken)
	read(t, c)
	send(t, c, ui.ClientMessage{Type: ui.TypeSubscribe, Topics: []string{ui.TopicDevices}})
	read(t, c)

	fd.mu.Lock()
	fd.gate, fd.entered = make(chan struct{}), make(chan struct{}, 1)
	gate, entered := fd.gate, fd.entered
	fd.mu.Unlock()
	fd.set(dev("d1", "nas", model.PresenceOffline))
	h.hub.NotifyDevice("d1")
	h.clk.Advance(time.Second)
	select {
	case <-entered:
	case <-time.After(waitTimeout):
		t.Fatal("推送协程应已开始读取设备")
	}

	done := make(chan struct{})
	go func() {
		h.hub.NotifyDevice("d1")
		h.hub.NotifyDevice("d2")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("读取进行中时 NotifyDevice 被阻塞")
	}
	close(gate)
	p := read(t, c)
	if p["entity"] != ui.EntityDevice || p["device"].(map[string]any)["presence"] != model.PresenceOffline {
		t.Fatalf("放开读取后应收到 patch: %v", p)
	}
}
