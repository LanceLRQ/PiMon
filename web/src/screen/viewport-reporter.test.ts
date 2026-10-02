import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ClientMessage } from '@/types/protocol.generated'
import { ViewportReporter } from './viewport-reporter'

let size = { w: 1024, h: 600, dpr: 1 }
let coarse = false
let sent: ClientMessage[] = []
let resizeHandler: (() => void) | null = null

function make() {
  const r = new ViewportReporter({
    send: (m) => sent.push(m),
    read: () => size,
    coarse: () => coarse,
    onResize: (fn) => {
      resizeHandler = fn
      return () => {
        resizeHandler = null
      }
    },
  })
  r.start()
  return r
}

beforeEach(() => {
  vi.useFakeTimers()
  size = { w: 1024, h: 600, dpr: 1 }
  coarse = false
  sent = []
  resizeHandler = null
})
afterEach(() => vi.useRealTimers())

const resize = (w: number, h: number) => {
  size = { ...size, w, h }
  resizeHandler?.()
}

describe('viewport 上报', () => {
  it('连上后立即上报尺寸、dpr 与是否有触摸', () => {
    coarse = true
    size = { w: 1280, h: 720, dpr: 1.5 }
    const r = make()
    r.reportNow()
    expect(sent).toEqual([{ type: 'viewport_report', viewport: { w: 1280, h: 720, dpr: 1.5 }, coarse_pointer: true }])
    r.stop()
  })

  it('窗口尺寸变化防抖后只上报最后一次', () => {
    const r = make()
    resize(800, 480)
    vi.advanceTimersByTime(300)
    resize(1920, 1080)
    vi.advanceTimersByTime(300)
    resize(1280, 720)
    expect(sent).toHaveLength(0)
    vi.advanceTimersByTime(999)
    expect(sent).toHaveLength(0)
    vi.advanceTimersByTime(1)
    expect(sent).toEqual([{ type: 'viewport_report', viewport: { w: 1280, h: 720, dpr: 1 }, coarse_pointer: false }])
    r.stop()
  })

  it('由关屏转为亮屏 6 秒后补报一次（Ruling 37）', () => {
    const r = make()
    r.onScreenMode('off')
    r.onScreenMode('on')
    vi.advanceTimersByTime(5999)
    expect(sent).toHaveLength(0)
    vi.advanceTimersByTime(1)
    expect(sent).toHaveLength(1)
    expect(sent[0]).toMatchObject({ type: 'viewport_report', viewport: { w: 1024, h: 600 } })
    r.stop()
  })

  it('补报前又关屏则取消；持续亮屏或首次得知亮屏不补报', () => {
    const r = make()
    r.onScreenMode('on')
    vi.advanceTimersByTime(10_000)
    expect(sent).toHaveLength(0)
    r.onScreenMode('off')
    r.onScreenMode('on')
    vi.advanceTimersByTime(3000)
    r.onScreenMode('off')
    vi.advanceTimersByTime(10_000)
    expect(sent).toHaveLength(0)
    r.stop()
  })

  it('关屏期间的窗口变化不上报', () => {
    const r = make()
    r.onScreenMode('off')
    resize(1920, 1080)
    vi.advanceTimersByTime(5000)
    expect(sent).toHaveLength(0)
    r.stop()
  })

  it('当前 screen 变化时上报，相同的不重复，重连时随 viewport 一起补报', () => {
    const r = make()
    r.reportCurrentScreen('index')
    r.reportCurrentScreen('index')
    r.reportCurrentScreen('s1')
    expect(sent).toEqual([
      { type: 'viewport_report', current_screen: 'index' },
      { type: 'viewport_report', current_screen: 's1' },
    ])
    sent = []
    r.reportNow()
    expect(sent).toEqual([
      { type: 'viewport_report', viewport: { w: 1024, h: 600, dpr: 1 }, coarse_pointer: false, current_screen: 's1' },
    ])
    r.stop()
  })

  it('stop 后不再响应变化且没有残留计时器', () => {
    const r = make()
    resize(800, 480)
    r.onScreenMode('off')
    r.onScreenMode('on')
    r.stop()
    expect(resizeHandler).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
    vi.advanceTimersByTime(10_000)
    expect(sent).toHaveLength(0)
  })

  it('每次连上后 6 秒再补报一次（Ruling 50），重连同样如此', () => {
    const r = make()
    r.reportNow()
    expect(sent).toHaveLength(1)
    vi.advanceTimersByTime(5999)
    expect(sent).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(sent).toHaveLength(2)
    expect(sent[1]).toMatchObject({ viewport: { w: 1024, h: 600 } })
    r.reportNow()
    vi.advanceTimersByTime(6000)
    expect(sent).toHaveLength(4)
    r.stop()
  })

  it('连接补报前被关屏则取消', () => {
    const r = make()
    r.onScreenMode('on')
    r.reportNow()
    r.onScreenMode('off')
    vi.advanceTimersByTime(10_000)
    expect(sent).toHaveLength(1)
    r.stop()
  })

  it('唤醒后 5 秒内的窗口变化不排上报，到期补报读到最新尺寸', () => {
    const r = make()
    r.onScreenMode('off')
    r.onScreenMode('on')
    vi.advanceTimersByTime(1000)
    resize(1920, 1080)
    vi.advanceTimersByTime(1500)
    expect(sent).toHaveLength(0)
    // 冻结 5 秒结束后窗口变化恢复正常的防抖上报
    vi.advanceTimersByTime(2600)
    resize(1280, 720)
    vi.advanceTimersByTime(1000)
    expect(sent).toHaveLength(2)
    expect(sent[0]).toMatchObject({ viewport: { w: 1280, h: 720 } })
    r.stop()
  })
})
