import { act } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { WidgetView } from './widget-view'
import { FIXED_NOW, makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 1, rows: 1 },
  { cols: 2, rows: 1 },
  { cols: 4, rows: 2 },
]

afterEach(() => vi.useRealTimers())

describe.each(sizes)('clock 模板 $cols x $rows', (size) => {
  it('按服务端时区（不是浏览器时区）显示时间', async () => {
    const widget = makeWidget({ template: 'clock', size })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />, { timezone: 'Asia/Shanghai' })
    expect(container.querySelector('.tpl-clock__hour')!.textContent).toBe('14')
    expect(container.querySelector('.tpl-clock__minute')!.textContent).toBe('05')
    expect(container.querySelector('[data-template="clock"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    expect(container.querySelector('.tpl-clock__time')!.className).toContain('tabular-nums')
  })

  it('换时区换时间', async () => {
    const widget = makeWidget({ template: 'clock', size })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />, { timezone: 'America/New_York' })
    expect(container.querySelector('.tpl-clock__hour')!.textContent).toBe('02')
  })
})

describe('clock 模板：各尺寸的内容取舍', () => {
  const has = async (size: { cols: number; rows: number }, options = {}, opts = {}) => {
    const widget = makeWidget({ template: 'clock', size, options })
    const r = await renderIn(<WidgetView widget={widget} data={{}} />, opts)
    return r.container
  }
  it('1x1 只有时分，没有日期与秒', async () => {
    const c = await has({ cols: 1, rows: 1 })
    expect(c.querySelector('.tpl-clock__date')).toBeNull()
    expect(c.querySelector('.tpl-clock__second')).toBeNull()
  })
  it('2x1 与 4x2 显示日期（中文）', async () => {
    expect((await has({ cols: 2, rows: 1 })).querySelector('.tpl-clock__date')!.textContent).toContain('10月1日')
    expect((await has({ cols: 4, rows: 2 })).querySelector('.tpl-clock__date')!.textContent).toContain('周四')
  })
  it('show_seconds 与 show_date 选项生效', async () => {
    const c = await has({ cols: 4, rows: 2 }, { show_seconds: true, show_date: false })
    expect(c.querySelector('.tpl-clock__second')!.textContent).toBe('09')
    expect(c.querySelector('.tpl-clock__date')).toBeNull()
  })
  it('12 小时制显示上午/下午', async () => {
    const c = await has({ cols: 2, rows: 1 }, { format: '12h' }, { lang: 'en' })
    expect(c.querySelector('.tpl-clock__hour')!.textContent).toBe('2')
    expect(c.querySelector('.tpl-clock__period')!.textContent).toMatch(/PM/i)
  })
  it('非法时区回落到 UTC 不抛错', async () => {
    const c = await has({ cols: 1, rows: 1 }, {}, { timezone: 'Not/AZone' })
    expect(c.querySelector('.tpl-clock__hour')!.textContent).toBe('06')
  })
  it('时钟不画状态标记，也不随实例状态灰显', async () => {
    const widget = makeWidget({ template: 'clock', size: { cols: 1, rows: 1 }, display_state: 'stale' })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.querySelector('[data-marker]')).toBeNull()
    expect(container.querySelector('[data-dimmed="true"]')).toBeNull()
  })
})

describe('clock 模板：走注入的时间提供者', () => {
  it('时间提供者前进后时钟刷新', async () => {
    vi.useFakeTimers()
    let now = FIXED_NOW
    const widget = makeWidget({ template: 'clock', size: { cols: 2, rows: 1 } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />, { now: () => now })
    expect(container.querySelector('.tpl-clock__minute')!.textContent).toBe('05')
    now += 120_000
    await act(async () => {
      vi.advanceTimersByTime(1500)
    })
    expect(container.querySelector('.tpl-clock__minute')!.textContent).toBe('07')
  })
})
