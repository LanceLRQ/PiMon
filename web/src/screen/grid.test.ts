import { describe, expect, it } from 'vitest'
import { computeGrid, widgetRect } from './grid'

describe('网格尺寸计算', () => {
  it('1024×600 的 8×5 网格每格 128×120', () => {
    const m = computeGrid(1024, 600, { cols: 8, rows: 5 })
    expect(m.cellW).toBe(128)
    expect(m.cellH).toBe(120)
  })

  it('小组件按位置与跨格数绝对定位', () => {
    const m = computeGrid(1024, 600, { cols: 8, rows: 5 })
    expect(widgetRect(m, { col: 2, row: 1, size: { cols: 2, rows: 3 } })).toEqual({
      left: 256,
      top: 120,
      width: 256,
      height: 360,
    })
  })

  it('除不尽时按累计边界取整，相邻小组件不留缝也不重叠，最后一列贴右边', () => {
    const m = computeGrid(800, 480, { cols: 6, rows: 4 })
    const rects = [0, 1, 2, 3, 4, 5].map((col) => widgetRect(m, { col, row: 0, size: { cols: 1, rows: 1 } }))
    for (let i = 1; i < rects.length; i++) expect(rects[i].left).toBe(rects[i - 1].left + rects[i - 1].width)
    const last = rects[5]
    expect(last.left + last.width).toBe(800)
    const full = widgetRect(m, { col: 0, row: 0, size: { cols: 6, rows: 4 } })
    expect(full).toEqual({ left: 0, top: 0, width: 800, height: 480 })
  })

  it('间距向内收，每侧各收一半', () => {
    const m = computeGrid(1024, 600, { cols: 8, rows: 5 })
    expect(widgetRect(m, { col: 0, row: 0, size: { cols: 1, rows: 1 } }, 12)).toEqual({
      left: 6,
      top: 6,
      width: 116,
      height: 108,
    })
  })

  it('网格或视口非法时尺寸为 0，不产生 NaN', () => {
    const m = computeGrid(0, 0, { cols: 0, rows: 0 })
    expect(m.cellW).toBe(0)
    expect(widgetRect(m, { col: 1, row: 1, size: { cols: 1, rows: 1 } })).toEqual({ left: 0, top: 0, width: 0, height: 0 })
  })
})
