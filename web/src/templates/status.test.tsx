import { screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { readThemeRuntime } from '@/themes/runtime'
import { StatusMarker, resolveStatus } from './status'
import { WidgetFrame } from './frame'
import { FIXED_NOW, bound, makeData, makeWidget, renderIn } from './test-utils'

describe('resolveStatus（Ruling 15）', () => {
  it('五个核心状态直接取同名级别', () => {
    for (const s of ['ok', 'warning', 'critical', 'unknown', 'error']) {
      expect(resolveStatus(s, 'ok')).toEqual({ kind: 'level', level: s })
    }
  })
  it('stale/offline/maintenance 保留 report_status 的级别并标灰显', () => {
    expect(resolveStatus('stale', 'warning')).toEqual({ kind: 'dim', level: 'warning', reason: 'stale' })
    expect(resolveStatus('offline', 'critical')).toEqual({ kind: 'dim', level: 'critical', reason: 'offline' })
    expect(resolveStatus('maintenance', undefined)).toEqual({ kind: 'dim', level: 'unknown', reason: 'maintenance' })
  })
  it('unconfigured/broken 是占位', () => {
    expect(resolveStatus('unconfigured', 'ok')).toEqual({ kind: 'placeholder', reason: 'unconfigured' })
    expect(resolveStatus('broken', 'ok')).toEqual({ kind: 'placeholder', reason: 'broken' })
  })
  it('不认识的状态按 unknown', () => {
    expect(resolveStatus('???', 'ok')).toEqual({ kind: 'level', level: 'unknown' })
  })
})

describe('StatusMarker', () => {
  it('五种形状各不相同，weight 带到属性上', async () => {
    const rt = readThemeRuntime(document.documentElement)
    const markers = new Set<string>()
    for (const level of ['ok', 'warning', 'critical', 'unknown', 'error'] as const) {
      const { container, unmount } = await renderIn(<StatusMarker level={level} />)
      const el = container.querySelector('[data-marker]')!
      markers.add(el.getAttribute('data-marker')!)
      expect(el.getAttribute('data-level')).toBe(level)
      expect(el.getAttribute('data-weight')).toBe(rt.status[level].weight)
      unmount()
    }
    expect(markers.size).toBe(5)
  })
})

const states = ['ok', 'warning', 'critical', 'unknown', 'error', 'stale', 'offline', 'maintenance', 'unconfigured', 'broken']

describe('WidgetFrame：每种展示状态都有形状或图标', () => {
  it.each(states)('展示状态 %s', async (ds) => {
    const widget = makeWidget({ template: 'value', display_state: ds, title: '温度', instance_id: 'i1' })
    const data = { i1: makeData([], { report_status: 'warning' }) }
    const { container } = await renderIn(<WidgetFrame widget={widget} data={data}>内容</WidgetFrame>)
    const frame = container.querySelector('[data-widget-frame]')!
    expect(frame.getAttribute('data-display-state')).toBe(ds)
    if (ds === 'unconfigured' || ds === 'broken') {
      expect(frame.getAttribute('data-placeholder')).toBe(ds)
      expect(container.querySelector('[data-status-icon]')).not.toBeNull()
      expect(container.querySelector('[data-marker]')).toBeNull()
      expect((frame as HTMLElement).style.border).toContain('--state-placeholder-border')
      expect(screen.queryByText('内容')).toBeNull()
    } else {
      expect(container.querySelector('[data-marker]')).not.toBeNull()
      expect(screen.getByText('内容')).toBeInTheDocument()
    }
    if (['stale', 'offline', 'maintenance'].includes(ds)) {
      expect(frame.getAttribute('data-dimmed')).toBe('true')
      expect(frame.getAttribute('data-status-level')).toBe('warning')
      expect(container.querySelector(`[data-status-icon="${ds === 'stale' ? 'clock' : ds === 'offline' ? 'wifi-off' : 'wrench'}"]`)).not.toBeNull()
      expect((container.querySelector('[data-widget-body]') as HTMLElement).style.opacity).toBe('var(--state-dim-opacity)')
      expect(container.querySelector('time')).not.toBeNull()
    } else {
      expect(frame.getAttribute('data-dimmed')).toBe('false')
    }
    if (['warning', 'critical', 'unknown', 'error'].includes(ds)) {
      expect(container.querySelector('[data-status-icon]')).not.toBeNull()
    }
  })

  it('灰显态的时间戳是最后成功时间（服务端时区）', async () => {
    const widget = makeWidget({ template: 'value', display_state: 'stale', instance_id: 'i1' })
    const data = { i1: makeData([], { last_success_at: new Date(FIXED_NOW - 3600_000).toISOString() }) }
    const { container } = await renderIn(<WidgetFrame widget={widget} data={data}>x</WidgetFrame>)
    expect(container.querySelector('time')!.textContent).toBe('13:05')
  })

  it('从未成功的灰显态时间戳显示「从未」而不是现在', async () => {
    const widget = makeWidget({ template: 'value', display_state: 'offline', instance_id: 'i1' })
    const data = { i1: makeData([], { last_success_at: null }) }
    const { container } = await renderIn(<WidgetFrame widget={widget} data={data}>x</WidgetFrame>)
    expect(container.querySelector('time')!.textContent).toBe('从未')
  })

  it('strong/invert 强调进入属性，超长标题被截断且保留 title 全文', async () => {
    const long = '很长很长的标题'.repeat(20)
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [], 'x', { title: long, display_state: 'critical' })
    const { container } = await renderIn(<WidgetFrame widget={widget} data={data}>x</WidgetFrame>)
    const frame = container.querySelector('[data-widget-frame]')!
    expect(['strong', 'invert']).toContain(frame.getAttribute('data-weight'))
    const title = container.querySelector('[data-widget-title]')!
    expect(title.className).toContain('truncate')
    expect(title.getAttribute('title')).toBe(long)
  })

  it('statusMode=static 时只显示占位态，其余不画状态与灰显', async () => {
    const widget = makeWidget({ template: 'clock', display_state: 'stale' })
    const { container } = await renderIn(<WidgetFrame widget={widget} data={{}} statusMode="static">x</WidgetFrame>)
    expect(container.querySelector('[data-marker]')).toBeNull()
    expect(container.querySelector('[data-widget-frame]')!.getAttribute('data-dimmed')).toBe('false')
  })
})

describe('WidgetFrame：展示状态运行时切换', () => {
  afterEach(() => vi.restoreAllMocks())

  it('ok 与 unconfigured 之间来回切换不产生 console error', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const mk = (ds: string) => (
      <WidgetFrame widget={makeWidget({ template: 'value', display_state: ds })} data={{}}>x</WidgetFrame>
    )
    const { rerender, container } = await renderIn(mk('ok'))
    for (const ds of ['unconfigured', 'critical', 'broken', 'ok']) {
      rerender(mk(ds))
      expect(container.querySelector('[data-widget-frame]')!.getAttribute('data-display-state')).toBe(ds)
    }
    expect(err).not.toHaveBeenCalled()
  })
})
