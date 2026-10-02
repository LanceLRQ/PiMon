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

describe('gauge 模板：读数按圆环大小缩放（Ruling 52：1x1 的单位与读数不得压到圆环）', () => {
  const sizeOf = (container: HTMLElement) => {
    const style = (container.querySelector('.tpl-gauge__reading') as HTMLElement).style.fontSize
    const m = style.match(/(\d+(?:\.\d+)?)px\)$/)
    return m ? Number(m[1]) : null
  }
  const withBox = async (px: number, run: () => Promise<void>) => {
    const w = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth')
    const h = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight')
    Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, get: () => px })
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get: () => px })
    try {
      await run()
    } finally {
      if (w) Object.defineProperty(HTMLElement.prototype, 'clientWidth', w)
      else delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientWidth
      if (h) Object.defineProperty(HTMLElement.prototype, 'clientHeight', h)
      else delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientHeight
    }
  }

  it('量到的环越小字号越小，且估算宽度不超过圆环内缘的弦', async () => {
    const long: Item = { key: 'mem', type: 'gauge', value: 74.82, min: 0, max: 100, unit: '%' }
    const fonts: number[] = []
    for (const ring of [56, 64, 120]) {
      await withBox(ring, async () => {
        const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [long], 'mem')
        const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
        const px = sizeOf(container)
        expect(px).not.toBeNull()
        fonts.push(px!)
        // 「74.82%」按每个拉丁字符 0.55em 估算宽度、行高等于字号：读数矩形的角点要落在内缘半径（0.375 倍环径）之内
        const w = px! * 6 * 0.55
        expect(Math.hypot(w / 2, px! / 2)).toBeLessThanOrEqual(ring * 0.375)
      })
    }
    expect(fonts[0]).toBeLessThan(fonts[1])
    expect(fonts[1]).toBeLessThanOrEqual(fonts[2])
  })

  it('量不到尺寸（首帧）时沿用主题 token 字号', async () => {
    const { widget, data } = bound('gauge', { cols: 1, rows: 1 }, [mem], 'mem')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect((container.querySelector('.tpl-gauge__reading') as HTMLElement).style.fontSize).toBe('var(--size-value-sm)')
  })
})
