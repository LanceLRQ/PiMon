import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createScreenStore } from './screen-store'
import { createScreenSession } from './session'
import { ScreenNavigator } from './state-machine'
import { defaultScreens, layoutOf, settingsOf, snapshotOf } from './test-utils'

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
}

const last = () => FakeSocket.instances[FakeSocket.instances.length - 1]

beforeEach(() => {
  vi.useFakeTimers()
  vi.stubGlobal('WebSocket', Object.assign(FakeSocket, { OPEN: 1 }))
  FakeSocket.instances = []
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: 600 })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function make(reload = vi.fn()) {
  const store = createScreenStore()
  const nav = new ScreenNavigator()
  nav.configure({
    screens: defaultScreens().map((s) => ({ id: s.id, dwellSeconds: 0, inRotation: true, widgetIds: [] })),
    carouselMode: 'auto',
    idleHomeSeconds: 60,
    defaultDwellSeconds: 15,
    touch: false,
  })
  const session = createScreenSession({
    store,
    nav,
    reload,
    socket: { createSocket: (u) => new FakeSocket(u) as unknown as WebSocket, getPageBuild: () => null },
  })
  return { store, nav, session, reload }
}

describe('屏幕会话装配', () => {
  it('连上后立即上报 viewport，snapshot 与 patch 写入屏幕 store', () => {
    const { store, session } = make()
    session.start()
    last().open()
    expect(store.getState().connected).toBe(true)
    expect(JSON.parse(last().sent[0])).toMatchObject({ type: 'viewport_report', viewport: { w: 1024, h: 600 } })
    last().receive(snapshotOf({ screen_settings: settingsOf(), resolved_layout: layoutOf(defaultScreens(), 4) }))
    expect(store.getState().layout?.version).toBe(4)
    session.stop()
  })

  it('screen_control switch 走状态机，refresh 整页刷新', () => {
    const { nav, session, reload } = make()
    session.start()
    last().open()
    last().receive({ type: 'screen_control', server_time: '2026-10-01T00:00:00Z', action: 'switch', screen_id: 's1', op_id: 1 })
    expect(nav.getState().screenId).toBe('s1')
    last().receive({ type: 'screen_control', server_time: '2026-10-01T00:00:00Z', action: 'refresh', op_id: 2 })
    expect(reload).toHaveBeenCalledTimes(1)
    session.stop()
  })

  it('窗口尺寸变化防抖后上报', () => {
    const { session } = make()
    session.start()
    last().open()
    const before = last().sent.length
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1280 })
    window.dispatchEvent(new Event('resize'))
    vi.advanceTimersByTime(1000)
    expect(last().sent.length).toBe(before + 1)
    expect(JSON.parse(last().sent[before])).toMatchObject({ viewport: { w: 1280, h: 600 } })
    session.stop()
  })

  it('重连后再次上报 viewport', () => {
    const { session } = make()
    session.start()
    last().open()
    last().onclose?.()
    vi.advanceTimersByTime(30_000)
    last().open()
    expect(last().sent.some((s) => s.includes('viewport_report'))).toBe(true)
    session.stop()
  })

  it('stop 后窗口变化不再上报', () => {
    const { session } = make()
    session.start()
    last().open()
    session.stop()
    const n = last().sent.length
    window.dispatchEvent(new Event('resize'))
    vi.advanceTimersByTime(5000)
    expect(last().sent.length).toBe(n)
  })
})
