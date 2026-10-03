package instances

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// 运行位置不合法的原因（instance.machine_invalid 的 details.reason）。
const (
	ReasonDeviceNotFound    = "device_not_found"
	ReasonRunsOnMismatch    = "runs_on_mismatch"
	ReasonPluginUnavailable = "plugin_unavailable"
)

// ErrDeviceNotFound 表示运行位置指向的设备不存在；原因为 device_not_found 的 MachineInvalidError 可用 errors.Is 判断它。
var ErrDeviceNotFound = errors.New("设备不存在")

// MachineInvalidError 表示实例的运行位置不合法，Reason 取 Reason* 常量。
type MachineInvalidError struct {
	Reason string
}

func (e *MachineInvalidError) Error() string { return "运行位置不合法: " + e.Reason }

// Unwrap 让原因为 device_not_found 的错误可用 errors.Is(err, ErrDeviceNotFound) 判断。
func (e *MachineInvalidError) Unwrap() error {
	if e.Reason == ReasonDeviceNotFound {
		return ErrDeviceNotFound
	}
	return nil
}

// ProxyLocationError 表示所选代理不能在该运行位置使用。Field 是配置里代理字段的键，Location 是代理的可用位置。
type ProxyLocationError struct {
	Field    string
	Location string
}

func (e *ProxyLocationError) Error() string {
	return "代理（可用位置 " + e.Location + "）不能在该运行位置使用"
}

// MachinePlugins 按机器取可用插件（由 plugins.Registry 实现）：hub 查注册表，设备查其上报的 manifest（带内存缓存）。
type MachinePlugins interface {
	MachinePlugin(machine, id string) (plugins.Plugin, bool)
}

// Remote 是远程实例的下发器（由 agentsrv 实现）。
type Remote interface {
	// MachineChanged 通知该设备上的实例集合或配置有变化，需要重新下发。
	// 调用方可能持有实例服务的内部锁，实现必须非阻塞（只做标记或投递），不得回调实例服务。
	MachineChanged(machine string)
	// RunNow 在设备上立即运行一次实例（「保存并测试」），timeout 是插件超时（实现自行加上通道余量）。
	// 成功返回报告；设备给出的失败结果包装 runtime.ErrTimeout / runtime.ErrFailed（会入库）；
	// 设备没有给出结果时用 NotRecorded 包装（不入库）；另可返回 ErrDeviceOffline、
	// *DeviceIncompatibleError、ErrRunBusy 或 ctx 的错误（都不入库）。
	RunNow(ctx context.Context, machine, instanceID string, timeout time.Duration) (*report.Report, error)
}

// DeviceFacts 是实例校验与展示需要的设备属性。
type DeviceFacts struct {
	// Role 是设备角色：server / nas / dev / vps。
	Role string
	// Offline 表示设备离线且不允许离线；Dormant 表示设备离线且允许离线（休眠），二者互斥。
	// pending 与 disconnected 两种状态都不置位。
	Offline, Dormant bool
	// Maintenance 是设备的维护模式开关。
	Maintenance bool
}

// DeviceStates 按 ID 查设备（由 app 适配）。设备不存在返回 false。
// 展示路径会对全部实例逐个调用，实现不得每次读库。
type DeviceStates interface {
	Device(id string) (DeviceFacts, bool)
}

// UseRemote 接入远程下发器，须在 Start 与任何请求之前调用；不接入时远程实例只是不在本机调度。
func (s *Service) UseRemote(r Remote) { s.remote = r }

// UseDevices 接入设备状态，须在 Start 与任何请求之前调用；不接入时任何设备 ID 都视为不存在。
func (s *Service) UseDevices(d DeviceStates) { s.devices = d }

// ListByMachine 按名称返回运行在 machine 上的实例；没有时为空列表。
func (s *Service) ListByMachine(ctx context.Context, machine string) ([]model.Instance, error) {
	rows, err := s.queryRows(ctx, `SELECT `+rowCols+` FROM plugin_instances WHERE machine = ? ORDER BY name, id`, machine)
	if err != nil {
		return nil, err
	}
	out := make([]model.Instance, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toInstance(r))
	}
	return out, nil
}

// pluginFor 按运行位置取插件：hub 查注册表，设备查其上报的 manifest（内存缓存，不逐次读库）。
// 不得在 DB 事务内调用：设备缓存未命中时会读库，数据库是单连接，事务内再发非 tx 查询会自锁。
func (s *Service) pluginFor(machine, id string) (plugins.Plugin, bool) {
	if machine == machineHub {
		return s.reg.Get(id)
	}
	return s.machines.MachinePlugin(machine, id)
}

// checkMachine 校验插件能否运行在 machine 上，通过时返回该机器上的插件：
//   - hub：注册表里要有该插件（否则 ErrPluginNotFound），且 manifest 的 runs_on 含 hub；
//   - 设备：设备存在；manifest 的 runs_on 含 agent（取设备上报的 manifest，设备没有时取中枢注册表的）；
//     该设备上报的可用插件里有它。设备是否在线不影响，按最后一次上报的 manifest 校验。
func (s *Service) checkMachine(machine, pluginID string) (plugins.Plugin, error) {
	if machine == machineHub {
		p, ok := s.reg.Get(pluginID)
		if !ok {
			return plugins.Plugin{}, ErrPluginNotFound
		}
		if !slices.Contains(p.Manifest.RunsOn, manifest.RunsOnHub) {
			return plugins.Plugin{}, &MachineInvalidError{Reason: ReasonRunsOnMismatch}
		}
		return p, nil
	}
	if _, ok := s.device(machine); !ok {
		return plugins.Plugin{}, &MachineInvalidError{Reason: ReasonDeviceNotFound}
	}
	p, onDevice := s.machines.MachinePlugin(machine, pluginID)
	m := p.Manifest
	if !onDevice {
		if hp, ok := s.reg.Get(pluginID); ok {
			m = hp.Manifest
		}
	}
	if m != nil && !slices.Contains(m.RunsOn, manifest.RunsOnAgent) {
		return plugins.Plugin{}, &MachineInvalidError{Reason: ReasonRunsOnMismatch}
	}
	if !onDevice {
		return plugins.Plugin{}, &MachineInvalidError{Reason: ReasonPluginUnavailable}
	}
	return p, nil
}

// device 查设备属性；未接入设备状态时一律视为不存在。
func (s *Service) device(id string) (DeviceFacts, bool) {
	if s.devices == nil {
		return DeviceFacts{}, false
	}
	return s.devices.Device(id)
}

// checkProxyLocation 校验所选代理能否在 machine 上使用：hub 上全部可用；
// 非 VPS 设备上 lan 与 any 可用；VPS 设备上只有 any 可用。proxyID 为空（直连）时不校验。
func (s *Service) checkProxyLocation(ctx context.Context, machine string, fields []schema.Field, proxyID string) error {
	if proxyID == "" || machine == machineHub || s.proxies == nil {
		return nil
	}
	loc, err := s.proxies.Location(ctx, proxyID)
	if err != nil {
		if isProxyNotFound(err) {
			return model.FieldErrors{proxyKey(fields): model.FieldInvalid}
		}
		return err
	}
	dev, ok := s.device(machine)
	if !ok {
		return &MachineInvalidError{Reason: ReasonDeviceNotFound}
	}
	if locationAllowed(loc, dev.Role) {
		return nil
	}
	return &ProxyLocationError{Field: proxyKey(fields), Location: loc}
}

// locationAllowed 是代理可用位置规则（设备上的实例）：any 随处可用；lan 只给非 VPS 设备；hub 只给中枢本机，设备上不可用。
func locationAllowed(loc, role string) bool {
	switch loc {
	case model.ProxyLocationAny:
		return true
	case model.ProxyLocationLAN:
		return role != model.DeviceRoleVPS
	}
	return false
}

// RoleProxyConflicts 列出运行在设备 machine 上、所选代理不能在 role 角色的设备上使用的实例
// （设备改角色前检查，如改为 vps 后只有 any 代理可用）。没有冲突时为空列表。
func (s *Service) RoleProxyConflicts(ctx context.Context, machine, role string) ([]model.InstanceRef, error) {
	out := []model.InstanceRef{}
	if s.proxies == nil {
		return out, nil
	}
	rows, err := s.proxyRefs(ctx, `machine = ? AND proxy_id <> ''`, machine)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		loc, err := s.proxies.Location(ctx, r.proxyID)
		if err != nil {
			if isProxyNotFound(err) {
				continue
			}
			return nil, err
		}
		if !locationAllowed(loc, role) {
			out = append(out, r.ref)
		}
	}
	return out, nil
}

// ProxyLocationConflicts 列出引用代理 proxyID、运行在设备上、且不能在 newLocation 可用位置下继续使用该代理的实例
// （代理改窄前检查）；各设备的角色取其当前值，hub 上的实例不受限，设备已不存在的实例（本就显示引用失效）跳过。
// 没有冲突时为空列表。
func (s *Service) ProxyLocationConflicts(ctx context.Context, proxyID, newLocation string) ([]model.InstanceRef, error) {
	out := []model.InstanceRef{}
	rows, err := s.proxyRefs(ctx, `proxy_id = ? AND machine <> 'hub'`, proxyID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		dev, ok := s.device(r.machine)
		if ok && !locationAllowed(newLocation, dev.Role) {
			out = append(out, r.ref)
		}
	}
	return out, nil
}

type proxyRef struct {
	ref              model.InstanceRef
	proxyID, machine string
}

// proxyRefs 按条件（单个参数）读取实例的 id、名称、代理与运行位置，按名称排序。
func (s *Service) proxyRefs(ctx context.Context, where, arg string) ([]proxyRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, proxy_id, machine FROM plugin_instances WHERE `+where+` ORDER BY name, id`, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []proxyRef
	for rows.Next() {
		var r proxyRef
		if err := rows.Scan(&r.ref.ID, &r.ref.Name, &r.proxyID, &r.machine); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// machineChanged 通知远程下发器该设备需要重新下发；hub 与未接入下发器时什么也不做。
func (s *Service) machineChanged(machine string) {
	if machine == machineHub || s.remote == nil {
		return
	}
	s.remote.MachineChanged(machine)
}

// NotifyMachine 对运行在 machine 上的全部实例逐个触发变化通知，供设备在线状态、
// 维护模式等变化后刷新其上实例的展示。会读库，调用方不得处于 agentsrv 的会话流程或数据库事务内。
func (s *Service) NotifyMachine(ctx context.Context, machine string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM plugin_instances WHERE machine = ?`, machine)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		s.notify(id)
	}
	return nil
}
