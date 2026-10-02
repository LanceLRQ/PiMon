import { act, render, screen } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SessionProvider } from '@/app/session'
import { createI18n } from '@/i18n'
import { ScreenApp } from './ScreenApp'
import { ScreenGate } from './ScreenGate'
import { screenStore } from './screen-store'
import type { SnapshotCache, StoredScreenData } from './snapshot-cache'
import { createScreenStore } from './screen-store'
import { snapshotOf } from './test-utils'

class DeadSocket {
  static OPEN = 1
  readyState = 3
  onopen: (() => void) | null = null
  onmessage: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor() {
    setTimeout(() => this.onclose?.(), 0)
  }
  send() {}
  close() {}
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function stored(): StoredScreenData {
  const store = createScreenStore()
  store.applySnapshot(snapshotOf({ server_time: '2026-10-01T06:00:00Z' }))
  const s = store.getState()
  return { v: 1, savedAt: Date.now(), lastDataAt: s.lastDataAt, settings: s.settings, layout: s.layout, screenState: s.screenState, data: s.data }
}

const nullCache: SnapshotCache = { save: vi.fn(async () => {}), load: async () => null, clear: async () => {} }

beforeEach(() => {
  screenStore.reset()
  vi.stubGlobal('WebSocket', DeadSocket)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('屏幕端应用：离线恢复', () => {
  it('hub 连不上时用本地缓存的 snapshot 渲染网格，并显示断线角标', async () => {
    const i18n = await createI18n('zh')
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { authenticated: true, kind: 'screen', needs_setup: false })))
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <SessionProvider>
            <ScreenApp initial={stored()} cache={nullCache} wakeLock={false} badgeGraceMs={20} />
          </SessionProvider>
        </MemoryRouter>
      </I18nextProvider>,
    )
    expect(await screen.findByText('你好')).toBeInTheDocument()
    expect(await screen.findByRole('status')).toHaveTextContent('连接中断，显示 14:00 的数据')
  })

  it('没有缓存且 hub 连不上：显示「hub 未运行」，不是空白', async () => {
    const i18n = await createI18n('zh')
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { authenticated: true, kind: 'screen', needs_setup: false })))
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <SessionProvider>
            <ScreenApp initial={null} cache={nullCache} wakeLock={false} badgeGraceMs={20} />
          </SessionProvider>
        </MemoryRouter>
      </I18nextProvider>,
    )
    expect(await screen.findByRole('heading', { name: 'hub 未运行' })).toBeInTheDocument()
  })
})

describe('屏幕端应用：握手被拒（会话被吊销）', () => {
  it('WebSocket 握手失败后重新查询会话，会话已失效则转为令牌失效页', async () => {
    const i18n = await createI18n('zh')
    let calls = 0
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url !== '/api/session') return json(404, {})
        calls++
        return calls === 1 ? json(200, { authenticated: true, kind: 'screen', needs_setup: false }) : json(200, { authenticated: false, needs_setup: false })
      }),
    )
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={['/screen']}>
          <SessionProvider>
            <ScreenGate cache={nullCache} wakeLock={false} />
          </SessionProvider>
        </MemoryRouter>
      </I18nextProvider>,
    )
    expect(await screen.findByRole('heading', { name: '屏幕令牌失效' })).toBeInTheDocument()
    expect(calls).toBeGreaterThanOrEqual(2)
    await act(async () => {})
  })
})
