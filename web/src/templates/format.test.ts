import { describe, expect, it } from 'vitest'
import { formatBytes, formatDuration } from './format'

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

describe('formatBytes（Ruling 52）：1024 进制自动换算，至多 3 位有效数字', () => {
  it('B/s 随量级换算', () => {
    expect(formatBytes(12_646.96, 'B/s', 'zh')).toEqual({ text: '12.4', unit: 'KB/s' })
    expect(formatBytes(512, 'B/s', 'zh')).toEqual({ text: '512', unit: 'B/s' })
    expect(formatBytes(3.5 * 1024 * 1024, 'B/s', 'zh')).toEqual({ text: '3.5', unit: 'MB/s' })
    expect(formatBytes(1.234 * 1024 ** 3, 'B', 'zh')).toEqual({ text: '1.23', unit: 'GB' })
  })
  it('进位边界：恰好 1024 进一级；不足 1024 留在当前级别，三位有效数字', () => {
    expect(formatBytes(1024, 'B', 'zh')).toEqual({ text: '1', unit: 'KB' })
    expect(formatBytes(1023, 'B', 'zh')).toEqual({ text: '1,020', unit: 'B' })
    expect(formatBytes(1024 ** 2, 'B/s', 'zh')).toEqual({ text: '1', unit: 'MB/s' })
  })
  it('0、负数与小于 1 的值', () => {
    expect(formatBytes(0, 'B', 'zh')).toEqual({ text: '0', unit: 'B' })
    expect(formatBytes(-2048, 'B/s', 'zh')).toEqual({ text: '-2', unit: 'KB/s' })
    expect(formatBytes(0.5, 'B/s', 'zh')).toEqual({ text: '0.5', unit: 'B/s' })
  })
  it('英文千分位沿用 en-US，三位有效数字不会出现千分位以上的小数', () => {
    expect(formatBytes(1000 * 1024, 'B', 'en')).toEqual({ text: '1,000', unit: 'KB' })
  })
  it('非字节单位或非有限值返回 null', () => {
    expect(formatBytes(5, '%', 'zh')).toBeNull()
    expect(formatBytes(Number.NaN, 'B', 'zh')).toBeNull()
  })
})
