import type { TFunction } from 'i18next'
import type { Device, Proxy } from '@/types/generated'
import { formatAgo, parseTime } from '@/lib/time'
import {
  needsUpgrade,
  placeholderKind,
  presenceView,
  type PlaceholderKind,
  type PresenceView,
  type UpgradeNeed,
} from './model'

// 设备列表一行的展示数据：中枢与设备统一成同一形状，表格与手机卡片共用。
export interface DeviceRow {
  id: string
  hub: boolean
  name: string
  role: string
  hostname: string
  // local | dial_in | dial_out
  direction: string
  // 拨出设备经哪个代理（名称，直连为 null）
  proxyName: string | null
  address: string
  platform: string
  version: string
  presence: PresenceView
  // 状态下方的补充行：最后在线、中枢已运行时间或占位说明
  sub: string
  placeholder: PlaceholderKind | null
  // 重新配对中的占位：被替换设备的名称
  replacesName: string
  upgrade: UpgradeNeed | null
  upgradeReason: string | null
  instances: number | null
  allowOffline: boolean
  maintenance: boolean
  needsRepair: boolean
  connError: string
  device: Device | null
}

export const hubPresence: PresenceView = { shape: 'ok', labelKey: 'devices.presence.online', tone: 'ok', needsRepair: false }

export interface HubFacts {
  version: string
  platform: string
  uptimeText: string | null
  instances: number | null
}

export function hubRow(t: TFunction, facts: HubFacts): DeviceRow {
  return {
    id: 'hub',
    hub: true,
    name: t('devices.hub'),
    role: 'hub',
    hostname: '',
    direction: 'local',
    proxyName: null,
    address: '127.0.0.1',
    platform: facts.platform,
    version: facts.version,
    presence: hubPresence,
    sub: facts.uptimeText ? t('devices.list.hubUptime', { uptime: facts.uptimeText }) : '',
    placeholder: null,
    replacesName: '',
    upgrade: null,
    upgradeReason: null,
    instances: facts.instances,
    allowOffline: false,
    maintenance: false,
    needsRepair: false,
    connError: '',
    device: null,
  }
}

export function deviceRow(
  t: TFunction,
  d: Device,
  ctx: { devices: readonly Device[]; hubVersion: string | null; now: number; proxies: readonly Proxy[]; instanceCounts: ReadonlyMap<string, number> | null },
): DeviceRow {
  const placeholder = placeholderKind(d, ctx.devices)
  const presence = presenceView(d)
  const seen = parseTime(d.last_seen_at)
  let sub: string
  if (placeholder) sub = t(`devices.list.placeholder.${placeholder}`, { name: ctx.devices.find((x) => x.id === d.replaces)?.name ?? d.replaces })
  else if (seen === null) sub = t('devices.list.neverSeen')
  else sub = t('devices.list.lastSeen', { ago: formatAgo(t, ctx.now, seen) })
  const proxy = d.direction === 'dial_out' && d.proxy_id ? (ctx.proxies.find((p) => p.id === d.proxy_id)?.name ?? d.proxy_id) : null
  return {
    id: d.id,
    hub: false,
    name: d.name,
    role: d.role,
    hostname: d.hostname,
    direction: d.direction,
    proxyName: proxy,
    address: d.address || '—',
    platform: d.os ? `${d.os}/${d.arch}` : '—',
    version: d.agent_version || '—',
    presence,
    sub,
    placeholder,
    replacesName: ctx.devices.find((x) => x.id === d.replaces)?.name ?? d.replaces,
    upgrade: needsUpgrade(d, ctx.hubVersion),
    upgradeReason: d.incompatible?.reason ?? null,
    instances: d.confirmed && ctx.instanceCounts ? (ctx.instanceCounts.get(d.id) ?? 0) : null,
    allowOffline: d.allow_offline,
    maintenance: d.maintenance,
    needsRepair: presence.needsRepair,
    connError: d.conn_error ?? '',
    device: d,
  }
}
