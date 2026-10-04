package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// deviceAttrs 是缓存的设备静态属性；exists 为 false 表示设备不存在（同样缓存，避免吊销后每次展示都读库）。
type deviceAttrs struct {
	exists       bool
	role         string
	allowOffline bool
	maintenance  bool
}

// deviceStates 把设备仓库与在线状态适配为实例服务的 DeviceStates。
// 实例展示每 15 秒对全部实例调用一次，所以设备属性放内存缓存，仓库变更时整体失效；
// 在线状态直接读 Presence（内存）。失效回调只清缓存，可在任何上下文中同步调用。
type deviceStates struct {
	store *devices.Store
	pres  *devices.Presence

	mu    sync.Mutex
	gen   uint64 // 每次失效递增，防止读库期间发生的变更被旧值覆盖
	cache map[string]deviceAttrs
}

func newDeviceStates(s *devices.Store, p *devices.Presence) *deviceStates {
	d := &deviceStates{store: s, pres: p, cache: map[string]deviceAttrs{}}
	s.OnChange(func(string, bool) { d.invalidate() })
	return d
}

func (d *deviceStates) invalidate() {
	d.mu.Lock()
	d.gen++
	clear(d.cache)
	d.mu.Unlock()
}

func (d *deviceStates) attrs(id string) deviceAttrs {
	d.mu.Lock()
	if a, ok := d.cache[id]; ok {
		d.mu.Unlock()
		return a
	}
	gen := d.gen
	d.mu.Unlock()

	var a deviceAttrs
	dev, err := d.store.Get(context.Background(), id)
	switch {
	case err == nil:
		a = deviceAttrs{exists: true, role: dev.Role, allowOffline: dev.AllowOffline, maintenance: dev.Maintenance}
	case errors.Is(err, devices.ErrNotFound):
	default:
		// 读库失败不缓存，下次重试；本次按设备不存在处理。
		slog.Warn("读取设备失败，按设备不存在处理", "device_id", id, "err", err)
		return a
	}
	d.mu.Lock()
	if d.gen == gen {
		d.cache[id] = a
	}
	d.mu.Unlock()
	return a
}

// Device 返回设备的展示事实：离线且允许离线为 Dormant，否则为 Offline；
// pending、disconnected 与未知状态都不置位。
func (d *deviceStates) Device(id string) (instances.DeviceFacts, bool) {
	a := d.attrs(id)
	if !a.exists {
		return instances.DeviceFacts{}, false
	}
	f := instances.DeviceFacts{Role: a.role, Maintenance: a.maintenance}
	if d.pres != nil {
		if st, _ := d.pres.State(id); st == model.PresenceOffline {
			f.Offline, f.Dormant = !a.allowOffline, a.allowOffline
		}
	}
	return f, true
}
