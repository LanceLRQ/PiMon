import { describe, expect, it } from 'vitest'
import type { Item, ResolvedHost } from '@/types/generated'
import { WidgetView } from './widget-view'
import { makeData, makeWidget, renderIn } from './test-utils'

const cpu = (value: number): Item[] => [{ key: 'cpu', type: 'gauge', value, unit: '%' }]

function hostsWidget(hosts: ResolvedHost[], over = {}) {
  return makeWidget({
    template: 'status-grid',
    source: 'aggregate',
    size: { cols: 4, rows: 2 },
    title: '主机',
    hosts,
    slots: { items: hosts.filter((h) => h.instance_id).map((h) => ({ instance_id: h.instance_id!, item: 'cpu' })) },
    ...over,
  })
}

async function cellsOf(hosts: ResolvedHost[], data = {}, lang: 'zh' | 'en' = 'zh') {
  const options = { threshold: { enabled: true, warning: 70, critical: 90, direction: 'above' } }
  const { container } = await renderIn(<WidgetView widget={hostsWidget(hosts, { options })} data={data} />, { lang })
  return [...container.querySelectorAll<HTMLElement>('.tpl-grid__cell')]
}

describe('status-grid 主机灯组模式', () => {
  it('沿用结构契约：每格名称、标记、副文本，data-host-state 标出状态', async () => {
    const cells = await cellsOf(
      [{ id: 'hub', name: '', state: 'online', instance_id: 'i1' }, { id: 'd1', name: 'nas', state: 'online' }],
      { i1: makeData(cpu(12)) },
    )
    expect(cells).toHaveLength(2)
    expect(cells.map((c) => c.getAttribute('data-host-state'))).toEqual(['online', 'online'])
    expect(cells[0].querySelector('.tpl-grid__name')!.textContent).toBe('中枢')
    expect(cells[0].querySelector('.tpl-grid__marker')).not.toBeNull()
    expect(cells[0].querySelector('.tpl-grid__sub')!.textContent).toContain('12')
    expect(cells[0].getAttribute('data-value-level')).toBe('ok')
    expect(cells[1].querySelector('.tpl-grid__name')!.textContent).toBe('nas')
  })

  it('英文下中枢名为 Hub', async () => {
    const cells = await cellsOf([{ id: 'hub', name: '', state: 'online' }], {}, 'en')
    expect(cells[0].querySelector('.tpl-grid__name')!.textContent).toBe('Hub')
  })

  it('在线且没有 CPU 实例：不显示数值，级别按 ok', async () => {
    const [cell] = await cellsOf([{ id: 'd1', name: 'nas', state: 'online' }])
    expect(cell.getAttribute('data-value-level')).toBe('ok')
    expect(cell.querySelector('.tpl-grid__sub')!.textContent).toBe('')
  })

  it('在线时级别取 CPU 阈值', async () => {
    const [cell] = await cellsOf([{ id: 'd1', name: 'nas', state: 'online', instance_id: 'i1' }], { i1: makeData(cpu(99)) })
    expect(cell.getAttribute('data-value-level')).toBe('critical')
    expect(cell.querySelector('.tpl-grid__sub')!.textContent).toContain('99')
  })

  it('有实例但 CPU 缺失显示未知，不当作零', async () => {
    const [cell] = await cellsOf([{ id: 'd1', name: 'nas', state: 'online', instance_id: 'i1' }], { i1: makeData([]) })
    expect(cell.getAttribute('data-value-level')).toBe('unknown')
    expect(cell.querySelector('.tpl-grid__sub')!.textContent).toBe('未知')
  })

  it.each([
    ['disconnected', 'warning', '连接中断', null],
    ['offline', 'critical', '离线', 'wifi-off'],
    ['dormant', 'unknown', '休眠', 'moon'],
    ['pending', 'unknown', '等待连接', 'clock'],
    ['maintenance', 'unknown', '维护中', 'wrench'],
    ['incompatible', 'warning', '需要升级', null],
  ])('%s：级别 %s，副文本「%s」，图标 %s，不显示 CPU 数值', async (state, level, text, icon) => {
    const [cell] = await cellsOf([{ id: 'd1', name: 'nas', state, instance_id: 'i1' }], { i1: makeData(cpu(50)) })
    expect(cell.getAttribute('data-host-state')).toBe(state)
    expect(cell.getAttribute('data-value-level')).toBe(level)
    expect(cell.querySelector('.tpl-grid__sub')!.textContent).toBe(text)
    if (icon) expect(cell.querySelector(`[data-status-icon="${icon}"]`)).not.toBeNull()
    else expect(cell.querySelector('[data-status-icon]')).toBeNull()
  })

  it('offline 用 dim 弱化，dormant 用更低透明度加灰度，二者都不改状态标记', async () => {
    const cells = await cellsOf([
      { id: 'a', name: 'a', state: 'offline' },
      { id: 'b', name: 'b', state: 'dormant' },
      { id: 'c', name: 'c', state: 'online' },
    ])
    expect(cells[0].style.opacity).toBe('var(--state-dim-opacity)')
    expect(cells[0].style.filter).toBe('')
    expect(cells[1].style.opacity).toBe('var(--state-dormant-opacity)')
    expect(cells[1].style.filter).toBe('var(--state-dormant-filter)')
    expect(cells[2].style.opacity).toBe('')
    expect(cells[0].querySelector('[data-marker]')!.getAttribute('data-level')).toBe('critical')
    expect(cells[1].querySelector('[data-marker]')!.getAttribute('data-level')).toBe('unknown')
  })

  it('放不下时折成「+N」', async () => {
    const hosts = Array.from({ length: 40 }, (_, i) => ({ id: `d${i}`, name: `n${i}`, state: 'online' }))
    const { container } = await renderIn(<WidgetView widget={hostsWidget(hosts)} data={{}} />)
    const shown = container.querySelectorAll('.tpl-grid__cell').length
    expect(shown).toBeLessThan(40)
    expect(container.querySelector('.tpl-grid__more')!.textContent).toBe(`+${40 - shown}`)
  })
})
