import type { Grid, Layout, LayoutScreen, LayoutWidget } from '@/types/generated'
import { evaluatePlacement, outOfBoundsWidgets, type GridWidgetRect } from './grid-ops'

// 未保存改动清单：由基线（服务端版本）与草稿逐项比较得出，不单独存历史，
// 因此「撤销此条」就是把该项恢复成基线的值，并先做碰撞检测。

export type ChangeKind = 'grid' | 'add' | 'remove' | 'move' | 'resize' | 'config'

export interface Change {
  /** 稳定键：同一小组件同一类改动永远是同一个键 */
  key: string
  kind: ChangeKind
  screenId?: string
  widgetId?: string
  /** 小组件显示名（标题、插件小组件 id、模板或 id） */
  label: string
  /** move 为 "c5 r3"，resize、grid 为 "2×1"，与语言无关 */
  from?: string
  to?: string
  /** config：改动了哪些显示选项或绑定 */
  detail?: string
  /** 当前值的签名，用来判断这一项是否又被改过 */
  sig: string
}

function stable(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(stable).join(',')}]`
  if (v && typeof v === 'object') {
    const o = v as Record<string, unknown>
    return `{${Object.keys(o)
      .filter((k) => o[k] !== undefined)
      .sort()
      .map((k) => `${JSON.stringify(k)}:${stable(o[k])}`)
      .join(',')}}`
  }
  return JSON.stringify(v) ?? 'null'
}

export function widgetLabel(w: LayoutWidget): string {
  const title = w.options?.title
  return (typeof title === 'string' && title) || w.widget_id || w.template || w.id
}

const pos = (w: LayoutWidget) => `c${w.col + 1} r${w.row + 1}`
const size = (w: LayoutWidget) => `${w.size.cols}×${w.size.rows}`

function index(layout: Layout): Map<string, { screen: LayoutScreen; widget: LayoutWidget }> {
  const m = new Map<string, { screen: LayoutScreen; widget: LayoutWidget }>()
  for (const screen of layout.screens) for (const widget of screen.widgets) m.set(widget.id, { screen, widget })
  return m
}

function configKeys(a: LayoutWidget, b: LayoutWidget): string[] {
  const ao = a.options ?? {}
  const bo = b.options ?? {}
  const keys = [...new Set([...Object.keys(ao), ...Object.keys(bo)])].filter((k) => stable(ao[k]) !== stable(bo[k])).sort()
  if (stable(a.binding) !== stable(b.binding)) keys.push('binding')
  return keys
}

/** 草稿相对基线的全部改动（网格、小组件新增与删除、移动、尺寸、显示选项与绑定） */
export function diffLayouts(base: Layout, draft: Layout): Change[] {
  const out: Change[] = []
  if (base.grid.cols !== draft.grid.cols || base.grid.rows !== draft.grid.rows) {
    const to = `${draft.grid.cols}×${draft.grid.rows}`
    out.push({ key: 'grid', kind: 'grid', label: '', from: `${base.grid.cols}×${base.grid.rows}`, to, sig: to })
  }
  const was = index(base)
  const now = index(draft)
  for (const [id, { screen, widget }] of now) {
    const old = was.get(id)
    if (!old) {
      out.push({ key: `add:${id}`, kind: 'add', screenId: screen.id, widgetId: id, label: widgetLabel(widget), to: `${pos(widget)} · ${size(widget)}`, sig: stable(widget) })
      continue
    }
    const w = old.widget
    const label = widgetLabel(widget)
    if (w.col !== widget.col || w.row !== widget.row) {
      out.push({ key: `move:${id}`, kind: 'move', screenId: screen.id, widgetId: id, label, from: pos(w), to: pos(widget), sig: pos(widget) })
    }
    if (w.size.cols !== widget.size.cols || w.size.rows !== widget.size.rows) {
      out.push({ key: `resize:${id}`, kind: 'resize', screenId: screen.id, widgetId: id, label, from: size(w), to: size(widget), sig: size(widget) })
    }
    const keys = configKeys(w, widget)
    if (keys.length) {
      out.push({ key: `config:${id}`, kind: 'config', screenId: screen.id, widgetId: id, label, detail: keys.join(', '), sig: stable([widget.options ?? {}, widget.binding]) })
    }
  }
  for (const [id, { screen, widget }] of was) {
    if (!now.has(id)) {
      out.push({ key: `remove:${id}`, kind: 'remove', screenId: screen.id, widgetId: id, label: widgetLabel(widget), from: `${pos(widget)} · ${size(widget)}`, sig: 'removed' })
    }
  }
  return out
}

export type UndoOutcome =
  | { ok: true; draft: Layout }
  | { ok: false; reason: 'overlap' | 'out_of_bounds'; conflicts: string[]; screenId?: string }

const rectOf = (w: LayoutWidget): GridWidgetRect => ({ id: w.id, col: w.col, row: w.row, w: w.size.cols, h: w.size.rows })

function mapWidget(layout: Layout, id: string, fn: (w: LayoutWidget) => LayoutWidget): Layout {
  return { ...layout, screens: layout.screens.map((s) => ({ ...s, widgets: s.widgets.map((w) => (w.id === id ? fn(w) : w)) })) }
}

function check(grid: Grid, siblings: LayoutWidget[], cand: LayoutWidget, screenId?: string): UndoOutcome | null {
  const placed = evaluatePlacement(grid, siblings.filter((w) => w.id !== cand.id).map(rectOf), rectOf(cand))
  if (placed.ok) return null
  // 越界没有冲突对象时，高亮这个小组件自己（它若在画布上）
  const conflicts = placed.conflicts.length || !siblings.some((w) => w.id === cand.id) ? placed.conflicts : [cand.id]
  return { ok: false, reason: placed.reason === 'overlap' ? 'overlap' : 'out_of_bounds', conflicts, screenId }
}

/**
 * 撤销一条改动：把该项恢复成基线的值。恢复后若会与当前草稿冲突（重叠或越界），
 * 不修改草稿，返回冲突方，由调用方高亮并提示，绝不静默覆盖。
 */
export function undoChange(base: Layout, draft: Layout, change: Change): UndoOutcome {
  const id = change.widgetId
  switch (change.kind) {
    case 'grid': {
      const bad = draft.screens.flatMap((s) => outOfBoundsWidgets(base.grid, s.widgets.map(rectOf)).map((w) => ({ w, screenId: s.id })))
      if (bad.length) return { ok: false, reason: 'out_of_bounds', conflicts: bad.map((b) => b.w), screenId: bad[0].screenId }
      return { ok: true, draft: { ...draft, grid: { ...base.grid } } }
    }
    case 'add':
      return { ok: true, draft: { ...draft, screens: draft.screens.map((s) => ({ ...s, widgets: s.widgets.filter((w) => w.id !== id) })) } }
    case 'remove': {
      const old = index(base).get(id!)
      const target = draft.screens.find((s) => s.id === old?.screen.id)
      if (!old || !target) return { ok: false, reason: 'out_of_bounds', conflicts: [] }
      const bad = check(draft.grid, target.widgets, old.widget, target.id)
      if (bad) return bad
      return { ok: true, draft: { ...draft, screens: draft.screens.map((s) => (s.id === target.id ? { ...s, widgets: [...s.widgets, old.widget] } : s)) } }
    }
    case 'move':
    case 'resize': {
      const old = index(base).get(id!)?.widget
      const cur = index(draft).get(id!)
      if (!old || !cur) return { ok: true, draft }
      const restored: LayoutWidget =
        change.kind === 'move' ? { ...cur.widget, col: old.col, row: old.row } : { ...cur.widget, size: { ...old.size } }
      const bad = check(draft.grid, cur.screen.widgets, restored, cur.screen.id)
      if (bad) return bad
      return { ok: true, draft: mapWidget(draft, id!, () => restored) }
    }
    case 'config': {
      const old = index(base).get(id!)?.widget
      if (!old) return { ok: true, draft }
      return { ok: true, draft: mapWidget(draft, id!, (w) => ({ ...w, options: { ...(old.options ?? {}) }, binding: structuredClone(old.binding) })) }
    }
  }
}
