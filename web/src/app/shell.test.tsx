import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { http } from '@/api/client'
import type { SessionInfo } from '@/api/session'
import { createI18n, languageStorageKey, type Language } from '@/i18n'
import { useSession } from './session'
import { liveStore } from '@/store/live-store'
import type { Settings } from '@/types/generated'
import type { Snapshot } from '@/types/protocol.generated'
import { AppRoutes } from './routes'
import { SessionProvider } from './session'

class IdleSocket {
  readyState = 0
  onopen = null
  onmessage = null
  onclose = null
  onerror = null
  send() {}
  close() {}
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function Probe() {
  const l = useLocation()
  return <div data-testid="loc">{l.pathname + l.search}</div>
}

const fetchMock = vi.fn()

interface Setup {
  session: SessionInfo
  path?: string
  lng?: Language
  redirectExternal?: (p: string) => void
}

async function mount({ session, path = '/', lng = 'zh', redirectExternal }: Setup) {
  fetchMock.mockImplementation(async (url: string) => {
    if (url === '/api/session') return json(200, session)
    if (url === '/api/logout') return new Response(null, { status: 204 })
    return json(404, { error: { code: 'not_found', details: {} } })
  })
  const i18n = await createI18n(lng)
  render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={[path]}>
        <SessionProvider redirectExternal={redirectExternal}>
          <AppRoutes />
          <Probe />
          <RefreshButton />
        </SessionProvider>
      </MemoryRouter>
    </I18nextProvider>,
  )
  return i18n
}

function RefreshButton() {
  const { refresh } = useSession()
  return <button onClick={() => void refresh()}>refresh-session</button>
}

const admin: SessionInfo = { authenticated: true, kind: 'admin', needs_setup: false }

describe('应用外壳与会话守卫', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('WebSocket', Object.assign(IdleSocket, { OPEN: 1 }))
    localStorage.clear()
    liveStore.reset()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('需要首次设置时跳到 /setup', async () => {
    await mount({ session: { authenticated: false, needs_setup: true }, path: '/instances' })
    expect(await screen.findByRole('heading', { name: '首次设置' })).toBeInTheDocument()
    expect(screen.getByTestId('loc')).toHaveTextContent('/setup')
  })

  it('未登录时跳到 /login 并记住原页面', async () => {
    await mount({ session: { authenticated: false, needs_setup: false }, path: '/instances/new' })
    expect(await screen.findByRole('heading', { name: '登录管理界面' })).toBeInTheDocument()
    expect(screen.getByTestId('loc')).toHaveTextContent('/login?next=%2Finstances%2Fnew')
  })

  it('未登录访问根路径时不带 next', async () => {
    await mount({ session: { authenticated: false, needs_setup: false } })
    await screen.findByRole('heading', { name: '登录管理界面' })
    expect(screen.getByTestId('loc')).toHaveTextContent(/^\/login$/)
  })

  it('屏幕会话访问管理页时跳 /screen', async () => {
    const redirect = vi.fn()
    await mount({ session: { authenticated: true, kind: 'screen', needs_setup: false }, path: '/settings', redirectExternal: redirect })
    await waitFor(() => expect(redirect).toHaveBeenCalledWith('/screen'))
    expect(screen.queryByRole('heading', { name: '设置' })).not.toBeInTheDocument()
  })

  it('屏幕会话访问 /screen：不再跳转，显示「屏幕端将在 M1d 提供」占位且没有管理外壳', async () => {
    const redirect = vi.fn()
    await mount({ session: { authenticated: true, kind: 'screen', needs_setup: false }, path: '/screen', redirectExternal: redirect })
    expect(await screen.findByText('屏幕端将在 M1d 提供')).toBeInTheDocument()
    expect(redirect).not.toHaveBeenCalled()
    expect(screen.queryByRole('complementary', { name: '主导航' })).not.toBeInTheDocument()
  })

  it('屏幕会话占位页英文界面', async () => {
    await mount({ session: { authenticated: true, kind: 'screen', needs_setup: false }, path: '/screen', lng: 'en' })
    expect(await screen.findByText('The screen app arrives in M1d')).toBeInTheDocument()
  })

  it('屏幕会话访问 /screens（管理页，与 /screen 前缀相近）仍跳 /screen', async () => {
    const redirect = vi.fn()
    await mount({ session: { authenticated: true, kind: 'screen', needs_setup: false }, path: '/screens', redirectExternal: redirect })
    await waitFor(() => expect(redirect).toHaveBeenCalledWith('/screen'))
  })

  it.each(['/login?next=%2F%5Cevil.com', '/login?next=%2F%5C%5Cevil.com', '/login?next=%2F%09%2Fevil.com'])(
    '已登录的管理员访问 %s 时回根路径',
    async (path) => {
      await mount({ session: admin, path })
      await screen.findByRole('heading', { name: '总览' })
      expect(screen.getByTestId('loc')).toHaveTextContent(/^\/$/)
    },
  )

  it('管理员会话显示外壳与页面', async () => {
    await mount({ session: admin, path: '/proxies' })
    expect(await screen.findByRole('heading', { name: '代理' })).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: '主导航' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /代理/, current: 'page' })).toBeInTheDocument()
  })

  it('已登录的管理员访问 /login 时回到 next 指定页面，next 非站内路径时回根', async () => {
    await mount({ session: admin, path: '/login?next=%2Finstances' })
    await screen.findByRole('heading', { name: '监控实例' })
    expect(screen.getByTestId('loc')).toHaveTextContent('/instances')
  })

  it('已登录的管理员访问 /login?next=//evil.com 时回根路径', async () => {
    await mount({ session: admin, path: '/login?next=%2F%2Fevil.com' })
    await screen.findByRole('heading', { name: '总览' })
    expect(screen.getByTestId('loc')).toHaveTextContent(/^\/$/)
  })

  it('已完成设置时访问 /setup 跳到登录页', async () => {
    await mount({ session: { authenticated: false, needs_setup: false }, path: '/setup' })
    await screen.findByRole('heading', { name: '登录管理界面' })
  })

  it('页面运行中任意 API 返回 401：跳登录页并记住当前页面', async () => {
    await mount({ session: admin, path: '/instances' })
    await screen.findByRole('heading', { name: '监控实例' })
    fetchMock.mockImplementation(async () => json(401, { error: { code: 'auth.required', details: {} } }))
    await expect(http.get('/api/instances')).rejects.toMatchObject({ code: 'auth.required' })
    await screen.findByRole('heading', { name: '登录管理界面' })
    expect(screen.getByTestId('loc')).toHaveTextContent('/login?next=%2Finstances')
  })

  it('会话查询失败时给出重试入口', async () => {
    fetchMock.mockRejectedValue(new TypeError('down'))
    const i18n = await createI18n('zh')
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <SessionProvider>
            <AppRoutes />
          </SessionProvider>
        </MemoryRouter>
      </I18nextProvider>,
    )
    expect(await screen.findByRole('alert')).toHaveTextContent('无法获取登录状态')
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('已登录后会话刷新失败（hub 暂时不可达）：外壳保持，不显示会话错误页', async () => {
    await mount({ session: admin, path: '/proxies' })
    await screen.findByRole('heading', { name: '代理' })
    fetchMock.mockRejectedValue(new TypeError('down'))
    await userEvent.click(screen.getByRole('button', { name: 'refresh-session' }))
    await waitFor(() => expect(fetchMock.mock.calls.filter(([u]) => u === '/api/session').length).toBeGreaterThan(1))
    await Promise.resolve()
    expect(screen.getByRole('heading', { name: '代理' })).toBeInTheDocument()
    expect(screen.queryByText(/无法获取登录状态/)).not.toBeInTheDocument()
  })

  it('手机端退出登录失败时显示错误', async () => {
    await mount({ session: admin })
    const bar = await screen.findByRole('navigation', { name: '手机导航' })
    fetchMock.mockImplementation(async (u: string) =>
      u === '/api/logout' ? json(500, { error: { code: 'internal', details: {} } }) : json(200, admin),
    )
    await userEvent.click(within(bar).getByRole('button', { name: '更多' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: '退出登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('中枢内部错误')
  })

  it('退出登录：请求 /api/logout 后回到登录页', async () => {
    await mount({ session: admin })
    await userEvent.click(await screen.findByRole('button', { name: '退出登录' }))
    await screen.findByRole('heading', { name: '登录管理界面' })
    expect(fetchMock.mock.calls.some(([u, init]) => u === '/api/logout' && init.method === 'POST')).toBe(true)
  })
})

describe('侧栏', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('WebSocket', Object.assign(IdleSocket, { OPEN: 1 }))
    localStorage.clear()
    liveStore.reset()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('中英切换后侧栏文字随之变化，并写入 localStorage', async () => {
    await mount({ session: admin })
    const side = await screen.findByRole('complementary', { name: '主导航' })
    expect(within(side).getByText('总览')).toBeInTheDocument()
    await userEvent.click(within(side).getByRole('radio', { name: 'English' }))
    expect(await screen.findByRole('complementary', { name: 'Main navigation' })).toBeInTheDocument()
    const en = screen.getByRole('complementary')
    expect(within(en).getByText('Overview')).toBeInTheDocument()
    expect(within(en).queryByText('总览')).not.toBeInTheDocument()
    expect(localStorage.getItem(languageStorageKey)).toBe('en')
    await userEvent.click(within(en).getByRole('radio', { name: '中文' }))
    expect(await within(screen.getByRole('complementary')).findByText('总览')).toBeInTheDocument()
  })

  it('后续里程碑入口灰显并标 m2/m4；屏幕与实例有二级菜单', async () => {
    await mount({ session: admin, path: '/instances/new' })
    const side = await screen.findByRole('complementary', { name: '主导航' })
    const devices = within(side).getByRole('button', { name: /设备/ })
    expect(devices).toBeDisabled()
    expect(devices).toHaveTextContent('m2')
    expect(within(side).getByRole('button', { name: /告警规则/ })).toHaveTextContent('m4')
    expect(within(side).getByRole('button', { name: /通知渠道/ })).toHaveTextContent('m4')
    for (const name of ['屏幕管理', '布局编辑器', '时段计划与主题', '远程操作', '实例列表', '新建实例']) {
      expect(within(side).getByRole('link', { name: new RegExp(name) })).toBeInTheDocument()
    }
    expect(within(side).getByRole('link', { name: /新建实例/, current: 'page' })).toBeInTheDocument()
  })

  it('主题切换：切到深色后 html 带 dark 类并写入存储', async () => {
    document.documentElement.classList.remove('dark')
    await mount({ session: admin })
    const side = await screen.findByRole('complementary', { name: '主导航' })
    await userEvent.click(within(side).getByRole('radio', { name: '深色' }))
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('pimon.admin.theme')).toBe('dark')
    await userEvent.click(within(side).getByRole('radio', { name: '浅色' }))
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('手机底栏：总览、屏幕、实例、设置、更多；「更多」菜单可进入代理页', async () => {
    await mount({ session: admin, path: '/instances' })
    const bar = await screen.findByRole('navigation', { name: '手机导航' })
    const labels = within(bar)
      .getAllByRole('link')
      .map((l) => l.textContent)
    expect(labels).toEqual(['总览', '屏幕', '实例', '设置'])
    expect(within(bar).getByRole('link', { name: '实例', current: 'page' })).toBeInTheDocument()
    await userEvent.click(within(bar).getByRole('button', { name: '更多' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: '代理' }))
    await screen.findByRole('heading', { name: '代理' })
    expect(screen.getByTestId('loc')).toBeInTheDocument()
  })

  describe('界面语言来源', () => {
    const snapshot = (language: string): Snapshot => ({
      type: 'snapshot',
      build: 'b',
      server_time: new Date().toISOString(),
      role: 'admin',
      topics: ['settings'],
      settings: { language } as Settings,
      instances: [],
    })

    it('本浏览器没有覆盖时，跟随全局设置的 language', async () => {
      await mount({ session: admin })
      await screen.findByRole('complementary', { name: '主导航' })
      act(() => liveStore.applySnapshot(snapshot('en')))
      expect(await screen.findByRole('complementary', { name: 'Main navigation' })).toBeInTheDocument()
    })

    it('本浏览器已选过语言时，全局设置不覆盖', async () => {
      localStorage.setItem(languageStorageKey, 'zh')
      await mount({ session: admin })
      await screen.findByRole('complementary', { name: '主导航' })
      act(() => liveStore.applySnapshot(snapshot('en')))
      await Promise.resolve()
      expect(screen.getByRole('complementary', { name: '主导航' })).toBeInTheDocument()
    })
  })
})
