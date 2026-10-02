import type { Grid, WidgetSize } from '@/types/generated'

// 编辑器网格的纯函数：碰撞、越界、尺寸白名单、吸附、方向键移动。
// 规则与 Go 侧一致，两端共用 src/internal/hub/screens/testdata/grid_cases.json 校验。

export interface GridWidgetRect {
  id: string
  col: number
  row: number
  w: number
  h: number
  /** 形如 "2x1" 的允许尺寸；缺省表示不限 */
  allowed?: string[]
}

export type GridProblemCode = 'invalid_size' | 'size_not_allowed' | 'out_of_bounds' | 'overlap'

export interface GridProblem {
  widget: string
  code: GridProblemCode
  /** 仅 overlap：与之重叠的靠前的那个 */
  with?: string
}

export interface GridMetricsLike {
  cols: number
  rows: number
  cellW: number
  cellH: number
}

export interface Cell {
  col: number
  row: number
}

export function sizeToKey(size: WidgetSize): string {
  return `${size.cols}x${size.rows}`
}

export function parseSizeKey(key: string): WidgetSize | null {
  const m = /^(\d+)x(\d+)$/.exec(key)
  return m ? { cols: Number(m[1]), rows: Number(m[2]) } : null
}

/** 尺寸是否在白名单内；allowed 缺省表示不限 */
export function sizeAllowed(allowed: readonly WidgetSize[] | undefined, size: WidgetSize): boolean {
  if (!allowed) return true
  return allowed.some((a) => a.cols === size.cols && a.rows === size.rows)
}

function outOfBounds(grid: Grid, r: GridWidgetRect): boolean {
  return r.col < 0 || r.row < 0 || r.col + r.w > grid.cols || r.row + r.h > grid.rows
}

function overlaps(a: GridWidgetRect, b: GridWidgetRect): boolean {
  return a.col < b.col + b.w && b.col < a.col + a.w && a.row < b.row + b.h && b.row < a.row + a.h
}

/** 整体检查：无效尺寸、白名单、越界、两两重叠（每对一条，widget 为靠后的）。尺寸无效者不参与其余检查 */
export function checkGrid(grid: Grid, widgets: readonly GridWidgetRect[]): GridProblem[] {
  const problems: GridProblem[] = []
  const valid: GridWidgetRect[] = []
  for (const r of widgets) {
    if (r.w < 1 || r.h < 1) {
      problems.push({ widget: r.id, code: 'invalid_size' })
      continue
    }
    if (r.allowed && !r.allowed.includes(`${r.w}x${r.h}`)) problems.push({ widget: r.id, code: 'size_not_allowed' })
    if (outOfBounds(grid, r)) problems.push({ widget: r.id, code: 'out_of_bounds' })
    valid.push(r)
  }
  for (let i = 0; i < valid.length; i++) {
    for (let j = 0; j < i; j++) {
      if (overlaps(valid[j], valid[i])) problems.push({ widget: valid[i].id, code: 'overlap', with: valid[j].id })
    }
  }
  return problems
}

/** 把拖动中小组件左上角的像素位置（画布坐标）吸附到最近的格，并钳制在网格内 */
export function snapToCell(left: number, top: number, m: GridMetricsLike, size: WidgetSize): Cell {
  if (m.cellW <= 0 || m.cellH <= 0) return { col: 0, row: 0 }
  const clamp = (v: number, max: number) => Math.max(0, Math.min(max, v))
  return {
    col: clamp(Math.round(left / m.cellW), m.cols - size.cols),
    row: clamp(Math.round(top / m.cellH), m.rows - size.rows),
  }
}

export interface PlacementResult {
  ok: boolean
  reason?: 'out_of_bounds' | 'overlap'
  /** 重叠的其他小组件 id */
  conflicts: string[]
}

/** 候选位置能否放下；others 里与候选同 id 的忽略（移动自己时） */
export function evaluatePlacement(grid: Grid, others: readonly GridWidgetRect[], cand: GridWidgetRect): PlacementResult {
  if (outOfBounds(grid, cand)) return { ok: false, reason: 'out_of_bounds', conflicts: [] }
  const conflicts = others.filter((o) => o.id !== cand.id && overlaps(o, cand)).map((o) => o.id)
  return conflicts.length ? { ok: false, reason: 'overlap', conflicts } : { ok: true, conflicts: [] }
}

const arrowDelta: Record<string, Cell> = {
  ArrowLeft: { col: -1, row: 0 },
  ArrowRight: { col: 1, row: 0 },
  ArrowUp: { col: 0, row: -1 },
  ArrowDown: { col: 0, row: 1 },
}

/** 方向键按格移动一步；越界、撞到别的小组件或不是方向键返回 null */
export function moveByKey(key: string, grid: Grid, others: readonly GridWidgetRect[], cur: GridWidgetRect): Cell | null {
  const d = arrowDelta[key]
  if (!d) return null
  const next = { ...cur, col: cur.col + d.col, row: cur.row + d.row }
  return evaluatePlacement(grid, others, next).ok ? { col: next.col, row: next.row } : null
}

/** 行优先找第一个放得下该尺寸的空位 */
export function firstFreeSpot(grid: Grid, others: readonly GridWidgetRect[], size: WidgetSize): Cell | null {
  for (let row = 0; row + size.rows <= grid.rows; row++) {
    for (let col = 0; col + size.cols <= grid.cols; col++) {
      const cand = { id: '', col, row, w: size.cols, h: size.rows }
      if (evaluatePlacement(grid, others, cand).ok) return { col, row }
    }
  }
  return null
}

/** 换成新网格后越界的小组件 id（缩小网格前先算出来；对话框归保存流程） */
export function outOfBoundsWidgets(grid: Grid, widgets: readonly GridWidgetRect[]): string[] {
  return widgets.filter((r) => r.w >= 1 && r.h >= 1 && outOfBounds(grid, r)).map((r) => r.id)
}

/** 被占用的格数（越界部分不计） */
export function occupiedCells(grid: Grid, widgets: readonly GridWidgetRect[]): number {
  const used = new Set<number>()
  for (const r of widgets) {
    for (let y = Math.max(0, r.row); y < Math.min(grid.rows, r.row + r.h); y++) {
      for (let x = Math.max(0, r.col); x < Math.min(grid.cols, r.col + r.w); x++) used.add(y * grid.cols + x)
    }
  }
  return used.size
}
