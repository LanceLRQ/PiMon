import { useEffect, useState } from 'react'
import type { TFunction } from 'i18next'
import { serverNow } from '@/store/live-store'

// 每秒刷新的「现在」（服务端校正时钟），用于「更新于」等相对时间，不触发任何请求
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => serverNow())
  useEffect(() => {
    const id = setInterval(() => setNow(serverNow()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

export function parseTime(value: string | undefined | null): number | null {
  if (!value) return null
  const t = Date.parse(value)
  return Number.isNaN(t) ? null : t
}

// 「N 秒/分钟/小时/天前」；时间缺失说明从未成功，不当作「刚刚」
export function formatAgo(t: TFunction, nowMs: number, thenMs: number | null): string {
  if (thenMs === null) return t('time.never')
  const sec = Math.max(0, Math.floor((nowMs - thenMs) / 1000))
  if (sec < 60) return t('time.secondsAgo', { n: sec })
  if (sec < 3600) return t('time.minutesAgo', { n: Math.floor(sec / 60) })
  if (sec < 86400) return t('time.hoursAgo', { n: Math.floor(sec / 3600) })
  return t('time.daysAgo', { n: Math.floor(sec / 86400) })
}

// 「N 分钟/小时/天后」，用于额度重置与到期；已过去的时刻显示「已过」
export function formatIn(t: TFunction, nowMs: number, thenMs: number): string {
  const sec = Math.floor((thenMs - nowMs) / 1000)
  if (sec <= 0) return t('time.passed')
  if (sec < 60) return t('time.inSeconds', { n: sec })
  if (sec < 3600) return t('time.inMinutes', { n: Math.floor(sec / 60) })
  if (sec < 86400) return t('time.inHours', { n: Math.floor(sec / 3600) })
  return t('time.inDays', { n: Math.floor(sec / 86400) })
}

export function formatDateTime(ms: number, locale: string): string {
  return new Date(ms).toLocaleString(locale, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

// 数值显示：最多 2 位小数，千分位随语言
export function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value)
}

// 金额：币种是合法 ISO 代码时按货币格式，否则「数值 币种」
export function formatMoney(amount: number, currency: string | undefined, locale: string): string {
  if (currency && /^[A-Za-z]{3}$/.test(currency)) {
    try {
      return new Intl.NumberFormat(locale, { style: 'currency', currency: currency.toUpperCase() }).format(amount)
    } catch {
      // 不识别的代码走下面的通用写法
    }
  }
  return currency ? `${formatNumber(amount, locale)} ${currency}` : formatNumber(amount, locale)
}

// 设置时区里的时刻：同一天只显示 HH:MM，不同天前面加「月/日」。与浏览器本地时区无关；时区名非法时回退 UTC。
export function formatClockInZone(ms: number, nowMs: number, timeZone: string, locale: string): string {
  const fmt = (zone: string, opts: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(locale, { timeZone: zone, ...opts })
  const build = (zone: string) => {
    const day = (t: number) => fmt(zone, { year: 'numeric', month: 'numeric', day: 'numeric' }).format(t)
    const clock = fmt(zone, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(ms)
    if (day(ms) === day(nowMs)) return clock
    return `${fmt(zone, { month: 'numeric', day: 'numeric' }).format(ms)} ${clock}`
  }
  try {
    return build(timeZone)
  } catch {
    return build('UTC')
  }
}
