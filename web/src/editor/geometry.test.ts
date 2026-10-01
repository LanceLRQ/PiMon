import { describe, expect, it } from 'vitest'
import { CANVAS_PAD, RULER_SIZE, fitCanvasScale, fitScale, pointerToScreen } from './geometry'

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

  describe('画布加标尺 contain', () => {
    const cases: [number, number][] = [[240, 600], [320, 500], [720, 500], [1000, 700], [300, 80]]
    for (const [w, h] of cases) {
      it(`容器 ${w}×${h}：画布像素宽高加标尺槽不超出可用区`, () => {
        const s = fitCanvasScale(w, h, 1024, 600)
        expect(Math.floor(1024 * s) + RULER_SIZE).toBeLessThanOrEqual(w - 2 * CANVAS_PAD)
        expect(Math.floor(600 * s) + RULER_SIZE).toBeLessThanOrEqual(h - 2 * CANVAS_PAD)
        expect(s).toBeLessThanOrEqual(1)
      })
    }
    it('空间足够时不放大', () => {
      expect(fitCanvasScale(4000, 4000, 1024, 600)).toBe(1)
    })
  })
})
