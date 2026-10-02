import { describe, expect, it } from 'vitest'
import { editorReducer, initialEditorState, type EditorState } from '@/editor/state'
import type { Layout, LayoutScreen } from '@/types/generated'
import { orderChanged, screenChanges, screenReducer } from './draft'

const sc = (id: string, over: Partial<LayoutScreen> = {}): LayoutScreen => ({ id, name: id, dwell_seconds: 0, in_rotation: true, widgets: [], ...over })
const layout = (): Layout => ({ grid: { cols: 8, rows: 5 }, screens: [sc('index'), sc('a'), sc('b'), sc('c')] })
const widget = (id: string, col: number, row: number) => ({ id, source: 'generic', template: 'value', size: { cols: 1, rows: 1 }, col, row, binding: {}, options: {} })

function loaded(l: Layout = layout()): EditorState {
  return editorReducer(initialEditorState(), { type: 'load', version: 3, layout: l })
}
const ids = (s: EditorState) => s.draft.screens.map((x) => x.id)

describe('screens 管理页草稿', () => {
  it('index 不能被移动、删除或取消轮播', () => {
    const s = loaded()
    expect(screenReducer(s, { type: 'moveScreen', id: 'index', to: 2 })).toBe(s)
    expect(screenReducer(s, { type: 'removeScreen', id: 'index' })).toBe(s)
    expect(screenReducer(s, { type: 'setRotation', id: 'index', on: false })).toBe(s)
  })

  it('其他 screen 排序时不会排到 index 前面，越界按边界处理', () => {
    let s = loaded()
    s = screenReducer(s, { type: 'moveScreen', id: 'c', to: 0 })
    expect(ids(s)).toEqual(['index', 'c', 'a', 'b'])
    s = screenReducer(s, { type: 'moveScreen', id: 'c', to: 99 })
    expect(ids(s)).toEqual(['index', 'a', 'b', 'c'])
  })

  it('设置停留秒数与轮播开关，未变化时返回原状态', () => {
    let s = loaded()
    s = screenReducer(s, { type: 'setDwell', id: 'a', seconds: 20 })
    s = screenReducer(s, { type: 'setRotation', id: 'b', on: false })
    expect(s.draft.screens.find((x) => x.id === 'a')?.dwell_seconds).toBe(20)
    expect(s.draft.screens.find((x) => x.id === 'b')?.in_rotation).toBe(false)
    expect(screenReducer(s, { type: 'setDwell', id: 'a', seconds: 20 })).toBe(s)
  })

  it('删除 screen 连同它的小组件一起去掉', () => {
    const l = layout()
    l.screens[1].widgets = [widget('w1', 0, 0)]
    const s = screenReducer(loaded(l), { type: 'removeScreen', id: 'a' })
    expect(ids(s)).toEqual(['index', 'b', 'c'])
  })

  it('新增与重命名沿用编辑器的校验：超长名称、非法 id 与重复 id 被拒绝', () => {
    const s = loaded()
    expect(screenReducer(s, { type: 'addScreen', id: 'bad id', name: 'x' })).toBe(s)
    expect(screenReducer(s, { type: 'addScreen', id: 'a', name: 'x' })).toBe(s)
    expect(screenReducer(s, { type: 'addScreen', id: 'n1', name: 'x'.repeat(65) })).toBe(s)
    expect(ids(screenReducer(s, { type: 'addScreen', id: 'n1', name: '新屏' }))).toEqual(['index', 'a', 'b', 'c', 'n1'])
  })

  it('screen 数到达上限 32 后不能再新增', () => {
    const l: Layout = { grid: { cols: 8, rows: 5 }, screens: Array.from({ length: 32 }, (_, i) => sc(i === 0 ? 'index' : `s${i}`)) }
    const s = loaded(l)
    expect(screenReducer(s, { type: 'addScreen', id: 'extra', name: 'x' })).toBe(s)
  })

  it('缩小网格带上越界移除后，小组件从各 screen 去掉', () => {
    const l = layout()
    l.screens[0].widgets = [widget('near', 0, 0), widget('far', 7, 4)]
    const s = screenReducer(loaded(l), { type: 'setGrid', grid: { cols: 6, rows: 4 }, removeOutOfBounds: true })
    expect(s.draft.grid).toEqual({ cols: 6, rows: 4 })
    expect(s.draft.screens[0].widgets.map((w) => w.id)).toEqual(['near'])
  })
})

describe('screenChanges', () => {
  it('未改动没有清单；排序、停留、轮播、增删与网格各有一条', () => {
    const base = layout()
    expect(screenChanges(base, base)).toEqual([])
    let s = loaded(base)
    s = screenReducer(s, { type: 'moveScreen', id: 'c', to: 1 })
    s = screenReducer(s, { type: 'setDwell', id: 'a', seconds: 25 })
    s = screenReducer(s, { type: 'setRotation', id: 'b', on: false })
    s = screenReducer(s, { type: 'removeScreen', id: 'b' })
    s = screenReducer(s, { type: 'addScreen', id: 'n1', name: '新屏' })
    s = screenReducer(s, { type: 'setGrid', grid: { cols: 10, rows: 6 } })
    expect(screenChanges(base, s.draft).map((c) => c.key)).toEqual(['grid', 'dwell:a', 'add:n1', 'remove:b', 'order'])
  })

  it('停留恢复默认值 0 时 from 或 to 为空串', () => {
    const base = layout()
    base.screens[1].dwell_seconds = 40
    const s = screenReducer(loaded(base), { type: 'setDwell', id: 'a', seconds: 0 })
    expect(screenChanges(base, s.draft)).toEqual([{ key: 'dwell:a', kind: 'dwell', screenId: 'a', from: '40', to: '' }])
  })

  it('orderChanged 只比较共同的 screen', () => {
    const base = layout()
    const del: Layout = { ...base, screens: base.screens.filter((s) => s.id !== 'b') }
    expect(orderChanged(base, del)).toBe(false)
    expect(orderChanged(base, { ...base, screens: [base.screens[0], base.screens[2], base.screens[1], base.screens[3]] })).toBe(true)
  })
})
