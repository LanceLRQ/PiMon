import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetView } from './widget-view'
import { makeData, makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 2, rows: 2 },
  { cols: 4, rows: 2 },
  { cols: 4, rows: 3 },
  { cols: 6, rows: 2 },
]

const reach: Item[] = [
  { key: 'target[Google]', type: 'gauge', value: 100 },
  { key: 'latency[Google]', type: 'number', value: 123, unit: 'ms' },
  { key: 'target[GitHub]', type: 'gauge', value: 66.7 },
  { key: 'latency[GitHub]', type: 'number', value: 480, unit: 'ms' },
  { key: 'target[百度]', type: 'gauge', value: 0 },
]

function gridWidget(size: { cols: number; rows: number }, keys: string[], over = {}) {
  return makeWidget({
    template: 'status-grid',
    source: 'aggregate',
    size,
    title: '网络连通总览',
    slots: { items: keys.map((item) => ({ instance_id: 'i1', item })) },
    ...over,
  })
}

describe.each(sizes)('status-grid 模板 $cols x $rows', (size) => {
  it('每格显示名称与状态标记，target 取成功率级别，延迟作副文本', async () => {
    const keys = ['target[Google]', 'target[GitHub]', 'target[百度]']
    const { container } = await renderIn(<WidgetView widget={gridWidget(size, keys)} data={{ i1: makeData(reach) }} />)
    expect(container.querySelector('[data-template="status-grid"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    const cells = [...container.querySelectorAll('.tpl-grid__cell')]
    expect(cells.map((c) => c.getAttribute('data-value-level'))).toEqual(['ok', 'warning', 'critical'])
    expect(cells[0].querySelector('.tpl-grid__name')!.textContent).toBe('Google')
    expect(cells[0].querySelector('.tpl-grid__sub')!.textContent).toContain('123')
    expect(cells[0].querySelector('.tpl-grid__sub')!.className).toContain('tabular-nums')
    expect(cells[0].querySelector('.tpl-grid__marker')).not.toBeNull()
    // 没有配对延迟的目标副文本显示成功率
    expect(cells[2].querySelector('.tpl-grid__sub')!.textContent).toContain('0')
  })

  it('50 个目标放不下时最后一格显示「+N」', async () => {
    const many: Item[] = Array.from({ length: 50 }, (_, i) => ({ key: `host[h${i}]`, type: 'state', state: 'ok', text: 'ok' }))
    const { container } = await renderIn(<WidgetView widget={gridWidget(size, many.map((m) => m.key))} data={{ i1: makeData(many) }} />)
    const shown = container.querySelectorAll('.tpl-grid__cell').length
    expect(shown).toBeLessThan(50)
    expect(container.querySelector('.tpl-grid__more')!.textContent).toBe(`+${50 - shown}`)
  })

  it('数据项缺失的格子显示「未知」状态', async () => {
    const { container } = await renderIn(<WidgetView widget={gridWidget(size, ['target[nope]'])} data={{ i1: makeData([]) }} />)
    const cell = container.querySelector('.tpl-grid__cell')!
    expect(cell.getAttribute('data-value-level')).toBe('unknown')
    expect(cell.textContent).toContain('未知')
  })
})

describe('status-grid 模板细节', () => {
  const size = { cols: 4, rows: 2 }

  it('state 项用其文字（先查 plugin 文案）作副文本', async () => {
    const items: Item[] = [{ key: 'host[pi]', type: 'state', state: 'critical', text: 'CPU 97%' }]
    const { container } = await renderIn(<WidgetView widget={gridWidget(size, ['host[pi]'])} data={{ i1: makeData(items) }} />)
    const cell = container.querySelector('.tpl-grid__cell')!
    expect(cell.getAttribute('data-value-level')).toBe('critical')
    expect(cell.querySelector('.tpl-grid__sub')!.textContent).toBe('CPU 97%')
  })

  it('超长名称截断并保留 title', async () => {
    const long = 'a-very-long-monitor-target-name-'.repeat(4)
    const items: Item[] = [{ key: `host[${long}]`, type: 'state', state: 'ok' }]
    const { container } = await renderIn(<WidgetView widget={gridWidget(size, [`host[${long}]`])} data={{ i1: makeData(items) }} />)
    const name = container.querySelector('.tpl-grid__name')!
    expect(name.className).toContain('truncate')
    expect(name.getAttribute('title')).toBe(long)
  })

  it('跨实例引用各取各自实例的数据', async () => {
    const widget = makeWidget({
      template: 'status-grid', source: 'aggregate', size, slots: { items: [{ instance_id: 'a', item: 'target[x]' }, { instance_id: 'b', item: 'target[y]' }] },
    })
    const data = {
      a: makeData([{ key: 'target[x]', type: 'gauge', value: 100 }], { instance_id: 'a' }),
      b: makeData([{ key: 'target[y]', type: 'gauge', value: 0 }], { instance_id: 'b' }),
    }
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect([...container.querySelectorAll('.tpl-grid__cell')].map((c) => c.getAttribute('data-value-level'))).toEqual(['ok', 'critical'])
  })

  it('没有任何引用时显示「未知」', async () => {
    const widget = makeWidget({ template: 'status-grid', source: 'aggregate', size, slots: {} })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.textContent).toContain('未知')
  })
})
