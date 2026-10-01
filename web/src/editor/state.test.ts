import { describe, expect, it } from 'vitest'
import type { Layout, LayoutWidget } from '@/types/generated'
import { editorReducer, gridShrinkConflicts, initialEditorState, currentScreen, type EditorState } from './state'

const widget = (id: string, col: number, row: number, cols = 1, rows = 1, over: Partial<LayoutWidget> = {}): LayoutWidget => ({
  id, source: 'generic', template: 'value', size: { cols, rows }, col, row, binding: {}, options: {}, ...over,
})
const layoutOf = (widgets: LayoutWidget[], grid = { cols: 6, rows: 4 }): Layout => ({
  grid,
  screens: [
    { id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets },
    { id: 's1', name: '二', dwell_seconds: 0, in_rotation: true, widgets: [] },
  ],
})
const load = (l: Layout, version = 3): EditorState => editorReducer(initialEditorState(), { type: 'load', version, layout: l })
const ws = (s: EditorState) => currentScreen(s)!.widgets

describe('编辑器状态', () => {
  it('载入后保留基线与草稿，版本号记为 base', () => {
    const s = load(layoutOf([widget('a', 0, 0)]))
    expect(s.baseVersion).toBe(3)
    expect(s.draft).toBe(s.base)
  })

  it('移动：可放则改草稿且基线不变，撞车或越界则拒绝并报告冲突', () => {
    let s = load(layoutOf([widget('a', 0, 0, 2, 2), widget('b', 4, 0)]))
    s = editorReducer(s, { type: 'move', id: 'a', col: 1, row: 1 })
    expect(ws(s)[0]).toMatchObject({ col: 1, row: 1 })
    expect(s.base.screens[0].widgets[0]).toMatchObject({ col: 0, row: 0 })
    const hit = editorReducer(s, { type: 'move', id: 'a', col: 3, row: 0 })
    expect(hit.rejection).toEqual({ reason: 'overlap', widgetId: 'a', conflicts: ['b'] })
    expect(ws(hit)[0]).toMatchObject({ col: 1, row: 1 })
    const oob = editorReducer(s, { type: 'move', id: 'a', col: 5, row: 0 })
    expect(oob.rejection?.reason).toBe('out_of_bounds')
  })

  it('尺寸切换只允许白名单内的尺寸，且不得撞车', () => {
    const allowed = [{ cols: 1, rows: 1 }, { cols: 2, rows: 1 }]
    let s = load(layoutOf([widget('a', 0, 0), widget('b', 2, 0)]))
    expect(editorReducer(s, { type: 'resize', id: 'a', size: { cols: 3, rows: 1 }, allowed }).rejection?.reason).toBe('size_not_allowed')
    s = editorReducer(s, { type: 'resize', id: 'a', size: { cols: 2, rows: 1 }, allowed })
    expect(ws(s)[0].size).toEqual({ cols: 2, rows: 1 })
    const s2 = load(layoutOf([widget('a', 0, 0), widget('b', 1, 0)]))
    expect(editorReducer(s2, { type: 'resize', id: 'a', size: { cols: 2, rows: 1 }, allowed }).rejection).toMatchObject({ reason: 'overlap', conflicts: ['b'] })
  })

  it('添加：选中新小组件；位置被占则拒绝', () => {
    let s = load(layoutOf([widget('a', 0, 0)]))
    s = editorReducer(s, { type: 'add', widget: widget('n', 1, 0) })
    expect(s.selectedId).toBe('n')
    expect(ws(s)).toHaveLength(2)
    expect(editorReducer(s, { type: 'add', widget: widget('m', 1, 0) }).rejection?.reason).toBe('overlap')
  })

  it('删除并取消选中；切换 screen 取消选中', () => {
    let s = load(layoutOf([widget('a', 0, 0)]))
    s = editorReducer(s, { type: 'select', id: 'a' })
    s = editorReducer(s, { type: 'remove', id: 'a' })
    expect(ws(s)).toHaveLength(0)
    expect(s.selectedId).toBeNull()
    s = editorReducer(editorReducer(s, { type: 'select', id: 'x' }), { type: 'selectScreen', id: 's1' })
    expect(s.screenId).toBe('s1')
    expect(s.selectedId).toBeNull()
  })

  it('清空标题删除 title 键而不是写 null', () => {
    let s = load(layoutOf([widget('a', 0, 0, 1, 1, { options: { title: '旧', icon: 'cpu' } })]))
    s = editorReducer(s, { type: 'setOptions', id: 'a', patch: { title: '' } })
    expect(ws(s)[0].options).toEqual({ icon: 'cpu' })
    expect('title' in ws(s)[0].options).toBe(false)
    s = editorReducer(s, { type: 'setOptions', id: 'a', patch: { icon: null, title: undefined } })
    expect(ws(s)[0].options).toEqual({})
    expect(JSON.stringify(ws(s)[0].options)).not.toContain('null')
  })

  it('缩小网格：有越界时拒绝并列出，无越界时生效', () => {
    const s = load(layoutOf([widget('a', 0, 0), widget('far', 5, 3)]))
    expect(gridShrinkConflicts(s.draft, { cols: 4, rows: 3 })).toEqual([{ screenId: 'index', widgetId: 'far' }])
    const rej = editorReducer(s, { type: 'setGrid', grid: { cols: 4, rows: 3 } })
    expect(rej.draft.grid).toEqual({ cols: 6, rows: 4 })
    expect(rej.rejection).toEqual({ reason: 'out_of_bounds', conflicts: ['far'] })
    const ok = editorReducer(load(layoutOf([widget('a', 0, 0)])), { type: 'setGrid', grid: { cols: 4, rows: 3 } })
    expect(ok.draft.grid).toEqual({ cols: 4, rows: 3 })
  })
})
