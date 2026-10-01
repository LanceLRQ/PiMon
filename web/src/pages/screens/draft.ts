import type { Layout, LayoutScreen } from '@/types/generated'
import { editorReducer, type EditorAction, type EditorState } from '@/editor/state'

// screens 管理页的草稿：沿用布局编辑器的状态（新增、重命名 screen 与改网格的规则只有编辑器那一份），
// 在其上增加管理页独有的操作：停留秒数、是否参与轮播、删除、排序。

export type ScreenAction =
  | EditorAction
  | { type: 'setDwell'; id: string; seconds: number }
  | { type: 'setRotation'; id: string; on: boolean }
  | { type: 'removeScreen'; id: string }
  | { type: 'moveScreen'; id: string; to: number }

const INDEX_ID = 'index'

function mapScreen(draft: Layout, id: string, fn: (s: LayoutScreen) => LayoutScreen): Layout {
  return { ...draft, screens: draft.screens.map((s) => (s.id === id ? fn(s) : s)) }
}

/** 目标位置：index 固定在最前，其他 screen 不能排到它前面 */
function clampTarget(screens: readonly LayoutScreen[], to: number): number {
  const floor = screens[0]?.id === INDEX_ID ? 1 : 0
  return Math.min(Math.max(to, floor), screens.length - 1)
}

export function screenReducer(state: EditorState, action: ScreenAction): EditorState {
  switch (action.type) {
    case 'setDwell': {
      const cur = state.draft.screens.find((s) => s.id === action.id)
      if (!cur || cur.dwell_seconds === action.seconds) return state
      return { ...state, draft: mapScreen(state.draft, action.id, (s) => ({ ...s, dwell_seconds: action.seconds })) }
    }
    case 'setRotation': {
      const cur = state.draft.screens.find((s) => s.id === action.id)
      // 首页始终参与轮播
      if (!cur || action.id === INDEX_ID || cur.in_rotation === action.on) return state
      return { ...state, draft: mapScreen(state.draft, action.id, (s) => ({ ...s, in_rotation: action.on })) }
    }
    case 'removeScreen': {
      if (action.id === INDEX_ID || !state.draft.screens.some((s) => s.id === action.id)) return state
      return { ...state, draft: { ...state.draft, screens: state.draft.screens.filter((s) => s.id !== action.id) } }
    }
    case 'moveScreen': {
      const from = state.draft.screens.findIndex((s) => s.id === action.id)
      if (from < 0 || action.id === INDEX_ID) return state
      const to = clampTarget(state.draft.screens, action.to)
      if (to === from) return state
      const screens = [...state.draft.screens]
      const [moved] = screens.splice(from, 1)
      screens.splice(to, 0, moved)
      return { ...state, draft: { ...state.draft, screens } }
    }
    default:
      return editorReducer(state, action)
  }
}

export type ScreenChangeKind = 'grid' | 'add' | 'rename' | 'dwell' | 'rotation' | 'remove' | 'order'

export interface ScreenChange {
  key: string
  kind: ScreenChangeKind
  screenId?: string
  from?: string
  to?: string
}

const dwellText = (s: number) => (s === 0 ? '' : `${s}`)

/** 草稿相对基线的改动（网格与 screen 级属性、增删、顺序），小组件级改动归布局编辑器 */
export function screenChanges(base: Layout, draft: Layout): ScreenChange[] {
  const out: ScreenChange[] = []
  if (base.grid.cols !== draft.grid.cols || base.grid.rows !== draft.grid.rows) {
    out.push({ key: 'grid', kind: 'grid', from: `${base.grid.cols}×${base.grid.rows}`, to: `${draft.grid.cols}×${draft.grid.rows}` })
  }
  const was = new Map(base.screens.map((s) => [s.id, s]))
  for (const s of draft.screens) {
    const old = was.get(s.id)
    if (!old) {
      out.push({ key: `add:${s.id}`, kind: 'add', screenId: s.id, to: s.name })
      continue
    }
    if (old.name !== s.name) out.push({ key: `rename:${s.id}`, kind: 'rename', screenId: s.id, from: old.name, to: s.name })
    if (old.dwell_seconds !== s.dwell_seconds) {
      out.push({ key: `dwell:${s.id}`, kind: 'dwell', screenId: s.id, from: dwellText(old.dwell_seconds), to: dwellText(s.dwell_seconds) })
    }
    if (old.in_rotation !== s.in_rotation) {
      out.push({ key: `rotation:${s.id}`, kind: 'rotation', screenId: s.id, to: s.in_rotation ? 'on' : 'off' })
    }
  }
  const now = new Set(draft.screens.map((s) => s.id))
  for (const s of base.screens) {
    if (!now.has(s.id)) out.push({ key: `remove:${s.id}`, kind: 'remove', screenId: s.id, from: s.name })
  }
  if (orderChanged(base, draft)) out.push({ key: 'order', kind: 'order' })
  return out
}

/** 前后都有的 screen 之间的先后顺序是否变了（与后端版本摘要的 reordered 同口径） */
export function orderChanged(base: Layout, draft: Layout): boolean {
  const inBase = new Set(base.screens.map((s) => s.id))
  const inDraft = new Set(draft.screens.map((s) => s.id))
  const a = base.screens.filter((s) => inDraft.has(s.id)).map((s) => s.id)
  const b = draft.screens.filter((s) => inBase.has(s.id)).map((s) => s.id)
  return a.length !== b.length || a.some((id, i) => id !== b[i])
}
