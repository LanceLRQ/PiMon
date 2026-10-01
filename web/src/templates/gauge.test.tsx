import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetView } from './widget-view'
import { bound, renderIn } from './test-utils'

const sizes = [
  { cols: 1, rows: 1 },
  { cols: 2, rows: 1 },
  { cols: 2, rows: 2 },
]
const mem: Item = { key: 'mem', type: 'gauge', value: 72, min: 0, max: 100, unit: '%' }

describe.each(sizes)('gauge 模板 $cols x $rows', (size) => {
  it('画出带 aria 的 meter，读数等宽', async () => {
    const { widget, data } = bound('gauge', size, [mem], 'mem', { title: '内存' })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    const meter = container.querySelector('[role="meter"]')!
    expect(meter.getAttribute('aria-valuenow')).toBe('72')
    expect(meter.getAttribute('aria-valuemax')).toBe('100')
    const reading = container.querySelector('.tpl-gauge__reading')!
    expect(reading.textContent).toContain('72')
    expect(reading.className).toContain('tabular-nums')
    expect(container.querySelector('[data-template="gauge"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
  })

  it('缺失值显示「未知」，不画填充弧', async () => {
    const { widget, data } = bound('gauge', size, [{ key: 'mem', type: 'gauge' }], 'mem')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.textContent).toContain('未知')
    expect(container.querySelector('[role="meter"]')).toBeNull()
  })

  it('值为 0 是 0%', async () => {
    const { widget, data } = bound('gauge', size, [{ ...mem, value: 0 }], 'mem')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[role="meter"]')!.getAttribute('aria-valuenow')).toBe('0')
  })
})

describe('gauge 模板：边界与配色', () => {
  it('超量程时进度夹到 100，读数仍是真实值', async () => {
    const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [{ ...mem, value: 130 }], 'mem')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[role="meter"]')!.getAttribute('aria-valuenow')).toBe('100')
    expect(container.querySelector('.tpl-gauge__reading')!.textContent).toContain('130')
  })
  it('未给量程时按 0–100', async () => {
    const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [{ key: 'mem', type: 'gauge', value: 30 }], 'mem')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[role="meter"]')!.getAttribute('aria-valuenow')).toBe('30')
  })
  it('自定义量程按比例换算', async () => {
    const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [{ key: 'm', type: 'gauge', value: 50, min: 0, max: 200 }], 'm')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[role="meter"]')!.getAttribute('aria-valuenow')).toBe('25')
  })
  it('手动阈值决定进度颜色并带形状标记', async () => {
    const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [mem], 'mem', {
      options: { threshold: { enabled: true, warning: 60, critical: 90, direction: 'above' } },
    })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[data-value-level]')!.getAttribute('data-value-level')).toBe('warning')
    expect(container.querySelector('.tpl-gauge__fill')!.getAttribute('class')).toContain('s-warning')
    expect(container.querySelector('.tpl-gauge__reading [data-marker]')).not.toBeNull()
  })
  it('无阈值时填充用主色（中性）', async () => {
    const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [mem], 'mem')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-gauge__fill')!.getAttribute('class')).toContain('s-primary')
  })
})
