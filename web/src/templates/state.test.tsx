import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetView } from './widget-view'
import { bound, makeData, makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 1, rows: 1 },
  { cols: 2, rows: 1 },
]
const down: Item = { key: 'status', type: 'state', state: 'critical', text: 'offline' }

describe.each(sizes)('state 模板 $cols x $rows', (size) => {
  it('按 state 画形状并显示文字，文字走 plugin.<id>.<key> 查找', async () => {
    const { widget, data } = bound('state', size, [down], 'status', { source: 'plugin', plugin_id: 'hub-self', slots: { state: [{ instance_id: 'i1', item: 'status' }] } })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    const body = container.querySelector('.tpl-state')!
    expect(body.getAttribute('data-value-level')).toBe('critical')
    expect(body.querySelector('[data-marker]')).not.toBeNull()
    expect(container.querySelector('.tpl-state__label')!.textContent).toBe('离线')
    expect(container.querySelector('[data-template="state"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
  })

  it('没有文字时显示级别名', async () => {
    const { widget, data } = bound('state', size, [{ key: 'status', type: 'state', state: 'ok' }], 'status')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-state__label')!.textContent).toBe('正常')
  })

  it('数据项缺失显示「未知」并用 unknown 形状', async () => {
    const { widget, data } = bound('state', size, [], 'status')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-state__label')!.textContent).toBe('未知')
    expect(container.querySelector('.tpl-state')!.getAttribute('data-value-level')).toBe('unknown')
  })
})

describe('state 模板：边界', () => {
  it('查不到 key 的文字原样显示', async () => {
    const { widget, data } = bound('state', { cols: 2, rows: 1 }, [{ key: 's', type: 'state', state: 'warning', text: 'HTTP 503 Service Unavailable' }], 's', { source: 'plugin', plugin_id: 'httpcheck' })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-state__label')!.textContent).toBe('HTTP 503 Service Unavailable')
  })
  it('非法 state 值按 unknown', async () => {
    const { widget, data } = bound('state', { cols: 1, rows: 1 }, [{ key: 's', type: 'state', state: 'weird' }], 's')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-state')!.getAttribute('data-value-level')).toBe('unknown')
  })
  it('plugin 来源的 state 槽也能读取', async () => {
    const widget = makeWidget({ template: 'state', source: 'plugin', plugin_id: 'tcpcheck', instance_id: 'i1', slots: { state: [{ instance_id: 'i1', item: 'status' }] }, size: { cols: 1, rows: 1 } })
    const data = { i1: makeData([{ key: 'status', type: 'state', state: 'ok' }]) }
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-state')!.getAttribute('data-value-level')).toBe('ok')
  })
})
