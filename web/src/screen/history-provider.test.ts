import { describe, expect, it, vi } from 'vitest'
import type { HistoryResult } from '@/types/generated'
import { createHistoryProvider, screenHistoryProvider } from './history-provider'

const result = (points: HistoryResult['points']): HistoryResult => ({
  instance_id: 'i1',
  item: 'temp',
  field: 'value',
  range: '24h',
  tier: '5m',
  from: 0,
  to: 1,
  points,
})

describe('屏幕端 history 提供者', () => {
  it('请求 GET /api/instances/{id}/history 并返回曲线点', async () => {
    const points = [
      { t: 1, avg: 2, min: 1, max: 3 },
      { t: 2, avg: 3, min: 2, max: 4 },
    ]
    const get = vi.fn().mockResolvedValue(result(points))
    const signal = new AbortController().signal
    const out = await createHistoryProvider(get).getHistory({ instanceId: 'a/b', item: 'disk[/]', field: 'used', range: '7d', signal })
    expect(out).toBe(points)
    expect(get).toHaveBeenCalledWith('/api/instances/a%2Fb/history', { query: { item: 'disk[/]', field: 'used', range: '7d' }, signal })
  })

  it('省略 field 时不带该参数', async () => {
    const get = vi.fn().mockResolvedValue(result([]))
    await createHistoryProvider(get).getHistory({ instanceId: 'i1', item: 'cpu', range: '1h' })
    expect(get.mock.calls[0][1].query).toEqual({ item: 'cpu', field: undefined, range: '1h' })
  })

  it('请求失败时抛出，由模板降级为「暂无历史数据」', async () => {
    const get = vi.fn().mockRejectedValue(new Error('boom'))
    await expect(createHistoryProvider(get).getHistory({ instanceId: 'i1', item: 'cpu', range: '1h' })).rejects.toThrow('boom')
  })

  it('响应没有 points 时按空曲线处理', async () => {
    const get = vi.fn().mockResolvedValue({})
    expect(await createHistoryProvider(get).getHistory({ instanceId: 'i1', item: 'cpu', range: '1h' })).toEqual([])
  })

  it('默认提供者是模块级单例，引用稳定', () => {
    expect(screenHistoryProvider).toBe(screenHistoryProvider)
    expect(typeof screenHistoryProvider.getHistory).toBe('function')
  })
})
