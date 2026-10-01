import { describe, expect, it } from 'vitest'
import { formatDuration } from './format'

const D = 86400
describe('formatDuration（Ruling 49）：取最大两级', () => {
  it('中文', () => {
    expect(formatDuration(3_003_654, 'zh')).toBe('34 天 18 小时')
    expect(formatDuration(5 * 60 + 12, 'zh')).toBe('5 分 12 秒')
    expect(formatDuration(3 * 3600 + 20 * 60 + 59, 'zh')).toBe('3 小时 20 分')
    expect(formatDuration(45, 'zh')).toBe('45 秒')
    expect(formatDuration(0, 'zh')).toBe('0 秒')
  })
  it('英文', () => {
    expect(formatDuration(3_003_654, 'en')).toBe('34d 18h')
    expect(formatDuration(5 * 60 + 12, 'en')).toBe('5m 12s')
    expect(formatDuration(45, 'en')).toBe('45s')
  })
  it('次级为 0 时只显示最大一级', () => {
    expect(formatDuration(2 * D, 'zh')).toBe('2 天')
    expect(formatDuration(2 * D, 'en')).toBe('2d')
    expect(formatDuration(3600, 'zh')).toBe('1 小时')
  })
  it('小数向下取整，负数与非有限值返回 null', () => {
    expect(formatDuration(59.9, 'zh')).toBe('59 秒')
    expect(formatDuration(-1, 'zh')).toBeNull()
    expect(formatDuration(Number.NaN, 'zh')).toBeNull()
  })
})
