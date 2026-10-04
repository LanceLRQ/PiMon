import type { i18n as I18n } from 'i18next'
import { translateError } from '@/i18n/errors'
import type { ShapeState } from '@/ui/status-shape'
import type { Device } from '@/types/generated'

// 设备页公共工具：设备列表、实例运行位置、总览计数、编辑器预览共用，不各写一份。

// 设备 id 到显示名：hub 是中枢本机（名称由调用方按语言传入）；设备已不存在时返回 id 并标记失效。
// 设备列表还没加载（null）时无法判断是否失效，不标记。
export function deviceName(
  id: string,
  devices: readonly Device[] | null,
  hubLabel: string,
): { name: string; missing: boolean } {
  if (id === 'hub') return { name: hubLabel, missing: false }
  const d = devices?.find((x) => x.id === id)
  if (d) return { name: d.name, missing: false }
  return { name: id, missing: devices !== null }
}

export type PresenceTone = 'ok' | 'weak' | 'warn' | 'muted' | 'neutral'

export interface PresenceView {
  shape: ShapeState
  // i18n 键：devices.presence.*
  labelKey: string
  // ok 正常；weak 弱警示（连接中断）；warn 警示（离线）；muted 灰色（休眠）；neutral 中性（等待连接、未知）
  tone: PresenceTone
  // 拨出设备凭据失效或被吊销：重连循环已停止，需要重新配对
  needsRepair: boolean
}

const presenceTable: Record<string, Pick<PresenceView, 'shape' | 'tone'>> = {
  online: { shape: 'ok', tone: 'ok' },
  disconnected: { shape: 'disconnected', tone: 'weak' },
  offline: { shape: 'offline', tone: 'warn' },
  dormant: { shape: 'dormant', tone: 'muted' },
  pending: { shape: 'pending', tone: 'neutral' },
}

export function presenceView(d: Device): PresenceView {
  const known = presenceTable[d.presence]
  return {
    shape: known?.shape ?? 'unknown',
    tone: known?.tone ?? 'neutral',
    labelKey: known ? `devices.presence.${d.presence}` : 'status.unknown',
    needsRepair: d.conn_error === 'bad_credential' || d.conn_error === 'revoked',
  }
}

export interface UpgradeNeed {
  // required：协议不兼容，必须升级才能接入；differs：仅版本字符串不同，较弱的提示
  level: 'required' | 'differs'
  // 要升级的一方：agent 或中枢
  target: 'agent' | 'hub'
}

// 两级判定：incompatible 非空为「需要升级」（version_too_new 要升级中枢，其余升级 agent）；
// 否则版本字符串不同且都非空为「版本不同」。开发构建（中枢版本 dev）总是判为不同。
export function needsUpgrade(d: Device, hubVersion: string | null | undefined): UpgradeNeed | null {
  if (d.incompatible) {
    return { level: 'required', target: d.incompatible.reason === 'version_too_new' ? 'hub' : 'agent' }
  }
  if (d.agent_version && hubVersion && d.agent_version !== hubVersion) {
    return { level: 'differs', target: 'agent' }
  }
  return null
}

// 不兼容原因的文案：与 API 错误翻译共用同一实现（按原因取，查不到退回通用文案）。
export function incompatibleText(i18n: I18n, reason: string | null | undefined): string {
  return translateError(i18n, 'device.incompatible', reason ? { reason } : undefined)
}

// 中枢版本：构建时未注入的占位值视为未知。
export function hubVersionOf(build: string | null | undefined): string | null {
  return build && build !== '__PIMON_BUILD__' ? build : null
}

// 总览口径：只算已确认设备（占位不算），不含中枢。
export function confirmedDevices(devices: readonly Device[]): Device[] {
  return devices.filter((d) => d.confirmed)
}

// 在线 agent：N = presence 为 online 的已确认设备，M = 已确认设备总数。
export function agentCounts(devices: readonly Device[]): { online: number; total: number } {
  const list = confirmedDevices(devices)
  return { online: list.filter((d) => d.presence === 'online').length, total: list.length }
}

export type DeviceAttentionKind = 'offline' | 'upgrade'

// 需要处理的设备：离线（休眠不算，设计 3.6），或协议不兼容、必须升级；两者同时成立时以升级为准。
export function deviceAttention(d: Device, hubVersion: string | null | undefined): DeviceAttentionKind | null {
  if (!d.confirmed) return null
  if (needsUpgrade(d, hubVersion)?.level === 'required') return 'upgrade'
  return d.presence === 'offline' ? 'offline' : null
}

export type PlaceholderKind = 'pending' | 'repairing' | 'dangling' | 'needsUpgrade'

// 未确认占位的种类（已确认的设备返回 null）：
// replaces 指向现存设备为「重新配对中」，指向已不存在的设备为「悬挂占位」（需要手工删除）；
// 否则带不兼容原因的为「需要升级后才能接入」，其余为「等待连接」。
export function placeholderKind(d: Device, devices: readonly Device[]): PlaceholderKind | null {
  if (d.confirmed) return null
  if (d.replaces) return devices.some((x) => x.id === d.replaces) ? 'repairing' : 'dangling'
  return d.incompatible ? 'needsUpgrade' : 'pending'
}

export type DeviceFilter = 'all' | 'online' | 'bad' | 'dormant' | 'pending'

// 异常：连接中断、离线，或必须升级才能接入。
export function isDeviceBad(d: Device): boolean {
  return d.presence === 'disconnected' || d.presence === 'offline' || !!d.incompatible
}

export function matchesFilter(d: Device, filter: DeviceFilter): boolean {
  switch (filter) {
    case 'online':
      return d.presence === 'online'
    case 'bad':
      return isDeviceBad(d)
    case 'dormant':
      return d.presence === 'dormant'
    case 'pending':
      return d.presence === 'pending'
    default:
      return true
  }
}

// 搜索名称、主机名、地址（不区分大小写）。
export function matchesQuery(d: Device, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [d.name, d.hostname, d.address].some((v) => v.toLowerCase().includes(q))
}

export interface DeviceCounts {
  online: number
  disconnected: number
  offline: number
  dormant: number
  pending: number
  upgrade: number
  bad: number
}

// 汇总条口径：含占位，不含中枢；「需要升级」含两级（必须升级与版本不同）。
export function deviceCounts(devices: readonly Device[], hubVersion: string | null | undefined): DeviceCounts {
  const c: DeviceCounts = { online: 0, disconnected: 0, offline: 0, dormant: 0, pending: 0, upgrade: 0, bad: 0 }
  for (const d of devices) {
    if (d.presence in c) c[d.presence as 'online']++
    if (needsUpgrade(d, hubVersion)) c.upgrade++
    if (isDeviceBad(d)) c.bad++
  }
  return c
}
