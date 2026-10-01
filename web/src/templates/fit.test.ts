import { describe, expect, it } from 'vitest'
import { fitFontSize, fitList, listCapacity, textUnits, truncateByWidth } from './fit'

describe('textUnits', () => {
  it('中日韩字符按 1、西文按 0.55 计宽', () => {
    expect(textUnits('温度')).toBe(2)
    expect(textUnits('ab')).toBeCloseTo(1.1)
    expect(textUnits('')).toBe(0)
  })
})

describe('truncateByWidth', () => {
  it('放得下原样返回，放不下截断并加省略号', () => {
    expect(truncateByWidth('短文本', 10)).toBe('短文本')
    const out = truncateByWidth('这是一段很长很长的标题文字', 6)
    expect(out.endsWith('…')).toBe(true)
    expect(textUnits(out)).toBeLessThanOrEqual(6)
  })
  it('预算为 0 或负时返回空串', () => {
    expect(truncateByWidth('abc', 0)).toBe('')
  })
})

describe('fitFontSize', () => {
  it('短文本取上限，长文本缩小，最小不低于下限', () => {
    expect(fitFontSize('42', { width: 200, height: 80, max: 48, min: 12 })).toBe(48)
    const mid = fitFontSize('1234567890.12', { width: 120, height: 60, max: 48, min: 12 })
    expect(mid).toBeLessThan(48)
    expect(mid).toBeGreaterThanOrEqual(12)
    expect(fitFontSize('超'.repeat(200), { width: 100, height: 40, max: 40, min: 12 })).toBe(12)
  })
  it('允许多行时可用更大字号', () => {
    const one = fitFontSize('一二三四五六七八九十', { width: 100, height: 100, max: 40, min: 10, maxLines: 1 })
    const three = fitFontSize('一二三四五六七八九十', { width: 100, height: 100, max: 40, min: 10, maxLines: 3 })
    expect(three).toBeGreaterThan(one)
  })
})

describe('fitList / listCapacity', () => {
  it('放得下全部时不显示 +N', () => {
    expect(fitList(3, 5)).toEqual({ shown: 3, more: 0 })
  })
  it('放不下时留一行给「+N」', () => {
    expect(fitList(50, 4)).toEqual({ shown: 3, more: 47 })
  })
  it('容量为 0 或 1 的边界', () => {
    expect(fitList(5, 0)).toEqual({ shown: 0, more: 5 })
    expect(fitList(5, 1)).toEqual({ shown: 0, more: 5 })
    expect(fitList(1, 1)).toEqual({ shown: 1, more: 0 })
  })
  it('按高度换算容量', () => {
    expect(listCapacity(100, 24)).toBe(4)
    expect(listCapacity(10, 24)).toBe(0)
  })
})
