import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { createLiveStore, type LiveStore } from '@/store/live-store'
import { LiveSocket } from './ws'

class FakeSocket {
  static instances: FakeSocket[] = []
  readyState = 0
  sent: string[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  url: string
  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }
  send(d: string) {
    this.sent.push(d)
  }
  close() {
    this.readyState = 3
  }
  open() {
    this.readyState = 1
    this.onopen?.()
  }
  receive(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) })
  }
  drop() {
    this.readyState = 3
    this.onclose?.()
  }
}

const snap = (build: string, ids: string[] = []) => ({
  type: 'snapshot',
  build,
  server_time: '2026-10-01T00:00:00Z',
  role: 'admin',
  topics: ['instances', 'settings'],
  instances: ids.map((id) => ({ id })),
})

describe('LiveSocket', () => {
  let store: LiveStore
  let reload: Mock<() => void>
  let failed: Mock<() => void>
  let pageBuildValue: string | null
  let sock: LiveSocket

  const last = () => FakeSocket.instances[FakeSocket.instances.length - 1]

  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('WebSocket', Object.assign(FakeSocket, { OPEN: 1 }))
    FakeSocket.instances = []
    store = createLiveStore()
    reload = vi.fn<() => void>()
    failed = vi.fn<() => void>()
    pageBuildValue = 'b1'
    sock = new LiveSocket({
      store,
      url: 'ws://hub/ws',
      createSocket: (u) => new FakeSocket(u) as unknown as WebSocket,
      reload,
      getPageBuild: () => pageBuildValue,
      random: () => 1,
      onHandshakeFailed: failed,
    })
  })
  afterEach(() => {
    sock.stop()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('连上后标记 connected，收到 snapshot 与 patch 写入 store', () => {
    sock.start()
    last().open()
    expect(store.getState().connected).toBe(true)
    last().receive(snap('b1', ['a']))
    expect(store.getState().instances.map((i) => i.id)).toEqual(['a'])
    last().receive({ type: 'patch', server_time: '2026-10-01T00:00:01Z', entity: 'instance_removed', id: 'a' })
    expect(store.getState().instances).toEqual([])
  })

  it('snapshot 的 build 与页面注入的不一致时触发 reload，且不写入 store', () => {
    sock.start()
    last().open()
    last().receive(snap('b2', ['a']))
    expect(reload).toHaveBeenCalledTimes(1)
    expect(store.getState().synced).toBe(false)
  })

  it('build 一致、页面是开发占位（null）或 snapshot 无 build 时不 reload', () => {
    sock.start()
    last().open()
    last().receive(snap('b1'))
    pageBuildValue = null
    last().receive(snap('whatever'))
    pageBuildValue = 'b1'
    last().receive(snap(''))
    expect(reload).not.toHaveBeenCalled()
    expect(store.getState().synced).toBe(true)
  })

  it('每 20 秒发一次 ping', () => {
    sock.start()
    last().open()
    vi.advanceTimersByTime(20_000)
    expect(last().sent).toEqual(['{"type":"ping"}'])
    vi.advanceTimersByTime(40_000)
    expect(last().sent).toHaveLength(3)
  })

  it('pong 校正时钟，error 消息记录但不断开', () => {
    sock.start()
    last().open()
    last().receive({ type: 'pong', server_time: new Date(Date.now() + 60_000).toISOString() })
    expect(store.getState().clockOffsetMs).toBeGreaterThan(50_000)
    last().receive({ type: 'error', error: { code: 'ws.subscribe_denied', details: { topics: ['instances'] } } })
    expect(store.getState().lastError?.code).toBe('ws.subscribe_denied')
    expect(store.getState().connected).toBe(true)
  })

  it('断线后指数退避重连，成功后退避重置', () => {
    sock.start()
    last().open()
    last().drop()
    expect(store.getState().connected).toBe(false)
    expect(FakeSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(FakeSocket.instances).toHaveLength(2)
    last().open()
    last().drop() // 成功连过，退避从头开始
    vi.advanceTimersByTime(1000)
    expect(FakeSocket.instances).toHaveLength(3)
    last().drop() // 握手失败：2s 后重连
    vi.advanceTimersByTime(1999)
    expect(FakeSocket.instances).toHaveLength(3)
    vi.advanceTimersByTime(1)
    expect(FakeSocket.instances).toHaveLength(4)
  })

  it('退避封顶 30 秒', () => {
    sock.start()
    for (let i = 0; i < 12; i++) {
      last().drop()
      vi.advanceTimersByTime(30_000)
    }
    const n = FakeSocket.instances.length
    last().drop()
    vi.advanceTimersByTime(29_999)
    expect(FakeSocket.instances).toHaveLength(n)
    vi.advanceTimersByTime(1)
    expect(FakeSocket.instances).toHaveLength(n + 1)
  })

  it('握手阶段失败通知外壳，已连上过的断线不通知', () => {
    sock.start()
    last().drop()
    expect(failed).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1000)
    last().open()
    last().drop()
    expect(failed).toHaveBeenCalledTimes(1)
  })

  it('stop 后不再重连', () => {
    sock.start()
    last().open()
    sock.stop()
    vi.advanceTimersByTime(60_000)
    expect(FakeSocket.instances).toHaveLength(1)
    expect(store.getState().connected).toBe(false)
  })
})
