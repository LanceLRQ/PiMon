import { createContext, useContext, useEffect, useState } from 'react'
import type { HistoryPoint, WidgetRef } from '@/types/generated'

// 历史数据提供者：chart 模板不直接发请求，由屏幕端应用注入（真实实现走 GET /api/instances/{id}/history）。

export interface HistoryRequest {
  instanceId: string
  item: string
  /** 为空取该数据项的默认字段 */
  field?: string
  /** 1h、24h、7d 这样的时长 */
  range: string
  /** 卸载或刷新时中止未完成的请求 */
  signal?: AbortSignal
}

export interface HistoryProvider {
  getHistory(req: HistoryRequest): Promise<HistoryPoint[]>
}

/** 提供者必须引用稳定（模块级或 useMemo）：它变化会重新取数 */
export const HistoryContext = createContext<HistoryProvider | null>(null)

/** 挂载时取一次，之后每 5 分钟刷新（Ruling 3） */
export const HISTORY_REFRESH_MS = 5 * 60_000

export const historyRanges = ['1h', '24h', '7d'] as const
export const defaultHistoryRange = '24h'

export function parseRange(raw: unknown): string {
  return typeof raw === 'string' && (historyRanges as readonly string[]).includes(raw) ? raw : defaultHistoryRange
}

export type HistoryState = 'unavailable' | 'loading' | 'ready' | 'empty' | 'error'

export interface HistorySeries {
  state: HistoryState
  points: HistoryPoint[]
}

interface Loaded {
  key: string
  points: HistoryPoint[] | null
}

const none: HistoryPoint[] = []

/**
 * 取历史曲线。刷新失败时保留上次成功的曲线；从未成功过则为 error。
 * 点数不足 2 个无法成线，按 empty 处理。
 */
export function useHistorySeries(ref: WidgetRef | undefined, range: string, enabled: boolean): HistorySeries {
  const provider = useContext(HistoryContext)
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const instanceId = ref?.instance_id
  const item = ref?.item
  const field = ref?.field
  const key = `${instanceId}|${item}|${field ?? ''}|${range}`

  useEffect(() => {
    if (!provider || !instanceId || !item || !enabled) return
    let alive = true
    let controller: AbortController | undefined
    const load = () => {
      controller?.abort()
      const c = new AbortController()
      controller = c
      let p: Promise<HistoryPoint[]>
      try {
        p = provider.getHistory({ instanceId, item, field, range, signal: c.signal })
      } catch (e) {
        p = Promise.reject(e instanceof Error ? e : new Error(String(e)))
      }
      p.then((points) => {
        if (alive && !c.signal.aborted) setLoaded({ key, points: Array.isArray(points) ? points : none })
      }).catch(() => {
        if (alive && !c.signal.aborted) setLoaded((prev) => (prev && prev.key === key && prev.points ? prev : { key, points: null }))
      })
    }
    load()
    const timer = setInterval(load, HISTORY_REFRESH_MS)
    return () => {
      alive = false
      clearInterval(timer)
      controller?.abort()
    }
  }, [provider, instanceId, item, field, range, enabled, key])

  if (!provider || !instanceId || !item || !enabled) return { state: 'unavailable', points: none }
  if (!loaded || loaded.key !== key) return { state: 'loading', points: none }
  if (loaded.points === null) return { state: 'error', points: none }
  return { state: loaded.points.length >= 2 ? 'ready' : 'empty', points: loaded.points }
}
