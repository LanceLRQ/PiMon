import type { Grid, Layout, LayoutScreen, LayoutWidget, WidgetSize } from '@/types/generated'
import { evaluatePlacement, outOfBoundsWidgets, sizeAllowed, type GridWidgetRect } from './grid-ops'
import { diffLayouts, undoChange, type Change } from './changes'
import { MAX_SCREENS, SCREEN_ID_PATTERN, normalizeScreenName } from './screen-rules'
import { applyOptionsPatch, type OptionsPatch } from './options'

// 编辑器状态：保留服务端基线（base + baseVersion）与当前草稿（draft），
// 改动清单、单条撤销与保存都可以在这两份之上做 diff，不需要改这里的形状。

export type RejectReason = 'out_of_bounds' | 'overlap' | 'size_not_allowed' | 'no_space'

/** 最近一次被拒绝的操作，画布高亮冲突块并提示 */
export interface Rejection {
  reason: RejectReason
  widgetId?: string
  conflicts: string[]
  /** 单条撤销被拒绝时为 true，提示文案不同 */
  undo?: boolean
}

export interface EditorState {
  baseVersion: number
  base: Layout
  draft: Layout
  screenId: string
  selectedId: string | null
  rejection: Rejection | null
  /** 改动清单键，按最近一次被改动的先后排列；末尾是最近一条 */
  order: string[]
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
  | { type: 'setGrid'; grid: Grid; removeOutOfBounds?: boolean }
  | { type: 'addScreen'; id: string; name: string }
  | { type: 'renameScreen'; id: string; name: string }
  | { type: 'undoChange'; key: string }
  | { type: 'undoLast' }
  | { type: 'discard' }

const emptyLayout: Layout = { grid: { cols: 1, rows: 1 }, screens: [] }

export function initialEditorState(): EditorState {
  return { baseVersion: 0, base: emptyLayout, draft: emptyLayout, screenId: 'index', selectedId: null, rejection: null, order: [] }
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

function innerReducer(state: EditorState, action: EditorAction): EditorState {
  const screen = currentScreen(state)
  const widgets = screen?.widgets ?? []
  switch (action.type) {
    case 'load': {
      const keep = action.layout.screens.some((s) => s.id === state.screenId) ? state.screenId : (action.layout.screens[0]?.id ?? 'index')
      return { baseVersion: action.version, base: action.layout, draft: action.layout, screenId: keep, selectedId: null, rejection: null, order: [] }
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
      // 缩小到有小组件越界时拒绝；用户在越界对话框里确认后带 removeOutOfBounds 再来，连同越界的小组件一起移除
      const oob = gridShrinkConflicts(state.draft, action.grid)
      if (oob.length && !action.removeOutOfBounds) return reject(state, { reason: 'out_of_bounds', conflicts: oob.map((o) => o.widgetId) })
      const gone = new Set(oob.map((o) => o.widgetId))
      const screens = gone.size ? state.draft.screens.map((s) => ({ ...s, widgets: s.widgets.filter((w) => !gone.has(w.id)) })) : state.draft.screens
      return {
        ...state,
        draft: { ...state.draft, grid: action.grid, screens },
        selectedId: state.selectedId && gone.has(state.selectedId) ? null : state.selectedId,
        rejection: null,
      }
    }
    case 'addScreen': {
      const name = normalizeScreenName(action.name)
      const ids = state.draft.screens.map((s) => s.id)
      if (!name || !SCREEN_ID_PATTERN.test(action.id) || ids.includes(action.id) || ids.length >= MAX_SCREENS) return state
      const screen: LayoutScreen = { id: action.id, name, dwell_seconds: 0, in_rotation: true, widgets: [] }
      return { ...state, draft: { ...state.draft, screens: [...state.draft.screens, screen] }, screenId: action.id, selectedId: null, rejection: null }
    }
    case 'renameScreen': {
      const name = normalizeScreenName(action.name)
      const cur = state.draft.screens.find((s) => s.id === action.id)
      if (!name || !cur || cur.name === name) return state
      return { ...state, draft: { ...state.draft, screens: state.draft.screens.map((s) => (s.id === action.id ? { ...s, name } : s)) } }
    }
    case 'undoChange':
      return applyUndo(state, diffLayouts(state.base, state.draft).find((c) => c.key === action.key))
    case 'undoLast':
      return applyUndo(state, changeList(state).at(-1))
    case 'discard':
      return { ...state, draft: state.base, selectedId: null, rejection: null, order: [] }
  }
}

function applyUndo(state: EditorState, change: Change | undefined): EditorState {
  if (!change) return state
  const r = undoChange(state.base, state.draft, change)
  if (!r.ok) {
    return {
      ...state,
      screenId: r.screenId ?? change.screenId ?? state.screenId,
      selectedId: null,
      rejection: { reason: r.reason, widgetId: change.widgetId, conflicts: r.conflicts, undo: true },
    }
  }
  const stillThere = change.widgetId && r.draft.screens.some((s) => s.widgets.some((w) => w.id === change.widgetId))
  return {
    ...state,
    draft: r.draft,
    screenId: [change.screenId, state.screenId, r.draft.screens[0]?.id].find((id) => id && r.draft.screens.some((s) => s.id === id)) ?? 'index',
    selectedId: stillThere ? change.widgetId! : null,
    rejection: null,
  }
}

/** 未保存的改动，按最近被改动的先后排列（末尾是最近一条） */
export function changeList(state: EditorState): Change[] {
  const rank = new Map(state.order.map((k, i) => [k, i]))
  return diffLayouts(state.base, state.draft).sort((a, b) => (rank.get(a.key) ?? -1) - (rank.get(b.key) ?? -1))
}

// 记录每条改动最近被动过的先后：草稿变了，就把签名发生变化（含新出现）的项挪到末尾
function touch(prev: EditorState, next: EditorState): string[] {
  const before = new Map(diffLayouts(next.base, prev.draft).map((c) => [c.key, c.sig]))
  const now = diffLayouts(next.base, next.draft)
  // 网格改动排在同批其他改动之后：缩小网格连带删除的小组件，要先撤网格才放得回去，顶栏撤销才不会卡住
  const touched = now.filter((c) => before.get(c.key) !== c.sig).sort((a, b) => Number(a.kind === 'grid') - Number(b.kind === 'grid')).map((c) => c.key)
  const alive = new Set(now.map((c) => c.key))
  return [...prev.order.filter((k) => alive.has(k) && !touched.includes(k)), ...touched]
}

export function editorReducer(state: EditorState, action: EditorAction): EditorState {
  const next = innerReducer(state, action)
  if (next.draft === state.draft || next.base !== state.base) return next
  return { ...next, order: touch(state, next) }
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
