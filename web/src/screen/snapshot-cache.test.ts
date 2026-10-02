import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Patch } from '@/types/protocol.generated'
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
        async delete(key) {
          return files.delete(key)
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

  it('clear 删除存档，之后读回 null；没有存档时清除也不报错', async () => {
    const { storage } = fakeStorage()
    const cache = createSnapshotCache(storage)
    const store = createScreenStore()
    store.applySnapshot(snapshotOf())
    await cache.save(store.getState())
    expect(await cache.load()).not.toBeNull()
    await cache.clear()
    expect(await cache.load()).toBeNull()
    await expect(cache.clear()).resolves.toBeUndefined()
  })

  it('没有 CacheStorage（非安全上下文）时静默跳过', async () => {
    const cache = createSnapshotCache(null)
    await expect(cache.save(createScreenStore().getState())).resolves.toBeUndefined()
    await expect(cache.clear()).resolves.toBeUndefined()
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

  const dataPatch = (summary: string, time: string): Patch => ({
    type: 'patch',
    server_time: time,
    entity: 'screen_data',
    screen_data: [{ instance_id: 'i1', display_state: 'ok', report_status: 'ok', report_stale: false, summary, last_success_at: null, items: [] }],
  })

  it('首次收到数据立即落盘，之后每 60 秒至多一次，合并期间只存最新状态', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const store = createScreenStore()
    const stop = startSnapshotPersistence(store, { save, load: vi.fn(), clear: vi.fn() })
    store.applySnapshot(snapshotOf({ server_time: '2026-10-01T06:00:00Z' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(save).toHaveBeenCalledTimes(1)

    store.applyPatch(dataPatch('a', '2026-10-01T06:00:01Z'))
    store.applyPatch(dataPatch('b', '2026-10-01T06:00:02Z'))
    await vi.advanceTimersByTimeAsync(59_000)
    expect(save).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(save).toHaveBeenCalledTimes(2)
    expect(save.mock.calls[1][0].data.i1.summary).toBe('b')
    stop()
  })

  it('内容没有变化（只是数据时间戳在走）就不重写', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const store = createScreenStore()
    const stop = startSnapshotPersistence(store, { save, load: vi.fn(), clear: vi.fn() })
    store.applySnapshot(snapshotOf({ server_time: '2026-10-01T06:00:00Z' }))
    await vi.advanceTimersByTimeAsync(0)
    store.applyPatch(dataPatch('x', '2026-10-01T06:00:01Z'))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(save).toHaveBeenCalledTimes(2)
    // 相同内容反复到来，只有时间戳变化
    for (let i = 2; i < 10; i++) {
      store.applyPatch(dataPatch('x', `2026-10-01T06:00:${String(10 + i).padStart(2, '0')}Z`))
      await vi.advanceTimersByTimeAsync(60_000)
    }
    expect(save).toHaveBeenCalledTimes(2)
    stop()
  })

  it('页面隐藏与 pagehide 时有未落盘的变化就立即补写', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const target = new EventTarget()
    const doc = {
      visibilityState: 'visible' as DocumentVisibilityState,
      addEventListener: target.addEventListener.bind(target),
      removeEventListener: target.removeEventListener.bind(target),
    }
    const win = { addEventListener: target.addEventListener.bind(target), removeEventListener: target.removeEventListener.bind(target) }
    const store = createScreenStore()
    const stop = startSnapshotPersistence(store, { save, load: vi.fn(), clear: vi.fn() }, { doc, win: win as never })
    store.applySnapshot(snapshotOf())
    await vi.advanceTimersByTimeAsync(0)
    expect(save).toHaveBeenCalledTimes(1)

    store.applyPatch(dataPatch('p1', '2026-10-01T06:00:05Z'))
    doc.visibilityState = 'visible'
    target.dispatchEvent(new Event('visibilitychange'))
    expect(save).toHaveBeenCalledTimes(1)
    doc.visibilityState = 'hidden'
    target.dispatchEvent(new Event('visibilitychange'))
    expect(save).toHaveBeenCalledTimes(2)

    store.applyPatch(dataPatch('p2', '2026-10-01T06:00:06Z'))
    target.dispatchEvent(new Event('pagehide'))
    expect(save).toHaveBeenCalledTimes(3)
    // 补写后内容没变，再触发不重复写
    target.dispatchEvent(new Event('pagehide'))
    expect(save).toHaveBeenCalledTimes(3)
    stop()
    store.applyPatch(dataPatch('p3', '2026-10-01T06:00:07Z'))
    target.dispatchEvent(new Event('pagehide'))
    expect(save).toHaveBeenCalledTimes(3)
  })

  it('没有真实数据（只是恢复出来的）不落盘；停止后不再落盘', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const store = createScreenStore()
    const stop = startSnapshotPersistence(store, { save, load: vi.fn(), clear: vi.fn() })
    store.setConnected(true)
    await vi.advanceTimersByTimeAsync(120_000)
    expect(save).not.toHaveBeenCalled()

    store.applySnapshot(snapshotOf())
    await vi.advanceTimersByTimeAsync(0)
    expect(save).toHaveBeenCalledTimes(1)

    stop()
    store.applyPatch(dataPatch('late', '2030-01-01T00:00:00Z'))
    await vi.advanceTimersByTimeAsync(120_000)
    expect(save).toHaveBeenCalledTimes(1)
  })
})
