import { act, render, screen, waitFor, within } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SessionProvider } from '@/app/session'
import { createI18n } from '@/i18n'
import type { Settings } from '@/types/generated'
import type { ClientMessage } from '@/types/protocol.generated'
import { AdminScreenPreview } from './AdminScreenPreview'
import { defaultScreens, layoutOf, makeData, setViewport, temp } from './test-utils'

class FakeSocket {
  static OPEN = 1
  static last: FakeSocket | null = null
  readyState = 1
  sent: ClientMessage[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor() {
    FakeSocket.last = this
  }
  send(raw: string) {
    this.sent.push(JSON.parse(raw) as ClientMessage)
  }
  close() {}
  receive(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) })
  }
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const settings = {
  language: 'zh', timezone: 'Asia/Shanghai', reduce_effects: false,
  screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'none', ui_scale: 1 },
} as unknown as Settings

const socketOpts = { createSocket: () => new FakeSocket() as unknown as WebSocket, getPageBuild: () => null }

async function mount(resolved: () => Response = () => json(200, layoutOf(defaultScreens(), 4))) {
  const i18n = await createI18n('zh')
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/session') return json(200, { authenticated: true, kind: 'admin', needs_setup: false })
      if (url === '/api/screens/resolved') return resolved()
      return json(404, { error: { code: 'not_found', details: {} } })
    }),
  )
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/screen']}>
        <SessionProvider>
          <AdminScreenPreview socket={socketOpts} />
        </SessionProvider>
      </MemoryRouter>
    </I18nextProvider>,
  )
}

beforeEach(() => {
  FakeSocket.last = null
  setViewport(1024, 600)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('管理员 /screen 预览', () => {
  it('取解析后的布局，连上后订阅 screen_data，管理员 snapshot 的数据渲染进网格，并保留「管理员预览」标识', async () => {
    await mount()
    await waitFor(() => expect(FakeSocket.last).not.toBeNull())
    const sock = FakeSocket.last!
    act(() => sock.onopen?.())
    expect(sock.sent.find((m) => m.type === 'subscribe')?.topics).toEqual(['instances', 'settings', 'layout', 'screen_state', 'screen_data'])
    act(() =>
      sock.receive({
        type: 'snapshot', build: 'b', server_time: new Date().toISOString(), role: 'admin', topics: [], instances: [], settings,
        screen_state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' },
        screen_data: [makeData([temp])],
      }),
    )
    expect(await screen.findByText('你好')).toBeInTheDocument()
    expect(screen.getByTestId('admin-preview-badge')).toHaveTextContent('管理员预览')
    // 网格从标识条下方开始，不被压住
    const badge = screen.getByTestId('admin-preview-badge').parentElement!
    expect(badge).toHaveClass('h-6')
    expect(document.querySelector<HTMLElement>('[data-screen-grid-area]')!.style.top).toBe('24px')
    expect(document.querySelector<HTMLElement>('[data-screen-grid]')!.style.height).toBe('576px')
    expect(screen.getByRole('link', { name: '返回管理界面' })).toHaveAttribute('href', '/')
  })

  it('不上报 viewport：连接、窗口变化与等待补报的时间过去之后都没有 viewport_report', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    await mount()
    await waitFor(() => expect(FakeSocket.last).not.toBeNull())
    const sock = FakeSocket.last!
    act(() => sock.onopen?.())
    act(() =>
      sock.receive({
        type: 'snapshot', build: 'b', server_time: new Date().toISOString(), role: 'admin', topics: [], instances: [], settings,
        screen_state: { mode: 'off', theme_id: 'ambient', reason: 'remote_off' },
      }),
    )
    act(() => {
      setViewport(800, 480)
      window.dispatchEvent(new Event('resize'))
    })
    act(() => {
      vi.advanceTimersByTime(15_000)
    })
    expect(sock.sent.filter((m) => m.type === 'viewport_report')).toEqual([])
    expect(sock.sent.map((m) => m.type).filter((t) => t !== 'ping')).toEqual(['subscribe'])
  })

  it('layout patch 后重新取解析布局并更新画面', async () => {
    let version = 4
    await mount(() => json(200, layoutOf(version === 4 ? defaultScreens() : [{ ...defaultScreens()[0], widgets: [] }], version)))
    await waitFor(() => expect(FakeSocket.last).not.toBeNull())
    const sock = FakeSocket.last!
    act(() => sock.onopen?.())
    act(() =>
      sock.receive({ type: 'snapshot', build: 'b', server_time: new Date().toISOString(), role: 'admin', topics: [], instances: [], settings, screen_state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' } }),
    )
    expect(await screen.findByText('你好')).toBeInTheDocument()
    version = 5
    act(() => sock.receive({ type: 'patch', server_time: new Date().toISOString(), entity: 'layout', layout: { version: 5 } }))
    await waitFor(() => expect(screen.queryByText('你好')).toBeNull())
  })

  it('取布局失败时显示失败提示，仍有预览标识', async () => {
    await mount(() => json(500, { error: { code: 'internal', details: {} } }))
    expect(await screen.findByText('预览加载失败')).toBeInTheDocument()
    expect(within(document.body).getByTestId('admin-preview-badge')).toBeInTheDocument()
    expect(FakeSocket.last).toBeNull()
  })

  it('实例变化的 patch 触发（合并后的）重取解析布局', async () => {
    let version = 4
    await mount(() => json(200, layoutOf(version === 4 ? defaultScreens() : [{ ...defaultScreens()[0], widgets: [] }], version)))
    await waitFor(() => expect(FakeSocket.last).not.toBeNull())
    const sock = FakeSocket.last!
    act(() => sock.onopen?.())
    act(() =>
      sock.receive({ type: 'snapshot', build: 'b', server_time: new Date().toISOString(), role: 'admin', topics: [], instances: [], settings, screen_state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' } }),
    )
    expect(await screen.findByText('你好')).toBeInTheDocument()
    version = 5
    act(() => sock.receive({ type: 'patch', server_time: new Date().toISOString(), entity: 'instance_state', instance: { id: 'i1' } }))
    await waitFor(() => expect(screen.queryByText('你好')).toBeNull(), { timeout: 3000 })
  })
})
