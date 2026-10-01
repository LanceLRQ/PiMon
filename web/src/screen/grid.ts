import type { Grid, WidgetSize } from '@/types/generated'

// 网格引擎的纯计算：容器 100vw × 100vh，单元格尺寸由 viewport 与列行数决定，小组件按位置绝对定位。

export interface GridMetrics {
  cols: number
  rows: number
  width: number
  height: number
  cellW: number
  cellH: number
}

export interface Rect {
  left: number
  top: number
  width: number
  height: number
}

export interface Placement {
  col: number
  row: number
  size: WidgetSize
}

export function computeGrid(width: number, height: number, grid: Grid): GridMetrics {
  const valid = width > 0 && height > 0 && grid.cols > 0 && grid.rows > 0
  return {
    cols: grid.cols,
    rows: grid.rows,
    width: valid ? width : 0,
    height: valid ? height : 0,
    cellW: valid ? width / grid.cols : 0,
    cellH: valid ? height / grid.rows : 0,
  }
}

/**
 * 小组件的像素矩形。边界按累计位置取整，保证相邻小组件不留缝、不重叠，最后一列/行贴齐容器边缘；
 * gap 是相邻小组件的视觉间距，每侧向内收一半。
 */
export function widgetRect(m: GridMetrics, p: Placement, gap = 0): Rect {
  if (m.cellW === 0 || m.cellH === 0) return { left: 0, top: 0, width: 0, height: 0 }
  const left = Math.round(p.col * m.cellW)
  const right = Math.round((p.col + p.size.cols) * m.cellW)
  const top = Math.round(p.row * m.cellH)
  const bottom = Math.round((p.row + p.size.rows) * m.cellH)
  const inset = gap / 2
  return {
    left: left + inset,
    top: top + inset,
    width: Math.max(0, right - left - gap),
    height: Math.max(0, bottom - top - gap),
  }
}
