package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
)

// deviceStates 把设备仓库适配为实例服务的 DeviceStates。
// 只在创建、更新实例的校验时调用（不在持有设备仓库事务或 agentsrv.flow 时调用），读一次库可以接受。
type deviceStates struct{ s *devices.Store }

func (d deviceStates) Device(id string) (instances.DeviceFacts, bool) {
	dev, err := d.s.Get(context.Background(), id)
	if err != nil {
		if !errors.Is(err, devices.ErrNotFound) {
			slog.Warn("读取设备失败，按设备不存在处理", "device_id", id, "err", err)
		}
		return instances.DeviceFacts{}, false
	}
	return instances.DeviceFacts{Role: dev.Role}, true
}
