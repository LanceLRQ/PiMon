import { describe, expect, it } from 'vitest'
import { effectiveTouch } from './touch'

describe('effectiveTouch', () => {
  it('手动指定优先于一切检测', () => {
    expect(effectiveTouch('touch', false, false)).toBe(true)
    expect(effectiveTouch('none', true, true)).toBe(false)
  })

  it('自动时 kiosk 的 udev 结果优先于页面上报的 coarse_pointer', () => {
    expect(effectiveTouch('auto', false, true)).toBe(true)
    expect(effectiveTouch('auto', true, false)).toBe(false)
  })

  it('kiosk 未检测（null / 缺失）时回退到 coarse_pointer', () => {
    expect(effectiveTouch('auto', true, null)).toBe(true)
    expect(effectiveTouch('auto', false, undefined)).toBe(false)
    expect(effectiveTouch('auto', true)).toBe(true)
  })

  it('两处都没有数据返回 null（未知，不当作无触摸）', () => {
    expect(effectiveTouch('auto', undefined, null)).toBeNull()
    expect(effectiveTouch('auto', undefined)).toBeNull()
  })
})
