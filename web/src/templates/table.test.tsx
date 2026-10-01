import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetView } from './widget-view'
import { makeData, makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 2, rows: 2 },
  { cols: 4, rows: 2 },
  { cols: 4, rows: 3 },
  { cols: 6, rows: 1 },
]

const tasks: Item = {
  key: 'tasks',
  type: 'table',
  columns: ['任务', '状态', '耗时', '模型'],
  rows: [
    ['编译前端', '完成', 12, 'sonnet'],
    ['跑测试', '运行中', 3.5, 'opus'],
  ],
}

function tableWidget(size: { cols: number; rows: number }, slot = 'table', over = {}) {
  return makeWidget({ template: 'table', size, title: 'AI 任务', slots: { [slot]: [{ instance_id: 'i1', item: 'tasks' }] }, ...over })
}

describe.each(sizes)('table 模板 $cols x $rows', (size) => {
  it('显示表头与数据行，列数不超过小组件宽度', async () => {
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([tasks]) }} />)
    expect(container.querySelector('[data-template="table"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    const heads = container.querySelectorAll('.tpl-table__head')
    expect(heads.length).toBeGreaterThanOrEqual(2)
    expect(heads.length).toBeLessThanOrEqual(Math.max(2, size.cols))
    expect(heads[0].textContent).toBe('任务')
    expect(container.querySelectorAll('.tpl-table__row').length).toBeGreaterThanOrEqual(1)
    expect(container.querySelector('.tpl-table__cell')!.textContent).toBe('编译前端')
  })

  it('50 行数据多行截断并显示「+N」，不会渲染全部 50 行', async () => {
    const rows = Array.from({ length: 50 }, (_, i) => [`任务 ${i}`, '完成'])
    const big: Item = { key: 'tasks', type: 'table', columns: ['任务', '状态'], rows }
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([big]) }} />)
    const shown = container.querySelectorAll('.tpl-table__row').length
    expect(shown).toBeLessThan(50)
    const more = container.querySelector('.tpl-table__more')
    // 容量不足 2 行时按 Ruling 45 不显示孤立的「+N」，其余尺寸必须显示
    if (more) expect(more.textContent).toBe(`+${50 - shown}`)
    else expect(shown).toBe(1)
  })

  it('table 项缺失时显示「未知」', async () => {
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([]) }} />)
    expect(container.textContent).toContain('未知')
    expect(container.querySelector('.tpl-table__row')).toBeNull()
  })
})

describe('table 模板细节', () => {
  const size = { cols: 4, rows: 3 }

  it('generic 来源从 value 槽取表格', async () => {
    const { container } = await renderIn(<WidgetView widget={tableWidget(size, 'value')} data={{ i1: makeData([tasks]) }} />)
    expect(container.querySelectorAll('.tpl-table__row')).toHaveLength(2)
  })

  it('数字单元格等宽数字；空单元格显示破折号而不是 0', async () => {
    const item: Item = { key: 'tasks', type: 'table', columns: ['名称', '数量'], rows: [['a', 7], ['b', null]] }
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([item]) }} />)
    const cells = container.querySelectorAll('.tpl-table__row')
    expect(cells[0].querySelectorAll('.tpl-table__cell')[1].className).toContain('tabular-nums')
    expect(cells[1].querySelectorAll('.tpl-table__cell')[1].textContent).toBe('—')
  })

  it('超长单元格截断并保留 title', async () => {
    const long = '很长很长很长的内容'.repeat(10)
    const item: Item = { key: 'tasks', type: 'table', columns: ['名称', '备注'], rows: [['a', long]] }
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([item]) }} />)
    const cell = container.querySelectorAll('.tpl-table__cell')[1]
    expect(cell.className).toContain('truncate')
    expect(cell.getAttribute('title')).toBe(long)
  })

  it('没有列定义的表按行内元素个数推出列，行内长度不一致不报错', async () => {
    const item: Item = { key: 'tasks', type: 'table', rows: [['a', 'b', 'c'], ['d']] }
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([item]) }} />)
    expect(container.querySelectorAll('.tpl-table__row')).toHaveLength(2)
  })

  it('表格没有任何行时显示「未知」', async () => {
    const item: Item = { key: 'tasks', type: 'table', columns: ['a'], rows: [] }
    const { container } = await renderIn(<WidgetView widget={tableWidget(size)} data={{ i1: makeData([item]) }} />)
    expect(container.textContent).toContain('未知')
  })
})

describe('table 模板：通配引用（Ruling 48）', () => {
  it('通配引用展开后取第一个 table 成员；没有成员时显示空态', async () => {
    const items: Item[] = [{ key: 'tasks[a]', type: 'table', columns: ['名称', '状态'], rows: [['编译', '完成']] }]
    const widget = makeWidget({ template: 'table', size: { cols: 2, rows: 2 }, source: 'plugin', slots: { table: [{ instance_id: 'i1', item: 'tasks[*]' }] } })
    const a = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(items) }} />)
    expect(a.container.querySelector('.tpl-table__row')!.textContent).toContain('编译')
    a.unmount()
    const b = await renderIn(<WidgetView widget={widget} data={{ i1: makeData([]) }} />)
    expect(b.container.querySelector('.tpl-table__empty')!.textContent).toBe('暂无数据项')
  })
})
