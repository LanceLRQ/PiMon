import { describe, expect, it } from 'vitest'
import type { Layout, LayoutWidget } from '@/types/generated'
import { changeList, editorReducer, gridShrinkConflicts, initialEditorState, currentScreen, type EditorState } from './state'

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

describe('改动清单与撤销', () => {
  it('清单按最近被改动的先后排列，再次改动同一项会挪到末尾', () => {
    let s = load(layoutOf([widget('a', 0, 0), widget('b', 3, 0)]))
    s = editorReducer(s, { type: 'move', id: 'a', col: 1, row: 1 })
    s = editorReducer(s, { type: 'move', id: 'b', col: 4, row: 1 })
    expect(changeList(s).map((c) => c.key)).toEqual(['move:a', 'move:b'])
    s = editorReducer(s, { type: 'move', id: 'a', col: 2, row: 1 })
    expect(changeList(s).map((c) => c.key)).toEqual(['move:b', 'move:a'])
  })

  it('顶栏撤销撤销最近一条，画布位置真正回退；撤空后与基线一致', () => {
    let s = load(layoutOf([widget('a', 0, 0), widget('b', 3, 0)]))
    s = editorReducer(s, { type: 'move', id: 'a', col: 1, row: 1 })
    s = editorReducer(s, { type: 'move', id: 'b', col: 4, row: 1 })
    s = editorReducer(s, { type: 'undoLast' })
    expect(ws(s).find((w) => w.id === 'b')).toMatchObject({ col: 3, row: 0 })
    expect(ws(s).find((w) => w.id === 'a')).toMatchObject({ col: 1, row: 1 })
    s = editorReducer(s, { type: 'undoLast' })
    expect(changeList(s)).toEqual([])
    expect(ws(s).map((w) => [w.col, w.row])).toEqual([[0, 0], [3, 0]])
  })

  it('撤销与当前草稿冲突时拒绝：草稿不变，rejection 标出冲突块并带 undo 标记', () => {
    let s = load(layoutOf([widget('a', 0, 0)]))
    s = editorReducer(s, { type: 'move', id: 'a', col: 3, row: 2 })
    s = editorReducer(s, { type: 'add', widget: widget('n', 0, 0) })
    const draft = s.draft
    s = editorReducer(s, { type: 'undoChange', key: 'move:a' })
    expect(s.draft).toBe(draft)
    expect(s.rejection).toEqual({ reason: 'overlap', widgetId: 'a', conflicts: ['n'], undo: true })
  })

  it('撤销其他 screen 上的改动时切到该 screen', () => {
    let s = load(layoutOf([widget('a', 0, 0)]))
    s = editorReducer(s, { type: 'move', id: 'a', col: 2, row: 2 })
    s = editorReducer(s, { type: 'selectScreen', id: 's1' })
    s = editorReducer(s, { type: 'undoChange', key: 'move:a' })
    expect(s.screenId).toBe('index')
    expect(s.selectedId).toBe('a')
  })

  it('放弃：草稿回到基线，清单清空', () => {
    let s = load(layoutOf([widget('a', 0, 0)]))
    s = editorReducer(s, { type: 'move', id: 'a', col: 2, row: 2 })
    s = editorReducer(s, { type: 'discard' })
    expect(s.draft).toBe(s.base)
    expect(changeList(s)).toEqual([])
  })

  it('越界确认后缩小：连同越界的小组件一起移除，且这些移除进入清单、可单条撤销（先撤网格再撤删除）', () => {
    let s = load(layoutOf([widget('a', 0, 0), widget('far', 5, 3)]))
    s = editorReducer(s, { type: 'setGrid', grid: { cols: 4, rows: 3 }, removeOutOfBounds: true })
    expect(s.draft.grid).toEqual({ cols: 4, rows: 3 })
    expect(ws(s).map((w) => w.id)).toEqual(['a'])
    expect(changeList(s).map((c) => c.key).sort()).toEqual(['grid', 'remove:far'])
    // 网格还小，放不回 far
    const blocked = editorReducer(s, { type: 'undoChange', key: 'remove:far' })
    expect(blocked.rejection).toMatchObject({ reason: 'out_of_bounds', undo: true })
    s = editorReducer(s, { type: 'undoChange', key: 'grid' })
    s = editorReducer(s, { type: 'undoChange', key: 'remove:far' })
    expect(changeList(s)).toEqual([])
  })
})

describe('缩小网格后的连续撤销', () => {
  it('越界确认缩小后，顶栏撤销（undoLast）连续两次回到 base，不会卡在被拒的删除上', () => {
    let s = load(layoutOf([widget('a', 0, 0), widget('far', 5, 3)]))
    s = editorReducer(s, { type: 'setGrid', grid: { cols: 4, rows: 3 }, removeOutOfBounds: true })
    s = editorReducer(s, { type: 'undoLast' })
    expect(s.rejection).toBeNull()
    expect(s.draft.grid).toEqual({ cols: 6, rows: 4 })
    s = editorReducer(s, { type: 'undoLast' })
    expect(s.rejection).toBeNull()
    expect(changeList(s)).toEqual([])
    expect(ws(s).map((w) => w.id).sort()).toEqual(['a', 'far'])
  })
})

describe('screen 新增与重命名', () => {
  it('新增 screen：选中新 screen，进入清单，撤销后消失', () => {
    let s = load(layoutOf([widget('a', 0, 0)]))
    s = editorReducer(s, { type: 'addScreen', id: 'screen1', name: '  第三屏 ' })
    expect(s.draft.screens.map((x) => x.id)).toEqual(['index', 's1', 'screen1'])
    expect(s.draft.screens[2]).toMatchObject({ name: '第三屏', in_rotation: true, dwell_seconds: 0, widgets: [] })
    expect(s.screenId).toBe('screen1')
    expect(changeList(s).map((c) => c.key)).toEqual(['screenAdd:screen1'])
    s = editorReducer(s, { type: 'undoLast' })
    expect(s.draft.screens.map((x) => x.id)).toEqual(['index', 's1'])
    expect(s.screenId).toBe('index')
    expect(changeList(s)).toEqual([])
  })

  it('新增校验对齐后端：id 重复或格式不对、名称为空或超 64 字、screen 数到 32 都不生效', () => {
    const s = load(layoutOf([]))
    expect(editorReducer(s, { type: 'addScreen', id: 's1', name: 'x' })).toBe(s)
    expect(editorReducer(s, { type: 'addScreen', id: '_bad', name: 'x' })).toBe(s)
    expect(editorReducer(s, { type: 'addScreen', id: 'n1', name: '   ' })).toBe(s)
    expect(editorReducer(s, { type: 'addScreen', id: 'n1', name: '字'.repeat(65) })).toBe(s)
    expect(editorReducer(s, { type: 'addScreen', id: 'n1', name: '字'.repeat(64) }).draft.screens).toHaveLength(3)
    let full = s
    for (let i = 0; i < 30; i++) full = editorReducer(full, { type: 'addScreen', id: `x${i}`, name: 'n' })
    expect(full.draft.screens).toHaveLength(32)
    expect(editorReducer(full, { type: 'addScreen', id: 'more', name: 'n' })).toBe(full)
  })

  it('重命名：进清单（前后名称），撤销恢复原名；空名与同名不生效', () => {
    let s = load(layoutOf([]))
    expect(editorReducer(s, { type: 'renameScreen', id: 's1', name: ' ' })).toBe(s)
    expect(editorReducer(s, { type: 'renameScreen', id: 's1', name: '二' })).toBe(s)
    s = editorReducer(s, { type: 'renameScreen', id: 's1', name: '主机' })
    expect(changeList(s)).toMatchObject([{ key: 'screenRename:s1', from: '二', to: '主机' }])
    s = editorReducer(s, { type: 'undoChange', key: 'screenRename:s1' })
    expect(s.draft.screens[1].name).toBe('二')
    expect(changeList(s)).toEqual([])
  })
})
