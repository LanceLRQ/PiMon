import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { formatClockInZone, formatIn, formatInMinutes } from './time'

describe('formatClockInZone', () => {
  const now = Date.parse('2026-10-01T14:47:00Z')
  it('同一天只显示时分，按给定时区而不是浏览器时区', () => {
    expect(formatClockInZone(Date.parse('2026-10-01T15:00:00Z'), now, 'Asia/Shanghai', 'zh')).toBe('23:00')
    expect(formatClockInZone(Date.parse('2026-10-01T15:00:00Z'), now, 'UTC', 'zh')).toBe('15:00')
  })
  it('不在同一天时带上月日（日期也按给定时区判断）', () => {
    // 上海 23:00 与次日 07:00：now 在上海是 10/1，15:00Z 之后的 23:30Z 在上海已是 10/2
    expect(formatClockInZone(Date.parse('2026-10-01T23:30:00Z'), now, 'Asia/Shanghai', 'zh')).toMatch(/^10\/2 07:30$/)
  })
  it('时区非法时回退 UTC', () => {
    expect(formatClockInZone(Date.parse('2026-10-01T15:00:00Z'), now, 'Nope/Zone', 'zh')).toBe('15:00')
  })
})

describe('formatInMinutes / formatIn', () => {
  it('不足 2 小时带分钟：22:02 到 00:00 是 1 小时 58 分后，而不是 1 小时后', async () => {
    const zh = await createI18n('zh')
    const en = await createI18n('en')
    expect(formatInMinutes(zh.t, 118)).toBe('1 小时 58 分后')
    expect(formatInMinutes(en.t, 118)).toBe('in 1 h 58 min')
  })
  it('整点、分钟与大于 2 小时的边界', async () => {
    const zh = await createI18n('zh')
    expect(formatInMinutes(zh.t, 0)).toBe('0 分钟后')
    expect(formatInMinutes(zh.t, 59)).toBe('59 分钟后')
    expect(formatInMinutes(zh.t, 60)).toBe('1 小时后')
    expect(formatInMinutes(zh.t, 61)).toBe('1 小时 1 分后')
    expect(formatInMinutes(zh.t, 120)).toBe('2 小时后')
    expect(formatInMinutes(zh.t, 7 * 60 + 30)).toBe('7 小时后')
  })
  it('formatIn 的小时段与之共用同一写法，天与秒不变', async () => {
    const zh = await createI18n('zh')
    const now = Date.parse('2026-10-01T22:02:00Z')
    expect(formatIn(zh.t, now, Date.parse('2026-10-02T00:00:00Z'))).toBe('1 小时 58 分后')
    expect(formatIn(zh.t, now, now + 5 * 3600_000)).toBe('5 小时后')
    expect(formatIn(zh.t, now, now + 30_000)).toBe('30 秒后')
    expect(formatIn(zh.t, now, now + 3 * 86400_000)).toBe('3 天后')
  })
})
