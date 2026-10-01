import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createScreenStore } from './screen-store'
import { createSnapshotCache, startSnapshotPersistence, type CacheStorageLike } from './snapshot-cache'
import { snapshotOf } from './test-utils'

// 内存版 CacheStorage：只实现 open/put/match
function fakeStorage() {
  const files = new Map<string, string>()
  const storage: CacheStorageLike = {
    async open() {
      return {
        async put(key, res) {
          files.set(key, await res.text())
        },
        async match(key) {
          const text = files.get(key)
          return text === undefined ? undefined : new Response(text)
        },
      }
    },
  }
  return { storage, files }
}

describe('snapshot 本地存储', () => {
  it('保存后能读回，内容覆盖布局、设置、屏幕状态与数据', async () => {
    const { storage } = fakeStorage()
    const cache = createSnapshotCache(storage)
    const store = createScreenStore()
    store.applySnapshot(snapshotOf({ server_time: '2026-10-01T06:00:00Z' }))
    await cache.save(store.getState())
    const loaded = await cache.load()
    expect(loaded?.layout?.screens.map((s) => s.id)).toEqual(['index', 's1'])
    expect(loaded?.settings?.timezone).toBe('Asia/Shanghai')
    expect(loaded?.screenState?.theme_id).toBe('ambient')
    expect(Object.keys(loaded?.data ?? {})).toEqual(['i1'])
    expect(loaded?.lastDataAt).toBe(Date.parse('2026-10-01T06:00:00Z'))
  })

  it('没有存过、内容损坏或结构不对时读回 null', async () => {
    const { storage, files } = fakeStorage()
    const cache = createSnapshotCache(storage)
    expect(await cache.load()).toBeNull()
    files.set('/screen/__snapshot', '{坏的')
    expect(await cache.load()).toBeNull()
    files.set('/screen/__snapshot', JSON.stringify({ v: 1, layout: { screens: 'x' } }))
    expect(await cache.load()).toBeNull()
    files.set('/screen/__snapshot', JSON.stringify({ v: 99, layout: { version: 1, grid: { cols: 1, rows: 1 }, screens: [] } }))
    expect(await cache.load()).toBeNull()
  })

  it('没有 CacheStorage（非安全上下文）时静默跳过', async () => {
    const cache = createSnapshotCache(null)
    await expect(cache.save(createScreenStore().getState())).resolves.toBeUndefined()
    expect(await cache.load()).toBeNull()
  })

  it('存储抛错时不向外抛', async () => {
    const storage: CacheStorageLike = {
      open: () => Promise.reject(new Error('quota')),
    }
    const cache = createSnapshotCache(storage)
    await expect(cache.save(createScreenStore().getState())).resolves.toBeUndefined()
    expect(await cache.load()).toBeNull()
  })
})

describe('restore：离线恢复', () => {
  it('用存下的数据还原 store：有布局与数据、没有连上、lastDataAt 取存储时间，不污染时钟校正', async () => {
    const { storage } = fakeStorage()
    const cache = createSnapshotCache(storage)
    const online = createScreenStore()
    online.applySnapshot(snapshotOf({ server_time: '2020-01-01T00:00:00Z' }))
    await cache.save(online.getState())

    const fresh = createScreenStore()
    fresh.restore((await cache.load())!)
    const s = fresh.getState()
    expect(s.layout?.screens).toHaveLength(2)
    expect(s.data.i1).toBeDefined()
    expect(s.connected).toBe(false)
    expect(s.synced).toBe(false)
    expect(s.lastDataAt).toBe(Date.parse('2020-01-01T00:00:00Z'))
    expect(s.clockOffsetMs).toBe(0)
  })

  it('已经收到过真实 snapshot 后不再被恢复数据覆盖', async () => {
    const { storage } = fakeStorage()
    const cache = createSnapshotCache(storage)
    const old = createScreenStore()
    old.applySnapshot(snapshotOf({ build: 'old' }))
    await cache.save(old.getState())

    const store = createScreenStore()
    store.applySnapshot(snapshotOf({ build: 'new' }))
    store.restore((await cache.load())!)
    expect(store.getState().build).toBe('new')
    expect(store.getState().synced).toBe(true)
  })
})

describe('startSnapshotPersistence', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('首次收到 snapshot 立即落盘，之后节流合并，只存最新状态', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const store = createScreenStore()
    const stop = startSnapshotPersistence(store, { save, load: vi.fn() }, { intervalMs: 5000 })
    store.applySnapshot(snapshotOf({ server_time: '2026-10-01T06:00:00Z' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(save).toHaveBeenCalledTimes(1)

    store.applyPatch({ type: 'patch', server_time: '2026-10-01T06:00:01Z', entity: 'screen_data', screen_data: [] })
    store.applyPatch({ type: 'patch', server_time: '2026-10-01T06:00:02Z', entity: 'screen_data', screen_data: [] })
    await vi.advanceTimersByTimeAsync(1000)
    expect(save).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(4000)
    expect(save).toHaveBeenCalledTimes(2)
    expect(save.mock.calls[1][0].lastDataAt).toBe(Date.parse('2026-10-01T06:00:02Z'))
    stop()
  })

  it('没有真实数据（只是恢复出来的）或没有新数据时不落盘；停止后不再落盘', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const store = createScreenStore()
    const stop = startSnapshotPersistence(store, { save, load: vi.fn() }, { intervalMs: 1000 })
    store.setConnected(true)
    await vi.advanceTimersByTimeAsync(2000)
    expect(save).not.toHaveBeenCalled()

    store.applySnapshot(snapshotOf())
    await vi.advanceTimersByTimeAsync(0)
    expect(save).toHaveBeenCalledTimes(1)
    store.setConnected(false)
    await vi.advanceTimersByTimeAsync(5000)
    expect(save).toHaveBeenCalledTimes(1)

    stop()
    store.applySnapshot(snapshotOf({ server_time: '2030-01-01T00:00:00Z' }))
    await vi.advanceTimersByTimeAsync(5000)
    expect(save).toHaveBeenCalledTimes(1)
  })
})
