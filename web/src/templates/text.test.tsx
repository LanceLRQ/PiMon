import { describe, expect, it } from 'vitest'
import { WidgetView } from './widget-view'
import { makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 2, rows: 1 },
  { cols: 2, rows: 2 },
  { cols: 4, rows: 1 },
  { cols: 4, rows: 2 },
]

describe.each(sizes)('text 模板 $cols x $rows', (size) => {
  it('显示选项里的正文与标题', async () => {
    const widget = makeWidget({ template: 'text', size, title: '备忘', options: { text: '周五前交周报' } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.querySelector('.tpl-text__body')!.textContent).toBe('周五前交周报')
    expect(container.querySelector('[data-widget-title]')!.textContent).toBe('备忘')
    expect(container.querySelector('[data-template="text"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
  })

  it('超长正文按行数截断（line clamp）并保留换行', async () => {
    const widget = makeWidget({ template: 'text', size, options: { text: '很长的一段话\n'.repeat(60) } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    const body = container.querySelector('.tpl-text__body') as HTMLElement
    expect(body.dataset.lines).toMatch(/^\d+$/)
    expect(Number(body.dataset.lines)).toBeGreaterThanOrEqual(2)
    expect(body.className).toContain('whitespace-pre-line')
  })

  it('空正文显示提示而不是空白', async () => {
    const widget = makeWidget({ template: 'text', size, options: {} })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.querySelector('.tpl-text__body')!.textContent).toBe('未设置文本')
  })
})

describe('text 模板：边界', () => {
  it('行数随高度增长', async () => {
    const lines = async (rows: number) => {
      const widget = makeWidget({ template: 'text', size: { cols: 4, rows }, options: { text: 'x' } })
      const { container, unmount } = await renderIn(<WidgetView widget={widget} data={{}} />)
      const n = Number((container.querySelector('.tpl-text__body') as HTMLElement).dataset.lines)
      unmount()
      return n
    }
    expect(await lines(2)).toBeGreaterThan(await lines(1))
  })
  it('文本是数据，按纯文本渲染不解析标记', async () => {
    const widget = makeWidget({ template: 'text', size: { cols: 2, rows: 1 }, options: { text: '<b>粗</b>' } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.querySelector('.tpl-text__body b')).toBeNull()
    expect(container.querySelector('.tpl-text__body')!.textContent).toBe('<b>粗</b>')
  })
  it('core 实例的展示状态不让文本灰显（静态模式）', async () => {
    const widget = makeWidget({ template: 'text', size: { cols: 2, rows: 1 }, display_state: 'stale', options: { text: 'x' } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.querySelector('[data-widget-frame]')!.getAttribute('data-dimmed')).toBe('false')
  })
})
