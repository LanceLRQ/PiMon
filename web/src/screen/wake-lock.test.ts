import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WakeLockKeeper, type WakeLockSentinelLike } from './wake-lock'

class FakeSentinel implements WakeLockSentinelLike {
  released = false
  private listeners: (() => void)[] = []
  release = vi.fn(async () => {
    this.releaseByBrowser()
  })
  addEventListener(_type: 'release', fn: () => void) {
    this.listeners.push(fn)
  }
  // 浏览器侧释放（例如页面被隐藏）
  releaseByBrowser() {
    this.released = true
    this.listeners.forEach((l) => l())
  }
}

function fakeDoc(visible = true) {
  const target = new EventTarget()
  const doc = {
    visibilityState: (visible ? 'visible' : 'hidden') as DocumentVisibilityState,
    addEventListener: target.addEventListener.bind(target),
    removeEventListener: target.removeEventListener.bind(target),
  }
  return {
    doc,
    setVisible(v: boolean) {
      doc.visibilityState = v ? 'visible' : 'hidden'
      target.dispatchEvent(new Event('visibilitychange'))
    },
  }
}

function setup(visible = true) {
  const sentinels: FakeSentinel[] = []
  const request = vi.fn(async () => {
    const s = new FakeSentinel()
    sentinels.push(s)
    return s
  })
  const d = fakeDoc(visible)
  const keeper = new WakeLockKeeper({ nav: { wakeLock: { request } }, doc: d.doc, retryMs: 30_000 })
  return { keeper, request, sentinels, ...d }
}

describe('WakeLockKeeper', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('启动时页面可见就申请 screen 锁', async () => {
    const { keeper, request } = setup()
    keeper.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(1)
    expect(request).toHaveBeenCalledWith('screen')
  })

  it('页面不可见时不申请，重新可见时申请', async () => {
    const { keeper, request, setVisible } = setup(false)
    keeper.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(request).not.toHaveBeenCalled()
    setVisible(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('锁被浏览器释放（隐藏）后，重新可见时再申请；已持有时重复可见不重复申请', async () => {
    const { keeper, request, sentinels, setVisible } = setup()
    keeper.start()
    await vi.advanceTimersByTimeAsync(0)
    setVisible(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(1)

    setVisible(false)
    sentinels[0].releaseByBrowser()
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(1)
    setVisible(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(2)
  })

  it('页面仍可见时锁被释放，立即重新申请', async () => {
    const { keeper, request, sentinels } = setup()
    keeper.start()
    await vi.advanceTimersByTimeAsync(0)
    sentinels[0].releaseByBrowser()
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(2)
  })

  it('申请被拒绝时静默，按间隔重试直到成功', async () => {
    const d = fakeDoc()
    const sentinel = new FakeSentinel()
    const request = vi.fn().mockRejectedValueOnce(new Error('NotAllowedError')).mockResolvedValue(sentinel)
    const keeper = new WakeLockKeeper({ nav: { wakeLock: { request } }, doc: d.doc, retryMs: 30_000 })
    keeper.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(29_000)
    expect(request).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(request).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(request).toHaveBeenCalledTimes(2)
  })

  it('浏览器不支持 Wake Lock 时静默跳过', async () => {
    const d = fakeDoc()
    const keeper = new WakeLockKeeper({ nav: {}, doc: d.doc })
    expect(() => keeper.start()).not.toThrow()
    expect(() => keeper.stop()).not.toThrow()
  })

  it('停止后释放锁、移除监听并不再申请', async () => {
    const { keeper, request, sentinels, setVisible } = setup()
    keeper.start()
    await vi.advanceTimersByTimeAsync(0)
    keeper.stop()
    expect(sentinels[0].release).toHaveBeenCalled()
    setVisible(false)
    setVisible(true)
    await vi.advanceTimersByTimeAsync(120_000)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('申请在途中停止：到手的锁立即释放', async () => {
    const d = fakeDoc()
    const sentinel = new FakeSentinel()
    let resolve!: (s: FakeSentinel) => void
    const request = vi.fn(() => new Promise<FakeSentinel>((r) => (resolve = r)))
    const keeper = new WakeLockKeeper({ nav: { wakeLock: { request } }, doc: d.doc })
    keeper.start()
    keeper.stop()
    resolve(sentinel)
    await vi.advanceTimersByTimeAsync(0)
    expect(sentinel.release).toHaveBeenCalled()
  })
})
