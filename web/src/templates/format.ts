// 模板用的格式化：数值、金额、时刻。时刻一律按注入的时区，不用浏览器本地时区。
import type { Lang } from './types'

export function localeFor(lang: Lang): string {
  return lang === 'zh' ? 'zh-CN' : 'en-US'
}

export function formatNumber(value: number, lang: Lang, maxFractionDigits = 2): string {
  return new Intl.NumberFormat(localeFor(lang), { maximumFractionDigits: maxFractionDigits }).format(value)
}

/** 币种是合法 ISO 代码时按货币格式，否则「数值 代码」；不跨币种换算 */
export function formatMoney(amount: number, currency: string | undefined, lang: Lang): string {
  if (currency && /^[A-Za-z]{3}$/.test(currency)) {
    try {
      return new Intl.NumberFormat(localeFor(lang), { style: 'currency', currency: currency.toUpperCase() }).format(amount)
    } catch {
      // 不识别的代码走通用写法
    }
  }
  return currency ? `${formatNumber(amount, lang)} ${currency}` : formatNumber(amount, lang)
}

/** 时区名不合法时回落到 UTC，避免 Intl 抛 RangeError 拖垮整个屏幕 */
export function safeTimeZone(tz: string): string {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz })
    return tz
  } catch {
    return 'UTC'
  }
}

/** 灰显态时间戳：当天显示 HH:mm，更早的带月日 */
export function formatStamp(ms: number, nowMs: number, tz: string, lang: Lang): string {
  const zone = safeTimeZone(tz)
  const locale = localeFor(lang)
  const time = new Intl.DateTimeFormat(locale, { timeZone: zone, hour: '2-digit', minute: '2-digit', hour12: false }).format(ms)
  const day = (v: number) => new Intl.DateTimeFormat('en-CA', { timeZone: zone }).format(v)
  if (day(ms) === day(nowMs)) return time
  const date = new Intl.DateTimeFormat(locale, { timeZone: zone, month: 'numeric', day: 'numeric' }).format(ms)
  return `${date} ${time}`
}
