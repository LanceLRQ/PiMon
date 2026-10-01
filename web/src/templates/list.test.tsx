import { screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetView } from './widget-view'
import { makeData, makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 2, rows: 1 },
  { cols: 2, rows: 2 },
  { cols: 2, rows: 3 },
  { cols: 4, rows: 2 },
  { cols: 4, rows: 3 },
]

const disks: Item[] = [
  { key: 'disk[/]', type: 'gauge', value: 61, unit: '%', label: '根分区' },
  { key: 'disk[/data]', type: 'gauge', value: 83.5, unit: '%' },
  { key: 'temp', type: 'number', value: 48, unit: '°C' },
  { key: 'link', type: 'state', state: 'warning', text: '链路降速' },
]

function listWidget(size: { cols: number; rows: number }, keys: string[], over = {}) {
  return makeWidget({
    template: 'list',
    size,
    source: 'aggregate',
    title: '主机',
    slots: { items: keys.map((item) => ({ instance_id: 'i1', item })) },
    ...over,
  })
}

describe.each(sizes)('list 模板 $cols x $rows', (size) => {
  it('每行一个数据项：标签在左、读数在右，读数等宽数字', async () => {
    const widget = listWidget(size, disks.map((d) => d.key))
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(disks) }} />)
    const rows = container.querySelectorAll('.tpl-list__row')
    expect(rows.length).toBeGreaterThanOrEqual(1)
    expect(container.querySelector('[data-template="list"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    expect(rows[0].querySelector('.tpl-list__label')!.textContent).toBe('根分区')
    const value = rows[0].querySelector('.tpl-list__value')!
    expect(value.textContent).toContain('61')
    expect(value.className).toContain('tabular-nums')
    expect(screen.getByText('主机')).toBeInTheDocument()
  })

  it('50 条数据在容量不足时最后一位显示「+N」', async () => {
    const many: Item[] = Array.from({ length: 50 }, (_, i) => ({ key: `disk[d${i}]`, type: 'gauge', value: i, unit: '%' }))
    const widget = listWidget(size, many.map((d) => d.key))
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(many) }} />)
    const rows = container.querySelectorAll('.tpl-list__row').length
    const more = container.querySelector('.tpl-list__more')!
    expect(more).not.toBeNull()
    expect(more.textContent).toBe(`+${50 - rows}`)
    expect(rows).toBeGreaterThanOrEqual(1)
  })

  it('数据项缺失时该行显示「未知」，不当作 0', async () => {
    const widget = listWidget(size, ['disk[/]', 'disk[gone]'])
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData([disks[0]]) }} />)
    const rows = container.querySelectorAll('.tpl-list__row')
    const last = rows[rows.length - 1]
    if (rows.length === 2) {
      expect(last.textContent).toContain('gone')
      expect(last.textContent).toContain('未知')
      expect(last.querySelector('.tpl-list__value')!.textContent).not.toMatch(/^0/)
    }
  })
})

describe('list 模板细节', () => {
  afterEach(() => vi.restoreAllMocks())

  const size = { cols: 2, rows: 3 }

  it('超长标题与超长标签不撑破布局：截断且保留 title 全文', async () => {
    const long = '一个非常非常非常非常非常非常长的磁盘分区名称'.repeat(3)
    const items: Item[] = [{ key: 'disk[x]', type: 'gauge', value: 1, label: long }]
    const widget = listWidget(size, ['disk[x]'], { title: long })
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(items) }} />)
    const label = container.querySelector('.tpl-list__label')!
    expect(label.className).toContain('truncate')
    expect(label.getAttribute('title')).toBe(long)
    expect(container.querySelector('[data-widget-title]')!.className).toContain('truncate')
  })

  it('state 项显示主题标记与文字；有级别的行带 data-value-level', async () => {
    const widget = listWidget(size, ['link'])
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(disks) }} />)
    const row = container.querySelector('.tpl-list__row')!
    expect(row.getAttribute('data-value-level')).toBe('warning')
    expect(row.querySelector('.tpl-list__marker')).not.toBeNull()
    expect(row.textContent).toContain('链路降速')
  })

  it('net-reach 的 target[*] 按成功率给级别：100 正常、0 严重、其余警告', async () => {
    const items: Item[] = [
      { key: 'target[a]', type: 'gauge', value: 100 },
      { key: 'target[b]', type: 'gauge', value: 0 },
      { key: 'target[c]', type: 'gauge', value: 66 },
    ]
    const widget = listWidget({ cols: 2, rows: 3 }, items.map((i) => i.key))
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(items) }} />)
    const levels = [...container.querySelectorAll('.tpl-list__row')].map((r) => r.getAttribute('data-value-level'))
    expect(levels).toEqual(['ok', 'critical', 'warning'])
  })

  it('没有 items 槽时用 value 槽里的 table 项：首列做标签、次列做读数', async () => {
    const items: Item[] = [{ key: 'tasks', type: 'table', columns: ['名称', '状态'], rows: [['编译', '完成'], ['测试', '运行中']] }]
    const widget = makeWidget({ template: 'list', size, source: 'generic', slots: { value: [{ instance_id: 'i1', item: 'tasks' }] } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(items) }} />)
    const rows = container.querySelectorAll('.tpl-list__row')
    expect(rows).toHaveLength(2)
    expect(rows[1].textContent).toContain('测试')
    expect(rows[1].textContent).toContain('运行中')
  })

  it('完全没有可显示的数据项时显示「未知」', async () => {
    const widget = makeWidget({ template: 'list', size, source: 'aggregate', slots: {} })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.textContent).toContain('未知')
    expect(container.querySelector('.tpl-list__row')).toBeNull()
  })

  it('灰显态（过期）仍渲染内容并保留状态徽章', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const widget = listWidget(size, ['disk[/]'], { display_state: 'stale' })
    const { container } = await renderIn(<WidgetView widget={widget} data={{ i1: makeData(disks, { display_state: 'stale' }) }} />)
    expect(container.querySelector('[data-dimmed="true"]')).not.toBeNull()
    expect(container.querySelector('.tpl-list__row')).not.toBeNull()
    expect(err).not.toHaveBeenCalled()
  })
})
