import { http, type RequestOptions } from '@/api/client'
import type { HistoryPoint, HistoryResult } from '@/types/generated'
import type { HistoryProvider } from '@/templates'

type Get = (path: string, opts?: Omit<RequestOptions, 'method' | 'body'>) => Promise<HistoryResult>

/** 用 GET /api/instances/{id}/history 实现 chart 模板需要的历史提供者 */
export function createHistoryProvider(get: Get = http.get<HistoryResult>): HistoryProvider {
  return {
    async getHistory(req): Promise<HistoryPoint[]> {
      const res = await get(`/api/instances/${encodeURIComponent(req.instanceId)}/history`, {
        query: { item: req.item, field: req.field, range: req.range },
        signal: req.signal,
      })
      return res.points ?? []
    },
  }
}

/** 模块级单例：提供者引用变化会让所有 chart 重新取数，所以必须稳定 */
export const screenHistoryProvider: HistoryProvider = createHistoryProvider()
