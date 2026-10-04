import type { Device, PluginInfo } from '@/types/generated'
import { needsUpgrade } from '@/pages/devices/model'

// 编辑器里「运行位置」的纯规则：哪些设备可选、插件允许的位置、默认设备。

// 能选作运行位置的设备：已确认（成功握手过）且协议兼容。离线、休眠的设备也能选，保存后等上线生效。
export function selectableDevices(devices: readonly Device[] | null): Device[] {
  return (devices ?? []).filter((d) => d.confirmed && !needsUpgrade(d, null))
}

// 插件允许的位置：runs_on 含 hub 才能放中枢，含 agent 才能放设备；插件未选定时都允许。
export function allowedLocations(plugin: PluginInfo | null): { hub: boolean; devices: boolean } {
  if (!plugin) return { hub: true, devices: true }
  return { hub: plugin.runs_on.includes('hub'), devices: plugin.runs_on.includes('agent') }
}

// 插件 runs_on 不含 hub 时的默认设备：优先第一台在线的，没有则第一台可选的；没有可选设备返回 null。
export function defaultDevice(devices: readonly Device[] | null): string | null {
  const list = selectableDevices(devices)
  return (list.find((d) => d.presence === 'online') ?? list[0])?.id ?? null
}

// 位置是不是一台已知但不在线的设备（离线、休眠、连接中断、等待连接）：保存照常，测试运行不可用。
// 设备列表还没加载或设备已不存在时无法判断，按不是处理。
export function deviceNotOnline(devices: readonly Device[] | null, id: string): boolean {
  if (id === 'hub') return false
  const d = (devices ?? []).find((x) => x.id === id)
  return d !== undefined && d.presence !== 'online'
}

// 运行位置对应的代理判定角色：中枢为 'hub'，设备取其角色，设备信息缺失时按最严的 VPS 处理
export function locationRole(devices: readonly Device[] | null, id: string): string {
  if (id === 'hub') return 'hub'
  return (devices ?? []).find((d) => d.id === id)?.role ?? 'vps'
}
