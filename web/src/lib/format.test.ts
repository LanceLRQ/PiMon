import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { formatBytes, formatUptime } from './format'

describe('formatBytes', () => {
  it('按 1024 进位并控制小数位', () => {
    expect(formatBytes(0, 'zh')).toBe('0 B')
    expect(formatBytes(512, 'zh')).toBe('512 B')
    expect(formatBytes(1536, 'zh')).toBe('1.5 KB')
    expect(formatBytes(46 * 1024 * 1024, 'zh')).toBe('46 MB')
    expect(formatBytes(4.2 * 1024 * 1024, 'en')).toBe('4.2 MB')
    expect(formatBytes(5 * 1024 ** 4, 'en')).toBe('5 TB')
  })
  it('异常值按 0 处理', () => {
    expect(formatBytes(-1, 'zh')).toBe('0 B')
    expect(formatBytes(Number.NaN, 'zh')).toBe('0 B')
  })
})

describe('formatUptime', () => {
  it('取最大的两级单位', async () => {
    const zh = await createI18n('zh')
    const en = await createI18n('en')
    expect(formatUptime(zh.t, 3 * 86400 + 4 * 3600 + 59)).toBe('3 天 4 小时')
    expect(formatUptime(zh.t, 4 * 3600 + 12 * 60)).toBe('4 小时 12 分')
    expect(formatUptime(zh.t, 300)).toBe('5 分钟')
    expect(formatUptime(zh.t, 30)).toBe('30 秒')
    expect(formatUptime(en.t, 86400 + 3600)).toBe('1d 1h')
  })
})
