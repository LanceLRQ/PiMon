import type { ScreenStore, ScreenStoreState } from './screen-store'
import type { ResolvedLayout, ScreenInstanceData, ScreenState } from '@/types/generated'
import type { ScreenSettings } from '@/types/protocol.generated'

// 屏幕端最后一份数据的本地存档：hub 不可达（重启、断网）时，页面用外壳加这份数据继续显示并带断线角标。
// 存在 Cache API 里（与 Service Worker 同属安全上下文；非安全上下文静默跳过）。
// Service Worker 不碰它：它只在页面里读写。

export const snapshotCacheName = 'pimon-screen-data-v1'
const snapshotKey = '/screen/__snapshot'
const storedVersion = 1

export interface StoredScreenData {
  v: 1
  /** 写入时的本机时间（毫秒），仅供排查 */
  savedAt: number
  /** 这份数据对应的服务端时间（毫秒时间戳），断线角标显示「hh:mm 的数据」用 */
  lastDataAt: number | null
  settings: ScreenSettings | null
  layout: ResolvedLayout | null
  screenState: ScreenState | null
  data: Record<string, ScreenInstanceData>
}

export interface SnapshotCache {
  save(state: ScreenStoreState): Promise<void>
  load(): Promise<StoredScreenData | null>
}

/** CacheStorage 的最小子集，便于测试注入 */
export interface CacheStorageLike {
  open(name: string): Promise<{
    put(key: string, res: Response): Promise<void>
    match(key: string): Promise<Response | undefined>
  }>
}

function isStored(v: unknown): v is StoredScreenData {
  if (typeof v !== 'object' || v === null) return false
  const o = v as Partial<StoredScreenData>
  if (o.v !== storedVersion) return false
  const layout = o.layout
  if (layout === null || layout === undefined) return false
  if (typeof layout !== 'object' || !Array.isArray((layout as ResolvedLayout).screens)) return false
  return typeof o.data === 'object' && o.data !== null
}

export function createSnapshotCache(storage: CacheStorageLike | null | undefined = defaultStorage()): SnapshotCache {
  return {
    async save(state) {
      if (!storage || !state.layout) return
      const doc: StoredScreenData = {
        v: storedVersion,
        savedAt: Date.now(),
        lastDataAt: state.lastDataAt,
        settings: state.settings,
        layout: state.layout,
        screenState: state.screenState,
        data: state.data,
      }
      try {
        const cache = await storage.open(snapshotCacheName)
        await cache.put(snapshotKey, new Response(JSON.stringify(doc), { headers: { 'Content-Type': 'application/json' } }))
      } catch {
        // 配额或存储不可用：只是少了离线数据，不影响正常显示
      }
    },
    async load() {
      if (!storage) return null
      try {
        const cache = await storage.open(snapshotCacheName)
        const res = await cache.match(snapshotKey)
        if (!res) return null
        const parsed: unknown = await res.json()
        return isStored(parsed) ? parsed : null
      } catch {
        return null
      }
    },
  }
}

function defaultStorage(): CacheStorageLike | null {
  return typeof caches === 'undefined' ? null : caches
}

export const defaultSnapshotCache: SnapshotCache = {
  save: (s) => createSnapshotCache().save(s),
  load: () => createSnapshotCache().load(),
}

export interface PersistenceOptions {
  /** 两次落盘的最小间隔，默认 5 秒；patch 频繁到来时合并 */
  intervalMs?: number
}

/**
 * 订阅屏幕 store，把最新的真实数据节流写入本地存档：首次收到数据立即写，之后至多每 intervalMs 写一次。
 * 只有从中枢收到过数据（synced）且服务端数据时间有更新才写，所以恢复出来的旧数据不会被原样重写。
 * 返回停止函数。
 */
export function startSnapshotPersistence(store: ScreenStore, cache: SnapshotCache, opts: PersistenceOptions = {}): () => void {
  const intervalMs = opts.intervalMs ?? 5000
  let savedAt = Number.NEGATIVE_INFINITY
  let savedDataAt: number | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let stopped = false

  const flush = () => {
    timer = null
    if (stopped) return
    const state = store.getState()
    if (!state.synced || !state.layout) return
    if (state.lastDataAt === savedDataAt) return
    savedAt = Date.now()
    savedDataAt = state.lastDataAt
    void cache.save(state)
  }

  const unsubscribe = store.subscribe(() => {
    if (timer !== null || stopped) return
    const state = store.getState()
    if (!state.synced || !state.layout || state.lastDataAt === savedDataAt) return
    timer = setTimeout(flush, Math.max(0, savedAt + intervalMs - Date.now()))
  })

  return () => {
    stopped = true
    unsubscribe()
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
}
