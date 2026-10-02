import { describe, expect, it } from 'vitest'
import type { Layout, LayoutWidget } from '@/types/generated'
import { diffLayouts, undoChange, widgetLabel } from './changes'

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
const withWidgets = (l: Layout, widgets: LayoutWidget[]): Layout => ({ ...l, screens: [{ ...l.screens[0], widgets }, l.screens[1]] })
const ws = (l: Layout) => l.screens[0].widgets

describe('改动清单 diff', () => {
  it('草稿与基线相同则没有改动', () => {
    const base = layoutOf([widget('a', 0, 0, 2, 2, { options: { title: 'x' } })])
    expect(diffLayouts(base, structuredClone(base))).toEqual([])
  })

  it('覆盖网格、新增、删除、移动、尺寸、显示选项各类改动', () => {
    const base = layoutOf([widget('a', 0, 0), widget('b', 2, 0), widget('c', 4, 0), widget('d', 0, 2)])
    const draft = layoutOf(
      [
        widget('a', 1, 1, 2, 1),
        widget('c', 4, 0, 1, 1, { options: { title: '新标题' } }),
        widget('d', 0, 2, 1, 1, { binding: { instance_id: 'i1' } }),
        widget('n', 5, 3),
      ],
      { cols: 8, rows: 5 },
    )
    const got = diffLayouts(base, draft).map((c) => [c.key, c.from, c.to, c.detail])
    expect(got).toEqual([
      ['grid', '6×4', '8×5', undefined],
      ['move:a', 'c1 r1', 'c2 r2', undefined],
      ['resize:a', '1×1', '2×1', undefined],
      ['config:c', undefined, undefined, 'title'],
      ['config:d', undefined, undefined, 'binding'],
      ['add:n', undefined, 'c6 r4 · 1×1', undefined],
      ['remove:b', 'c3 r1 · 1×1', undefined, undefined],
    ])
  })

  it('选项键顺序不同、options 缺省与空对象视为相同', () => {
    const base = layoutOf([widget('a', 0, 0, 1, 1, { options: { title: 't', icon: 'cpu' } })])
    const draft = layoutOf([widget('a', 0, 0, 1, 1, { options: { icon: 'cpu', title: 't' } })])
    expect(diffLayouts(base, draft)).toEqual([])
    const noOpts = widget('a', 0, 0)
    delete (noOpts as { options?: unknown }).options
    expect(diffLayouts(layoutOf([widget('a', 0, 0)]), layoutOf([noOpts]))).toEqual([])
  })

  it('小组件显示名：标题优先，其次插件小组件 id、模板、id', () => {
    expect(widgetLabel(widget('a', 0, 0, 1, 1, { options: { title: 'T' } }))).toBe('T')
    expect(widgetLabel(widget('a', 0, 0, 1, 1, { widget_id: 'cpu' }))).toBe('cpu')
    expect(widgetLabel(widget('a', 0, 0))).toBe('value')
    expect(widgetLabel(widget('a', 0, 0, 1, 1, { template: undefined }))).toBe('a')
  })
})

describe('单条撤销', () => {
  const find = (base: Layout, draft: Layout, key: string) => diffLayouts(base, draft).find((c) => c.key === key)!

  it('撤销移动：位置真正回到基线，清单项消失', () => {
    const base = layoutOf([widget('a', 0, 0, 2, 2)])
    const draft = withWidgets(base, [widget('a', 3, 1, 2, 2)])
    const r = undoChange(base, draft, find(base, draft, 'move:a'))
    expect(r.ok && ws(r.draft)[0]).toMatchObject({ col: 0, row: 0 })
    expect(r.ok && diffLayouts(base, r.draft)).toEqual([])
  })

  it('撤销移动：原位置已被别的小组件占用则拒绝，列出冲突方，草稿不变', () => {
    const base = layoutOf([widget('a', 0, 0, 2, 2)])
    const draft = withWidgets(base, [widget('a', 3, 1, 2, 2), widget('n', 1, 1)])
    const r = undoChange(base, draft, find(base, draft, 'move:a'))
    expect(r).toEqual({ ok: false, reason: 'overlap', conflicts: ['n'], screenId: 'index' })
  })

  it('撤销尺寸：先碰撞检测，撞车拒绝', () => {
    const base = layoutOf([widget('a', 0, 0, 2, 2)])
    const bigger = withWidgets(base, [widget('a', 0, 0, 2, 2)])
    expect(diffLayouts(base, bigger)).toEqual([])
    const draftShrunk = withWidgets(base, [widget('a', 0, 0, 1, 1), widget('n', 1, 0)])
    const r = undoChange(base, draftShrunk, find(base, draftShrunk, 'resize:a'))
    expect(r).toMatchObject({ ok: false, reason: 'overlap', conflicts: ['n'] })
    const free = withWidgets(base, [widget('a', 0, 0, 1, 1)])
    const ok = undoChange(base, free, find(base, free, 'resize:a'))
    expect(ok.ok && ws(ok.draft)[0].size).toEqual({ cols: 2, rows: 2 })
  })

  it('撤销移动 + 又被改过尺寸：两条互相独立，各自恢复', () => {
    const base = layoutOf([widget('a', 0, 0, 1, 1)])
    const draft = withWidgets(base, [widget('a', 4, 2, 2, 1)])
    const r1 = undoChange(base, draft, find(base, draft, 'move:a'))
    expect(r1.ok && ws(r1.draft)[0]).toMatchObject({ col: 0, row: 0, size: { cols: 2, rows: 1 } })
  })

  it('撤销删除：放回原位；原位已被占则拒绝', () => {
    const base = layoutOf([widget('a', 0, 0, 2, 1), widget('b', 3, 0)])
    const draft = withWidgets(base, [widget('b', 3, 0)])
    const ok = undoChange(base, draft, find(base, draft, 'remove:a'))
    expect(ok.ok && ws(ok.draft).map((w) => w.id).sort()).toEqual(['a', 'b'])
    const blocked = withWidgets(base, [widget('b', 3, 0), widget('n', 1, 0)])
    expect(undoChange(base, blocked, find(base, blocked, 'remove:a'))).toMatchObject({ ok: false, reason: 'overlap', conflicts: ['n'] })
  })

  it('撤销新增：移除该小组件', () => {
    const base = layoutOf([])
    const draft = withWidgets(base, [widget('n', 0, 0)])
    const r = undoChange(base, draft, find(base, draft, 'add:n'))
    expect(r.ok && ws(r.draft)).toEqual([])
  })

  it('撤销显示选项与绑定改动：整份恢复成基线', () => {
    const base = layoutOf([widget('a', 0, 0, 1, 1, { options: { title: '旧' }, binding: { instance_id: 'i1' } })])
    const draft = withWidgets(base, [widget('a', 0, 0, 1, 1, { options: { title: '新', icon: 'cpu' }, binding: { instance_id: 'i2' } })])
    const r = undoChange(base, draft, find(base, draft, 'config:a'))
    expect(r.ok && diffLayouts(base, r.draft)).toEqual([])
  })

  it('撤销网格：放大回基线时无事；基线网格放不下当前小组件时拒绝并列出越界者', () => {
    const base = layoutOf([widget('a', 0, 0)])
    const grown = { ...withWidgets(base, [widget('a', 0, 0)]), grid: { cols: 8, rows: 5 } }
    const back = undoChange(base, grown, find(base, grown, 'grid'))
    expect(back.ok && back.draft.grid).toEqual({ cols: 6, rows: 4 })
    const moved = { ...withWidgets(base, [widget('a', 0, 0), widget('far', 7, 4)]), grid: { cols: 8, rows: 5 } }
    expect(undoChange(base, moved, find(base, moved, 'grid'))).toEqual({ ok: false, reason: 'out_of_bounds', conflicts: ['far'], screenId: 'index' })
  })
})
