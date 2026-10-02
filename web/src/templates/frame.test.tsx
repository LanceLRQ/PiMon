import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { WidgetFrame } from './frame'
import { makeData, makeWidget, renderIn } from './test-utils'

const num: Item = { key: 'mem', type: 'number', value: 67 }

function frameOf(container: HTMLElement) {
  return container.querySelector<HTMLElement>('[data-widget-frame]')!
}

describe('外框着色按小组件自身绑定的数据项（Ruling 47）', () => {
  it('实例整体 critical 但数值项无阈值：外框中性，标题栏标记为 critical', async () => {
    const widget = makeWidget({ template: 'gauge', display_state: 'critical', instance_id: 'i1', slots: { value: [{ instance_id: 'i1', item: 'mem' }] } })
    const { container } = await renderIn(<WidgetFrame widget={widget} data={{ i1: makeData([num]) }}>x</WidgetFrame>)
    const f = frameOf(container)
    expect(f.getAttribute('data-accent-level')).toBe('none')
    expect(f.getAttribute('data-weight')).toBe('none')
    expect(f.style.border).toContain('var(--border)')
    expect(f.className).toContain('bg-s-card')
    expect(f.getAttribute('data-status-level')).toBe('critical')
    expect(container.querySelector('header [data-marker][data-level="critical"]')).not.toBeNull()
  })

  it('开了手动阈值且超限：外框按该级别着色', async () => {
    const widget = makeWidget({
      template: 'gauge',
      display_state: 'ok',
      instance_id: 'i1',
      slots: { value: [{ instance_id: 'i1', item: 'mem' }] },
      options: { threshold: { enabled: true, warning: 50, critical: 60, direction: 'above' } },
    })
    const { container } = await renderIn(<WidgetFrame widget={widget} data={{ i1: makeData([num]) }}>x</WidgetFrame>)
    const f = frameOf(container)
    expect(f.getAttribute('data-accent-level')).toBe('critical')
    expect(f.style.border).toContain('--status-critical-color')
    expect(f.getAttribute('data-status-level')).toBe('ok')
  })

  it('state 项按其 state 着色，即使实例整体正常', async () => {
    const widget = makeWidget({ template: 'state', display_state: 'ok', instance_id: 'i1', slots: { state: [{ instance_id: 'i1', item: 's' }] } })
    const items: Item[] = [{ key: 's', type: 'state', state: 'critical', text: '挂了' }]
    const { container } = await renderIn(<WidgetFrame widget={widget} data={{ i1: makeData(items) }}>x</WidgetFrame>)
    expect(frameOf(container).getAttribute('data-accent-level')).toBe('critical')
  })

  it('通配引用展开为成员后取最严重的级别', async () => {
    const widget = makeWidget({ template: 'list', source: 'aggregate', display_state: 'ok', slots: { items: [{ instance_id: 'i1', item: 'target[*]' }] } })
    const items: Item[] = [
      { key: 'target[a]', type: 'gauge', value: 100 },
      { key: 'target[b]', type: 'gauge', value: 0 },
    ]
    const { container } = await renderIn(<WidgetFrame widget={widget} data={{ i1: makeData(items) }}>x</WidgetFrame>)
    expect(frameOf(container).getAttribute('data-accent-level')).toBe('critical')
  })

  it('灰显态仍灰显，占位态不着色', async () => {
    const widget = makeWidget({ template: 'gauge', display_state: 'stale', instance_id: 'i1', slots: { value: [{ instance_id: 'i1', item: 'mem' }] } })
    const { container } = await renderIn(<WidgetFrame widget={widget} data={{ i1: makeData([num]) }}>x</WidgetFrame>)
    expect(frameOf(container).getAttribute('data-dimmed')).toBe('true')
    const ph = makeWidget({ template: 'gauge', display_state: 'broken' })
    const r = await renderIn(<WidgetFrame widget={ph} data={{}}>x</WidgetFrame>)
    expect(frameOf(r.container).getAttribute('data-accent-level')).toBe('none')
  })
})
