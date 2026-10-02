import { act, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { HistoryPoint, Item } from '@/types/generated'
import { HistoryContext, type HistoryProvider } from './history'
import { WidgetView } from './widget-view'
import { bound, renderIn } from './test-utils'

const sizes = [
  { cols: 2, rows: 1 },
  { cols: 2, rows: 2 },
  { cols: 4, rows: 2 },
]
const cpu: Item = { key: 'cpu', type: 'number', value: 42.5, unit: '%' }
const points: HistoryPoint[] = Array.from({ length: 12 }, (_, i) => ({ t: 1_000_000 + i * 60_000, avg: 20 + i, min: 18 + i, max: 22 + i }))

function withProvider(provider: HistoryProvider | null, ui: ReactNode) {
  return <HistoryContext.Provider value={provider}>{ui}</HistoryContext.Provider>
}

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe.each(sizes)('chart 模板 $cols x $rows', (size) => {
  it('取到历史后画出趋势，并带当前读数', async () => {
    const getHistory = vi.fn().mockResolvedValue(points)
    const { widget, data } = bound('chart', size, [cpu], 'cpu', { title: 'CPU 趋势' })
    const { container } = await renderIn(withProvider({ getHistory }, <WidgetView widget={widget} data={data} />))
    await waitFor(() => expect(container.querySelector('[data-chart-state="ready"]')).not.toBeNull())
    expect(container.querySelector('[data-template="chart"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    expect(container.querySelector('.tpl-chart__plot svg')).not.toBeNull()
    expect(container.querySelector('.tpl-chart__reading')!.textContent).toContain('42.5')
    expect(container.querySelector('.tpl-chart__reading')!.className).toContain('tabular-nums')
    expect(getHistory).toHaveBeenCalledTimes(1)
    expect(getHistory.mock.calls[0][0]).toMatchObject({ instanceId: 'i1', item: 'cpu', range: '24h' })
  })

  it('历史为空时显示降级文案且不报 console.error', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { container } = await renderIn(withProvider({ getHistory: () => Promise.resolve([]) }, <WidgetView widget={widget} data={data} />))
    await waitFor(() => expect(container.querySelector('[data-chart-state="empty"]')).not.toBeNull())
    expect(container.textContent).toContain('暂无历史数据')
    expect(container.querySelector('.tpl-chart__plot svg')).toBeNull()
    expect(err).not.toHaveBeenCalled()
  })

  it('history 提供者报错时降级显示且不报 console.error', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { container } = await renderIn(withProvider({ getHistory: () => Promise.reject(new Error('boom')) }, <WidgetView widget={widget} data={data} />))
    await waitFor(() => expect(container.querySelector('[data-chart-state="error"]')).not.toBeNull())
    expect(container.textContent).toContain('暂无历史数据')
    expect(container.querySelector('.tpl-chart__reading')!.textContent).toContain('42.5')
    expect(err).not.toHaveBeenCalled()
  })
})

describe('chart 模板细节', () => {
  const size = { cols: 2, rows: 2 }

  it('没有注入 history 提供者时显示降级文案', async () => {
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { container } = await renderIn(<WidgetView widget={widget} data={data} />)
    expect(container.querySelector('[data-chart-state="unavailable"]')).not.toBeNull()
    expect(container.textContent).toContain('暂无历史数据')
  })

  it('加载中显示加载状态', async () => {
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { container } = await renderIn(withProvider({ getHistory: () => new Promise<HistoryPoint[]>(() => {}) }, <WidgetView widget={widget} data={data} />))
    expect(container.querySelector('[data-chart-state="loading"]')).not.toBeNull()
  })

  it('挂载取一次，之后每 5 分钟刷新，卸载后停止并中止未完成请求', async () => {
    vi.useFakeTimers()
    const signals: AbortSignal[] = []
    const getHistory = vi.fn((req: { signal?: AbortSignal }) => {
      if (req.signal) signals.push(req.signal)
      return Promise.resolve(points)
    })
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { unmount } = await renderIn(withProvider({ getHistory }, <WidgetView widget={widget} data={data} />))
    await act(async () => { await Promise.resolve() })
    expect(getHistory).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(5 * 60_000 - 1) })
    expect(getHistory).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(getHistory).toHaveBeenCalledTimes(2)
    unmount()
    expect(signals.at(-1)!.aborted).toBe(true)
    await act(async () => { await vi.advanceTimersByTimeAsync(10 * 60_000) })
    expect(getHistory).toHaveBeenCalledTimes(2)
  })

  it('刷新失败时保留上次的曲线', async () => {
    vi.useFakeTimers()
    const getHistory = vi.fn().mockResolvedValueOnce(points).mockRejectedValue(new Error('down'))
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { container } = await renderIn(withProvider({ getHistory }, <WidgetView widget={widget} data={data} />))
    await act(async () => { await Promise.resolve() })
    await act(async () => { await vi.advanceTimersByTimeAsync(5 * 60_000) })
    expect(getHistory).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-chart-state="ready"]')).not.toBeNull()
  })

  it('options.range 合法时传给提供者，非法回落 24h', async () => {
    const getHistory = vi.fn().mockResolvedValue(points)
    const a = bound('chart', size, [cpu], 'cpu', { options: { range: '7d' } })
    await renderIn(withProvider({ getHistory }, <WidgetView widget={a.widget} data={a.data} />))
    await waitFor(() => expect(getHistory).toHaveBeenCalled())
    expect(getHistory.mock.calls[0][0].range).toBe('7d')
    const getHistory2 = vi.fn().mockResolvedValue(points)
    const b = bound('chart', size, [cpu], 'cpu', { options: { range: 'forever' } })
    await renderIn(withProvider({ getHistory: getHistory2 }, <WidgetView widget={b.widget} data={b.data} />))
    await waitFor(() => expect(getHistory2).toHaveBeenCalled())
    expect(getHistory2.mock.calls[0][0].range).toBe('24h')
  })

  it('wind_direction 与非数值项不画历史也不请求', async () => {
    const getHistory = vi.fn().mockResolvedValue(points)
    const wind = bound('chart', size, [{ key: 'wind_direction', type: 'number', value: 90, unit: '°' }], 'wind_direction')
    const { container } = await renderIn(withProvider({ getHistory }, <WidgetView widget={wind.widget} data={wind.data} />))
    expect(container.querySelector('[data-chart-state="unsupported"]')).not.toBeNull()
    const text = bound('chart', size, [{ key: 'note', type: 'text', text: 'hi' }], 'note')
    const r2 = await renderIn(withProvider({ getHistory }, <WidgetView widget={text.widget} data={text.data} />))
    expect(r2.container.querySelector('[data-chart-state="unsupported"]')).not.toBeNull()
    expect(getHistory).not.toHaveBeenCalled()
  })

  it('当前数据项缺失时读数显示「未知」', async () => {
    const { widget, data } = bound('chart', size, [], 'cpu')
    const { container } = await renderIn(withProvider({ getHistory: () => Promise.resolve(points) }, <WidgetView widget={widget} data={data} />))
    expect(container.querySelector('.tpl-chart__reading')!.textContent).toContain('未知')
  })

  it('图表只用 L2 图表 token，不含颜色字面量', async () => {
    const { widget, data } = bound('chart', size, [cpu], 'cpu')
    const { container } = await renderIn(withProvider({ getHistory: () => Promise.resolve(points) }, <WidgetView widget={widget} data={data} />))
    await waitFor(() => expect(container.querySelector('[data-chart-state="ready"]')).not.toBeNull())
    expect(container.innerHTML).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgb\(/)
    expect(container.innerHTML).toContain('var(--chart-1)')
  })
})
