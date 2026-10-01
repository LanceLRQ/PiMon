import { describe, expect, it } from 'vitest'
import { formatClockInZone } from './time'

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
