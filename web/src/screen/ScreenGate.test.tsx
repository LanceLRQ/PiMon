import { act, render, screen, waitFor } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionInfo } from '@/api/session'
import { SessionProvider } from '@/app/session'
import { createI18n } from '@/i18n'
import { ScreenGate } from './ScreenGate'
import type { SnapshotCache, StoredScreenData } from './snapshot-cache'
import { createScreenStore } from './screen-store'
import { snapshotOf } from './test-utils'

vi.mock('./AdminScreenPreview', () => ({
  AdminScreenPreview: () => <div data-testid="admin-preview" />,
}))

vi.mock('./ScreenApp', () => ({
  ScreenApp: (props: { initial?: StoredScreenData | null }) => (
    <div data-testid="screen-app" data-restored={props.initial ? 'yes' : 'no'} />
  ),
}))

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const fetchMock = vi.fn()

interface Routes {
  session: () => Response | Promise<Response>
  setupCode?: () => Response | Promise<Response>
}

function route(r: Routes) {
  fetchMock.mockImplementation(async (url: string) => {
    if (url === '/api/session') return r.session()
    if (url === '/api/screen/setup-code') return r.setupCode ? r.setupCode() : json(404, { error: { code: 'not_found', details: {} } })
    return json(404, { error: { code: 'not_found', details: {} } })
  })
}

const sess = (over: Partial<SessionInfo>): SessionInfo => ({ authenticated: true, kind: 'screen', needs_setup: false, ...over })

function storedFromSnapshot(): StoredScreenData {
  const store = createScreenStore()
  store.applySnapshot(snapshotOf())
  const s = store.getState()
  return { v: 1, savedAt: Date.now(), lastDataAt: s.lastDataAt, settings: s.settings, layout: s.layout, screenState: s.screenState, data: s.data }
}

const emptyCache: SnapshotCache = { save: async () => {}, load: async () => null, clear: async () => {} }

async function mount(opts: { cache?: SnapshotCache; lng?: 'zh' | 'en'; pollMs?: number } = {}) {
  const i18n = await createI18n(opts.lng ?? 'zh')
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/screen']}>
        <SessionProvider>
          <ScreenGate cache={opts.cache ?? emptyCache} pollMs={opts.pollMs ?? 20} />
        </SessionProvider>
      </MemoryRouter>
    </I18nextProvider>,
  )
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => vi.unstubAllGlobals())

describe('屏幕端守卫', () => {
  it('正常的屏幕会话进入屏幕端应用', async () => {
    route({ session: () => json(200, sess({})) })
    await mount()
    expect(await screen.findByTestId('screen-app')).toBeInTheDocument()
  })

  it('没有会话：显示令牌失效页，不跳登录页', async () => {
    route({ session: () => json(200, { authenticated: false, needs_setup: false }) })
    await mount()
    expect(await screen.findByRole('heading', { name: '屏幕令牌失效' })).toBeInTheDocument()
    expect(screen.getByText(/本机 kiosk 会在几秒内自动用新令牌重新登录/)).toBeInTheDocument()
    expect(screen.queryByTestId('screen-app')).toBeNull()
  })

  it('令牌失效时清除本地存档；正常会话不清', async () => {
    const clear = vi.fn(async () => {})
    const cache: SnapshotCache = { save: async () => {}, load: async () => null, clear }
    route({ session: () => json(200, sess({})) })
    const view = await mount({ cache })
    await screen.findByTestId('screen-app')
    expect(clear).not.toHaveBeenCalled()
    view.unmount()

    route({ session: () => json(200, { authenticated: false, needs_setup: false }) })
    await mount({ cache })
    await screen.findByRole('heading', { name: '屏幕令牌失效' })
    await waitFor(() => expect(clear).toHaveBeenCalledTimes(1))
  })

  it('令牌失效页有英文文案', async () => {
    route({ session: () => json(200, { authenticated: false, needs_setup: false }) })
    await mount({ lng: 'en' })
    expect(await screen.findByRole('heading', { name: 'Screen token is no longer valid' })).toBeInTheDocument()
  })

  it('会话在使用中被吊销：重新查询会话后转为失效页', async () => {
    let revoked = false
    route({ session: () => json(200, revoked ? { authenticated: false, needs_setup: false } : sess({})) })
    await mount()
    await screen.findByTestId('screen-app')
    revoked = true
    // 模拟 WebSocket 握手被拒后外壳重新查询会话：这里直接触发 401
    const { request } = await import('@/api/client')
    fetchMock.mockImplementationOnce(async () => json(401, { error: { code: 'auth.required', details: {} } }))
    await act(async () => {
      await request('/api/x').catch(() => {})
    })
    expect(await screen.findByRole('heading', { name: '屏幕令牌失效' })).toBeInTheDocument()
  })

  it('管理员会话打开 /screen：进入管理员预览，不渲染屏幕端应用', async () => {
    route({ session: () => json(200, sess({ kind: 'admin' })) })
    await mount()
    expect(await screen.findByTestId('admin-preview')).toBeInTheDocument()
    expect(screen.queryByTestId('screen-app')).toBeNull()
  })

  it('还没有管理员且是屏幕会话：显示设置码与手机访问地址', async () => {
    route({
      session: () => json(200, sess({ needs_setup: true })),
      setupCode: () => json(200, { code: 'ABCD-1234', expires_at: '2099-01-01T00:00:00Z' }),
    })
    await mount()
    expect(await screen.findByText('ABCD-1234')).toBeInTheDocument()
    // 地址取当前页面 host；jsdom 默认 localhost（回环）时提示用树莓派地址
    expect(screen.getByText(/用手机打开/)).toBeInTheDocument()
    expect(screen.getByText(/http:\/\/.+:\d+/)).toBeInTheDocument()
    expect(screen.queryByTestId('screen-app')).toBeNull()
  })

  it('hub 给出的候选地址大字显示第一个，其余小字，不再用占位', async () => {
    route({
      session: () => json(200, sess({ needs_setup: true })),
      setupCode: () =>
        json(200, { code: 'ABCD-1234', expires_at: '2099-01-01T00:00:00Z', urls: ['http://192.168.1.20:31415', 'http://10.0.0.5:31415'] }),
    })
    await mount()
    const big = await screen.findByText(/用手机打开 http:\/\/192\.168\.1\.20:31415 完成设置/)
    expect(big).toBeInTheDocument()
    expect(screen.queryByText(/树莓派地址/)).toBeNull()
    expect(screen.getByText(/也可以试试：http:\/\/10\.0\.0\.5:31415/)).toBeInTheDocument()
  })

  it('会话刷新失败时轮询不会停：之后仍继续取设置码', async () => {
    let sessionCalls = 0
    let codeCalls = 0
    route({
      session: () => {
        sessionCalls++
        // 1 初始、2 第一轮、3 第二轮（报告设置已完成）→ 4 是 refresh 本身，失败；之后恢复 needs_setup
        if (sessionCalls === 4) return Promise.reject(new TypeError('Failed to fetch'))
        return json(200, sess({ needs_setup: sessionCalls !== 3 }))
      },
      setupCode: () => {
        codeCalls++
        return json(200, { code: 'ABCD-1234', expires_at: '2099-01-01T00:00:00Z', urls: [] })
      },
    })
    await mount()
    await screen.findByText('ABCD-1234')
    await waitFor(() => expect(sessionCalls).toBeGreaterThanOrEqual(6))
    const before = codeCalls
    await waitFor(() => expect(codeCalls).toBeGreaterThan(before))
  })

  it('设置码接口 404（无有效码）时提示等待而不是报错', async () => {
    route({ session: () => json(200, sess({ needs_setup: true })) })
    await mount()
    expect(await screen.findByText(/设置码暂不可用/)).toBeInTheDocument()
  })

  it('轮询：设置码刷新后显示新码；管理员设置完成后自动进入屏幕', async () => {
    let step = 0
    route({
      session: () => json(200, sess({ needs_setup: step < 3 })),
      setupCode: () => {
        step++
        return json(200, { code: step === 1 ? 'AAAA-1111' : 'BBBB-2222', expires_at: '2099-01-01T00:00:00Z' })
      },
    })
    await mount()
    expect(await screen.findByText('AAAA-1111')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('BBBB-2222')).toBeInTheDocument())
    expect(await screen.findByTestId('screen-app')).toBeInTheDocument()
    expect(screen.queryByText('BBBB-2222')).toBeNull()
  })

  it('需要设置但没有会话：显示令牌失效页（取不到设置码）', async () => {
    route({ session: () => json(200, { authenticated: false, needs_setup: true }) })
    await mount()
    expect(await screen.findByRole('heading', { name: '屏幕令牌失效' })).toBeInTheDocument()
  })
})

describe('屏幕端守卫：hub 不可达', () => {
  it('会话查询失败、没有本地缓存：显示「hub 未运行」页，hub 恢复后自动进入', async () => {
    let up = false
    route({
      session: () => (up ? json(200, sess({})) : Promise.reject(new TypeError('Failed to fetch'))),
    })
    await mount()
    expect(await screen.findByRole('heading', { name: 'hub 未运行' })).toBeInTheDocument()
    up = true
    expect(await screen.findByTestId('screen-app')).toBeInTheDocument()
  })

  it('会话查询失败但有缓存的 snapshot：带着缓存进入屏幕端应用', async () => {
    route({ session: () => Promise.reject(new TypeError('Failed to fetch')) })
    const cache: SnapshotCache = { save: async () => {}, load: async () => storedFromSnapshot(), clear: async () => {} }
    await mount({ cache })
    const app = await screen.findByTestId('screen-app')
    expect(app).toHaveAttribute('data-restored', 'yes')
    expect(screen.queryByRole('heading', { name: 'hub 未运行' })).toBeNull()
  })

  it('正常在线时也把缓存传给屏幕端应用（冷启动先显示上次数据）', async () => {
    route({ session: () => json(200, sess({})) })
    const cache: SnapshotCache = { save: async () => {}, load: async () => storedFromSnapshot(), clear: async () => {} }
    await mount({ cache })
    expect(await screen.findByTestId('screen-app')).toHaveAttribute('data-restored', 'yes')
  })
})
