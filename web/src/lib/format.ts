import type { TFunction } from 'i18next'

const units = ['B', 'KB', 'MB', 'GB', 'TB']

// 字节数按 1024 进位显示：<10 保留 1 位小数，其余取整；负数与非有限值不应出现，按 0 B 处理
export function formatBytes(bytes: number, locale: string): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B'
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const digits = i > 0 && v < 10 ? 1 : 0
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: digits, minimumFractionDigits: 0 }).format(v)} ${units[i]}`
}

// 运行时长：「3 天 4 小时」「4 小时 12 分」「5 分钟」「30 秒」
export function formatUptime(t: TFunction, seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return t('time.uptimeDays', { d, h })
  if (h > 0) return t('time.uptimeHours', { h, m })
  if (m > 0) return t('time.uptimeMinutes', { m })
  return t('time.uptimeSeconds', { s: s % 60 })
}
