import { describe, expect, it } from 'vitest'
import { fitScale, pointerToScreen } from './geometry'

describe('画布几何', () => {
  it('按 viewport 比例缩放，取宽高中更紧的一边，不放大', () => {
    expect(fitScale(512, 600, 1024, 600)).toBeCloseTo(0.5)
    expect(fitScale(2000, 300, 1024, 600)).toBeCloseTo(0.5)
    expect(fitScale(4000, 4000, 1024, 600)).toBe(1)
  })
  it('量不到尺寸时按 1', () => {
    expect(fitScale(0, 0, 1024, 600)).toBe(1)
    expect(fitScale(100, 100, 0, 600)).toBe(1)
  })
  it('指针坐标除以缩放换回屏幕坐标', () => {
    expect(pointerToScreen(150, 120, { left: 50, top: 20 }, 0.5)).toEqual({ x: 200, y: 200 })
    expect(pointerToScreen(10, 10, { left: 0, top: 0 }, 0)).toEqual({ x: 10, y: 10 })
  })
})
