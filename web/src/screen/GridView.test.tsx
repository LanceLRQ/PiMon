import { fireEvent } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { makeData, renderIn } from '@/templates/test-utils'
import { GridView } from './GridView'
import { screenOf, temp, textWidget, valueWidget } from './test-utils'

describe('网格渲染', () => {
  const screen = screenOf('index', [textWidget('w1', 0, 0), valueWidget('w2', 2, 1)])

  it('容器占满给定尺寸且不滚动，小组件按网格绝对定位', async () => {
    const { container } = await renderIn(
      <GridView screen={screen} grid={{ cols: 8, rows: 5 }} width={1024} height={600} data={{ i1: makeData([temp]) }} />,
    )
    const root = container.querySelector<HTMLElement>('[data-screen-grid]')!
    expect(root.style.width).toBe('1024px')
    expect(root.style.height).toBe('600px')
    expect(root.className).toContain('overflow-hidden')
    const w1 = container.querySelector<HTMLElement>('[data-widget-id="w1"]')!
    expect(w1.style.left).toBe('0px')
    expect(w1.style.width).toBe('256px')
    expect(w1.style.height).toBe('120px')
    const w2 = container.querySelector<HTMLElement>('[data-widget-id="w2"]')!
    expect(w2.style.left).toBe('256px')
    expect(w2.style.top).toBe('120px')
    expect(w2.style.width).toBe('128px')
  })

  it('每个小组件用 D5 的 WidgetView 渲染', async () => {
    const { container } = await renderIn(
      <GridView screen={screen} grid={{ cols: 8, rows: 5 }} width={1024} height={600} data={{ i1: makeData([temp]) }} />,
    )
    expect(container.querySelector('[data-template="text"]')).not.toBeNull()
    expect(container.querySelector('[data-template="value"]')).not.toBeNull()
  })

  it('点击小组件回调其 id；没有回调时不可点击', async () => {
    const onWidgetClick = vi.fn()
    const { container, rerender } = await renderIn(
      <GridView screen={screen} grid={{ cols: 8, rows: 5 }} width={1024} height={600} data={{}} onWidgetClick={onWidgetClick} />,
    )
    fireEvent.click(container.querySelector('[data-widget-id="w2"]')!)
    expect(onWidgetClick).toHaveBeenCalledWith('w2')
    expect(container.querySelector('[data-widget-id="w2"]')!.getAttribute('data-clickable')).toBe('true')
    rerender(<GridView screen={screen} grid={{ cols: 8, rows: 5 }} width={1024} height={600} data={{}} />)
    expect(container.querySelector('[data-widget-id="w2"]')!.getAttribute('data-clickable')).toBeNull()
  })

  it('空 screen 渲染空网格不报错', async () => {
    const { container } = await renderIn(<GridView screen={screenOf('index', [])} grid={{ cols: 8, rows: 5 }} width={1024} height={600} data={{}} />)
    expect(container.querySelectorAll('[data-widget-id]')).toHaveLength(0)
  })

  it('可高亮某个小组件（严重告警入口，M4 使用）', async () => {
    const { container } = await renderIn(
      <GridView screen={screen} grid={{ cols: 8, rows: 5 }} width={1024} height={600} data={{}} highlightId="w1" />,
    )
    expect(container.querySelector('[data-widget-id="w1"]')!.getAttribute('data-highlight')).toBe('true')
    expect(container.querySelector('[data-widget-id="w2"]')!.getAttribute('data-highlight')).toBeNull()
  })
})
