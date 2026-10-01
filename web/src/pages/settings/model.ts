import type { Settings } from '@/types/generated'

// 设置页的脏状态检测与保存前规整。所有比较都基于规整后的值，首尾空白不算修改。

export type SettingsKey =
  | 'language'
  | 'timezone'
  | 'access_url'
  | 'https_enabled'
  | 'trusted_proxies'
  | 'reduce_effects'
  | 'retention.raw_hours'
  | 'retention.five_min_days'
  | 'retention.hour_days'
  | 'backup.daily_at'
  | 'backup.keep'

export function normalizeForSave(s: Settings): Settings {
  return {
    ...s,
    access_url: s.access_url.trim(),
    trusted_proxies: s.trusted_proxies.map((p) => p.trim()).filter((p) => p !== ''),
    retention: { ...s.retention },
    backup: { ...s.backup },
  }
}

// 返回与基线不同的设置项（trusted_proxies 整个列表算一项）
export function dirtyKeys(draft: Settings, base: Settings): SettingsKey[] {
  const a = normalizeForSave(draft)
  const b = normalizeForSave(base)
  const out: SettingsKey[] = []
  if (a.language !== b.language) out.push('language')
  if (a.timezone !== b.timezone) out.push('timezone')
  if (a.access_url !== b.access_url) out.push('access_url')
  if (a.https_enabled !== b.https_enabled) out.push('https_enabled')
  if (a.trusted_proxies.length !== b.trusted_proxies.length || a.trusted_proxies.some((p, i) => p !== b.trusted_proxies[i])) {
    out.push('trusted_proxies')
  }
  if (a.reduce_effects !== b.reduce_effects) out.push('reduce_effects')
  if (a.retention.raw_hours !== b.retention.raw_hours) out.push('retention.raw_hours')
  if (a.retention.five_min_days !== b.retention.five_min_days) out.push('retention.five_min_days')
  if (a.retention.hour_days !== b.retention.hour_days) out.push('retention.hour_days')
  if (a.backup.daily_at !== b.backup.daily_at) out.push('backup.daily_at')
  if (a.backup.keep !== b.backup.keep) out.push('backup.keep')
  return out
}

// 服务端对反代列表的错误键形如 trusted_proxies[2]，取出各行的错误码
export function trustedProxyErrors(fields: Record<string, string>): Record<number, string> {
  const out: Record<number, string> = {}
  for (const [k, v] of Object.entries(fields)) {
    const m = /^trusted_proxies\[(\d+)\]$/.exec(k)
    if (m) out[Number(m[1])] = v
  }
  return out
}
