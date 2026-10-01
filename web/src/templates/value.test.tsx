import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetView } from './widget-view'
import { bound, renderIn } from './test-utils'

const sizes = [
  { cols: 1, rows: 1 },
  { cols: 2, rows: 1 },
  { cols: 2, rows: 2 },
]
const cpu: Item = { key: 'cpu', type: 'number', value: 42.5, unit: '%' }

describe.each(sizes)('value 模板 $cols x $rows', (size) => {
  it('显示数值与单位，读数使用等宽数字', async () => {
    const { widget, data } = bound('value', size, [cpu], 'cpu', { title: 'CPU' })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    const num = container.querySelector('.tpl-value__number')!
    expect(num.textContent).toBe('42.5')
    expect(num.className).toContain('tabular-nums')
    expect(container.querySelector('.tpl-value__unit')!.textContent).toBe('%')
    expect(container.querySelector('[data-template="value"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    expect(screen.getByText('CPU')).toBeInTheDocument()
  })

  it('数据项缺失时显示「未知」而不是 0', async () => {
    const { widget, data } = bound('value', size, [], 'cpu')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.textContent).toContain('未知')
    expect(container.querySelector('.tpl-value__number')).toBeNull()
    expect(container.textContent).not.toMatch(/(^|[^\d.])0([^\d.]|$)/)
  })

  it('数据项存在但 value 缺失同样显示「未知」', async () => {
    const { widget, data } = bound('value', size, [{ key: 'cpu', type: 'number' }], 'cpu')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.textContent).toContain('未知')
  })

  it('值为 0 显示 0', async () => {
    const { widget, data } = bound('value', size, [{ key: 'cpu', type: 'number', value: 0, unit: 'ms' }], 'cpu')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-value__number')!.textContent).toBe('0')
  })
})

describe('value 模板：money 与极端数据（Ruling 27）', () => {
  const currencies = ['CNY', 'USD', 'EUR', 'JPY']
  it.each(currencies)('币种 %s 按货币格式显示，不跨币种相加', async (cur) => {
    const items: Item[] = [{ key: 'balance', type: 'money', amount: 1234.5, currency: cur }]
    const { widget, data } = bound('value', { cols: 2, rows: 1 }, items, 'balance')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    const text = container.querySelector('.tpl-value__number')!.textContent!
    expect(text).toMatch(/1,23[45]/)
    expect(text).not.toContain('NaN')
  })
  it('四种币种的符号互不相同', async () => {
    const seen = new Set<string>()
    for (const cur of currencies) {
      const { widget, data } = bound('value', { cols: 1, rows: 1 }, [{ key: 'b', type: 'money', amount: 10, currency: cur }], 'b')
      const { container, unmount } = await renderIn(<WidgetView widget={widget} data={data} />)
      seen.add(container.querySelector('.tpl-value__number')!.textContent!)
      unmount()
    }
    expect(seen.size).toBe(4)
  })
  it('非法币种代码回落到「数值 代码」', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [{ key: 'b', type: 'money', amount: 3, currency: 'XYZ9' }], 'b')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-value__number')!.textContent).toBe('3 XYZ9')
  })
  it('金额缺失显示「未知」', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [{ key: 'b', type: 'money', currency: 'USD' }], 'b')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.textContent).toContain('未知')
  })
  it('超长标题被截断，超大数值不抛错', async () => {
    const title = '一个非常非常非常长的小组件标题'.repeat(8)
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [{ key: 'n', type: 'number', value: 123456789012.345 }], 'n', { title })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[data-widget-title]')!.className).toContain('truncate')
    expect(container.querySelector('.tpl-value__number')!.textContent).toContain('123,456,789,012')
  })
})

describe('value 模板：配色（Ruling 32）', () => {
  const item: Item = { key: 'n', type: 'number', value: 85, unit: '%' }
  const level = (c: HTMLElement) => c.querySelector('[data-value-level]')?.getAttribute('data-value-level') ?? null

  it('开了手动阈值按阈值取色，并带形状标记', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [item], 'n', {
      options: { threshold: { enabled: true, warning: 60, critical: 80, direction: 'above' } },
    })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(level(container)).toBe('critical')
    expect(container.querySelector('.tpl-value__reading [data-marker]')).not.toBeNull()
  })
  it('没有手动阈值时用传入的默认阈值', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [item], 'n')
    const { container } = await renderIn(
      <WidgetView widget={widget} data={data} defaultThreshold={{ enabled: true, warning: 80, direction: 'above' }} />,
    )
    expect(level(container)).toBe('warning')
  })
  it('既无阈值也不是 state 项时中性，不加任何级别', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [item], 'n')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(level(container)).toBeNull()
  })
  it('用 s-* 屏幕颜色类，不用管理端颜色类', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [item], 'n', {
      options: { threshold: { enabled: true, critical: 80, direction: 'above' } },
    })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[data-value-level]')!.className).toContain('text-s-critical')
  })
})

describe('value 模板：2x2 额外显示摘要，1x1 不显示', () => {
  it('summary 只在大尺寸出现', async () => {
    const a = bound('value', { cols: 1, rows: 1 }, [cpu], 'cpu', {}, { summary: '负载正常' })
    const r1 = await renderIn(<WidgetView widget={a.widget} data={a.data} />)
    expect(r1.container.textContent).not.toContain('负载正常')
    r1.unmount()
    const b = bound('value', { cols: 2, rows: 2 }, [cpu], 'cpu', {}, { summary: '负载正常' })
    const r2 = await renderIn(<WidgetView widget={b.widget} data={b.data} />)
    expect(r2.container.textContent).toContain('负载正常')
  })
})

describe('小组件图标（options.icon）', () => {
  it('清单内的图标画出，未知名字不画也不报错', async () => {
    const a = bound('value', { cols: 1, rows: 1 }, [cpu], 'cpu', { options: { icon: 'cpu' } })
    const r1 = await renderIn(<WidgetView widget={a.widget} data={a.data} />)
    expect(r1.container.querySelector('header svg.lucide')).not.toBeNull()
    r1.unmount()
    const b = bound('value', { cols: 1, rows: 1 }, [cpu], 'cpu', { options: { icon: 'no-such-icon' } })
    const r2 = await renderIn(<WidgetView widget={b.widget} data={b.data} />)
    expect(r2.container.querySelector('header svg.lucide')).toBeNull()
  })
})

describe('value 模板：修复项', () => {
  it('读数不带截断，只允许不换行（不会被省略号改写成另一个数）', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [{ key: 'n', type: 'number', value: 1234567.89, unit: 'GB' }], 'n', {
      options: { threshold: { enabled: true, critical: 10, direction: 'above' } },
    })
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    const num = container.querySelector('.tpl-value__number')!
    expect(num.className).not.toContain('truncate')
    expect(num.className).toContain('whitespace-nowrap')
  })

  it('绑定 state 项且没有 text 时显示级别名，与 state 模板一致', async () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [{ key: 's', type: 'state', state: 'ok' }], 's')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-value__number')!.textContent).toBe('正常')
    expect(container.querySelector('[data-value-level]')!.getAttribute('data-value-level')).toBe('ok')
    expect(container.textContent).not.toContain('未知')
  })
})

describe('value 模板：时长（Ruling 49）', () => {
  it('单位为 s 的数值按最大两级显示，英文用缩写', async () => {
    const items: Item[] = [{ key: 'uptime', type: 'number', value: 5 * 60 + 12, unit: 's' }]
    const { widget, data } = bound('value', { cols: 2, rows: 1 }, items)
    const zh = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(zh.container.querySelector('.tpl-value__number')!.textContent).toBe('5 分 12 秒')
    expect(zh.container.querySelector('.tpl-value__unit')).toBeNull()
    zh.unmount()
    const en = await renderIn(<WidgetView widget={widget} data={data} />, { lang: 'en' })
    expect(en.container.querySelector('.tpl-value__number')!.textContent).toBe('5m 12s')
  })
})

describe('value 模板：字节单位自动换算（Ruling 52）', () => {
  it('B/s 的数值显示为 KB/s，读数与单位分开', async () => {
    const items: Item[] = [{ key: 'rx', type: 'number', value: 12_646.96, unit: 'B/s' }]
    const { widget, data } = bound('value', { cols: 2, rows: 1 }, items)
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-value__number')!.textContent).toBe('12.4')
    expect(container.querySelector('.tpl-value__unit')!.textContent).toBe('KB/s')
  })
})

describe('value 模板：quota 的默认读数是百分比', () => {
  it('quota 带字节单位时读数仍以 % 为单位', async () => {
    const items: Item[] = [{ key: 'disk[/]', type: 'quota', remaining_pct: 42.5, used: 100, total: 200, unit: 'B' }]
    const { widget, data } = bound('value', { cols: 2, rows: 1 }, items)
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('.tpl-value__unit')!.textContent).toBe('%')
  })
})
