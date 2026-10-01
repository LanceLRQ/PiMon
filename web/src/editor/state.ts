import type { Grid, Layout, LayoutScreen, LayoutWidget, WidgetSize } from '@/types/generated'
import { evaluatePlacement, outOfBoundsWidgets, sizeAllowed, type GridWidgetRect } from './grid-ops'
import { applyOptionsPatch, type OptionsPatch } from './options'

// 编辑器状态：保留服务端基线（base + baseVersion）与当前草稿（draft），
// 改动清单、单条撤销与保存都可以在这两份之上做 diff，不需要改这里的形状。

export type RejectReason = 'out_of_bounds' | 'overlap' | 'size_not_allowed' | 'no_space'

/** 最近一次被拒绝的操作，画布高亮冲突块并提示 */
export interface Rejection {
  reason: RejectReason
  widgetId?: string
  conflicts: string[]
}

export interface EditorState {
  baseVersion: number
  base: Layout
  draft: Layout
  screenId: string
  selectedId: string | null
  rejection: Rejection | null
}

export type EditorAction =
  | { type: 'load'; version: number; layout: Layout }
  | { type: 'selectScreen'; id: string }
  | { type: 'select'; id: string | null }
  | { type: 'clearRejection' }
  | { type: 'add'; widget: LayoutWidget }
  | { type: 'move'; id: string; col: number; row: number }
  | { type: 'resize'; id: string; size: WidgetSize; allowed: readonly WidgetSize[] }
  | { type: 'remove'; id: string }
  | { type: 'setOptions'; id: string; patch: OptionsPatch }
  | { type: 'setBinding'; id: string; binding: LayoutWidget['binding'] }
  | { type: 'setGrid'; grid: Grid }

const emptyLayout: Layout = { grid: { cols: 1, rows: 1 }, screens: [] }

export function initialEditorState(): EditorState {
  return { baseVersion: 0, base: emptyLayout, draft: emptyLayout, screenId: 'index', selectedId: null, rejection: null }
}

export function toRect(w: LayoutWidget): GridWidgetRect {
  return { id: w.id, col: w.col, row: w.row, w: w.size.cols, h: w.size.rows }
}

export function currentScreen(state: EditorState): LayoutScreen | undefined {
  return state.draft.screens.find((s) => s.id === state.screenId) ?? state.draft.screens[0]
}

function mapScreen(state: EditorState, fn: (s: LayoutScreen) => LayoutScreen): Layout {
  const id = currentScreen(state)?.id
  return { ...state.draft, screens: state.draft.screens.map((s) => (s.id === id ? fn(s) : s)) }
}

function mapWidget(state: EditorState, id: string, fn: (w: LayoutWidget) => LayoutWidget): Layout {
  return mapScreen(state, (s) => ({ ...s, widgets: s.widgets.map((w) => (w.id === id ? fn(w) : w)) }))
}

function reject(state: EditorState, r: Rejection): EditorState {
  return { ...state, rejection: r }
}

export function editorReducer(state: EditorState, action: EditorAction): EditorState {
  const screen = currentScreen(state)
  const widgets = screen?.widgets ?? []
  switch (action.type) {
    case 'load': {
      const keep = action.layout.screens.some((s) => s.id === state.screenId) ? state.screenId : (action.layout.screens[0]?.id ?? 'index')
      return { baseVersion: action.version, base: action.layout, draft: action.layout, screenId: keep, selectedId: null, rejection: null }
    }
    case 'selectScreen':
      return state.screenId === action.id ? state : { ...state, screenId: action.id, selectedId: null, rejection: null }
    case 'select':
      return { ...state, selectedId: action.id, rejection: null }
    case 'clearRejection':
      return state.rejection ? { ...state, rejection: null } : state
    case 'add': {
      const w = action.widget
      const placed = evaluatePlacement(state.draft.grid, widgets.map(toRect), toRect(w))
      if (!placed.ok) return reject(state, { reason: placed.reason!, widgetId: w.id, conflicts: placed.conflicts })
      return { ...state, draft: mapScreen(state, (s) => ({ ...s, widgets: [...s.widgets, w] })), selectedId: w.id, rejection: null }
    }
    case 'move': {
      const cur = widgets.find((w) => w.id === action.id)
      if (!cur) return state
      if (cur.col === action.col && cur.row === action.row) return { ...state, rejection: null }
      const cand = { ...toRect(cur), col: action.col, row: action.row }
      const placed = evaluatePlacement(state.draft.grid, widgets.map(toRect), cand)
      if (!placed.ok) return reject(state, { reason: placed.reason!, widgetId: cur.id, conflicts: placed.conflicts })
      return { ...state, draft: mapWidget(state, cur.id, (w) => ({ ...w, col: action.col, row: action.row })), rejection: null }
    }
    case 'resize': {
      const cur = widgets.find((w) => w.id === action.id)
      if (!cur) return state
      if (cur.size.cols === action.size.cols && cur.size.rows === action.size.rows) return state
      if (!sizeAllowed(action.allowed, action.size)) return reject(state, { reason: 'size_not_allowed', widgetId: cur.id, conflicts: [] })
      const cand = { ...toRect(cur), w: action.size.cols, h: action.size.rows }
      const placed = evaluatePlacement(state.draft.grid, widgets.map(toRect), cand)
      if (!placed.ok) return reject(state, { reason: placed.reason!, widgetId: cur.id, conflicts: placed.conflicts })
      return { ...state, draft: mapWidget(state, cur.id, (w) => ({ ...w, size: { ...action.size } })), rejection: null }
    }
    case 'remove': {
      if (!widgets.some((w) => w.id === action.id)) return state
      return {
        ...state,
        draft: mapScreen(state, (s) => ({ ...s, widgets: s.widgets.filter((w) => w.id !== action.id) })),
        selectedId: state.selectedId === action.id ? null : state.selectedId,
        rejection: null,
      }
    }
    case 'setOptions':
      if (!widgets.some((w) => w.id === action.id)) return state
      return { ...state, draft: mapWidget(state, action.id, (w) => ({ ...w, options: applyOptionsPatch(w.options ?? {}, action.patch) })) }
    case 'setBinding':
      if (!widgets.some((w) => w.id === action.id)) return state
      return { ...state, draft: mapWidget(state, action.id, (w) => ({ ...w, binding: action.binding })) }
    case 'setGrid': {
      // 缩小到有小组件越界时拒绝（越界处理对话框在保存流程里）；越界列表由 gridShrinkConflicts 单独提供
      const oob = gridShrinkConflicts(state.draft, action.grid)
      if (oob.length) return reject(state, { reason: 'out_of_bounds', conflicts: oob.map((o) => o.widgetId) })
      return { ...state, draft: { ...state.draft, grid: action.grid }, rejection: null }
    }
  }
}

export interface ShrinkConflict {
  screenId: string
  widgetId: string
}

/** 换成新网格后越界的小组件（跨全部 screen，网格是全局的） */
export function gridShrinkConflicts(layout: Layout, grid: Grid): ShrinkConflict[] {
  return layout.screens.flatMap((s) =>
    outOfBoundsWidgets(grid, s.widgets.map(toRect)).map((widgetId) => ({ screenId: s.id, widgetId })),
  )
}
