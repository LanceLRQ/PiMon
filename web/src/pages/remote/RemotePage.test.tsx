import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, json, mockApi, renderWithApp, seedStore, type ApiHandler, type Req } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { LayoutState, ScreenOp, ScreenState, ScreenStatus } from '@/types/generated'
import { RemotePage } from './RemotePage'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

const layoutOf = (): LayoutState => ({
  version: 4, source: 'edit', created_at: '2026-10-01T00:00:00Z', broken: [],
  layout: {
    grid: { cols: 8, rows: 5 },
    screens: [
      { id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets: [] },
      { id: 's1', name: '主机', dwell_seconds: 0, in_rotation: true, widgets: [] },
      { id: 's2', name: '网络', dwell_seconds: 0, in_rotation: true, widgets: [] },
    ],
  },
})
const stateOf = (over: Partial<ScreenState> = {}): ScreenState => ({ mode: 'on', theme_id: 'ambient', reason: 'schedule', next_change: '2026-10-01T15:00:00Z', ...over })
const statusOf = (over: Partial<ScreenStatus> = {}): ScreenStatus => ({
  state: stateOf(), viewport: { w: 1024, h: 600, dpr: 1 }, coarse_pointer: false, current_screen: 's1', online: true, ...over,
})
const op = (id: number, action: string, over: Partial<ScreenOp> = {}): ScreenOp => ({
  id, action, params: {}, client_ip: '192.168.1.35', delivered: true, at: '2026-10-01T14:40:00Z', ...over,
})

function seed(state: ScreenState = stateOf()) {
  seedStore([])
  liveStore.applySnapshot({
    type: 'snapshot', build: 'b', role: 'admin', topics: [], server_time: '2026-10-01T14:47:00Z', instances: [],
    settings: { reduce_effects: false, timezone: 'Asia/Shanghai', access_url: '', screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1 } },
    layout: layoutOf(),
    screen_state: state,
  } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
}

interface Opts {
  status?: ScreenStatus
  ops?: ScreenOp[]
  over?: ApiHandler
}
async function mount(o: Opts = {}) {
  let ops = o.ops ?? []
  const api = mockApi((req) => {
    const custom = o.over?.(req)
    if (custom) return custom
    const path = req.url.split('?')[0]
    if (req.method === 'GET' && path === '/api/screen/status') return json(200, o.status ?? statusOf())
    if (req.method === 'GET' && path === '/api/screen/ops') return json(200, ops)
    if (req.method === 'POST' && path === '/api/screen/control') {
      const body = req.body as { action: string; screen_id?: string; minutes?: number }
      const params = body.action === 'switch' ? { screen_id: body.screen_id } : body.action === 'wake' ? { minutes: body.minutes } : {}
      const created = op(100 + ops.length, body.action, { params, at: '2026-10-01T14:47:00Z', delivered: body.action !== 'refresh' || (o.status ?? statusOf()).online })
      ops = [created, ...ops]
      return json(200, { op: created, state: stateOf() })
    }
    if (req.method === 'POST' && path === '/api/screen/token/reset') {
      ops = [op(200, 'token_reset', { at: '2026-10-01T14:47:00Z' }), ...ops]
      return new Response(null, { status: 204 })
    }
    return undefined
  })
  await renderWithApp(<RemotePage />)
  await screen.findByTestId('remote-state')
  return api
}
const posts = (calls: Req[], url: string) => calls.filter((c) => c.method === 'POST' && c.url === url)

describe('远程操作页', () => {
  it('当前状态卡：显示中、当前 screen、主题、下一次变化（按设置时区）与输入方式', async () => {
    seed()
    await mount()
    const st = screen.getByTestId('remote-state')
    expect(within(st).getByText('显示中')).toBeInTheDocument()
    expect(within(st).getByText(/s1 · ambient/)).toBeInTheDocument()
    // 15:00Z 在 Asia/Shanghai 是 23:00
    expect(within(st).getByText(/下一次变化 23:00/)).toBeInTheDocument()
    const kv = screen.getByTestId('remote-kv')
    expect(within(kv).getByText('在线 · 1024×600')).toBeInTheDocument()
    expect(within(kv).getByText('无触摸')).toBeInTheDocument()
    for (const k of ['k1', 'k2', 'k3', 'k4', 'k5', 'k6']) expect(screen.getByTestId(`key-${k}`)).toBeInTheDocument()
  })

  it('远程开屏与关屏的文案说明持续到下一个时段边界，并显示 until', async () => {
    seed(stateOf({ reason: 'remote_off', mode: 'off', until: '2026-10-01T15:00:00Z', next_change: '2026-10-01T15:00:00Z' }))
    await mount({ status: statusOf({ state: stateOf({ reason: 'remote_off', mode: 'off', until: '2026-10-01T15:00:00Z' }) }) })
    const st = screen.getByTestId('remote-state')
    expect(within(st).getByText('已关屏')).toBeInTheDocument()
    expect(within(st).getByText(/远程关屏中，持续到 23:00（下一个时段边界）/)).toBeInTheDocument()
    expect(screen.getByTestId('key-k3')).toHaveTextContent('直到时段计划的下一个边界')
    expect(screen.getByTestId('key-k4')).toHaveTextContent('直到时段计划的下一个边界')
  })

  it('临时亮屏显示到期时刻并说明到期后回到计划，临时亮屏行显示「至 HH:MM」', async () => {
    seed(stateOf({ reason: 'wake', until: '2026-10-01T15:17:00Z' }))
    await mount({ status: statusOf({ state: stateOf({ reason: 'wake', until: '2026-10-01T15:17:00Z' }) }) })
    expect(within(screen.getByTestId('remote-state')).getByText(/临时亮屏中，23:17 到期后回到时段计划/)).toBeInTheDocument()
    expect(within(screen.getByTestId('remote-kv')).getByText('至 23:17')).toBeInTheDocument()
  })

  it('点击 k3 开屏写入操作记录并刷新记录列表，操作记录只取最近 5 条', async () => {
    seed()
    const user = userEvent.setup()
    const api = await mount({ ops: [op(1, 'off'), op(2, 'refresh')] })
    expect(api.calls.some((c) => c.url === '/api/screen/ops?limit=5')).toBe(true)
    expect(within(screen.getByTestId('remote-log')).getAllByRole('row')).toHaveLength(2)
    await user.click(screen.getByTestId('key-k3'))
    await waitFor(() => expect(posts(api.calls, '/api/screen/control').at(-1)?.body).toEqual({ action: 'on' }))
    await waitFor(() => expect(within(screen.getByTestId('remote-log')).getAllByRole('row')).toHaveLength(3))
    expect(within(screen.getByTestId('remote-log')).getAllByRole('row')[0]).toHaveTextContent('开屏')
  })

  it('k4 关屏、k1 刷新、k2 切换到所选 screen、k5 按所选分钟临时亮屏', async () => {
    seed()
    const user = userEvent.setup()
    const api = await mount()
    await user.click(screen.getByTestId('key-k4'))
    await waitFor(() => expect(posts(api.calls, '/api/screen/control').at(-1)?.body).toEqual({ action: 'off' }))
    await user.click(screen.getByTestId('key-k1'))
    await waitFor(() => expect(posts(api.calls, '/api/screen/control').at(-1)?.body).toEqual({ action: 'refresh' }))
    await user.selectOptions(screen.getByRole('combobox', { name: '目标 screen' }), 's2')
    await user.click(screen.getByRole('button', { name: '切换' }))
    await waitFor(() => expect(posts(api.calls, '/api/screen/control').at(-1)?.body).toEqual({ action: 'switch', screen_id: 's2' }))
    await user.click(screen.getByRole('radio', { name: '15' }))
    await user.click(screen.getByRole('button', { name: '亮屏' }))
    await waitFor(() => expect(posts(api.calls, '/api/screen/control').at(-1)?.body).toEqual({ action: 'wake', minutes: 15 }))
    const rows = within(screen.getByTestId('remote-log')).getAllByRole('row')
    expect(rows).toHaveLength(4)
    expect(rows[0]).toHaveTextContent('临时亮屏')
    expect(rows[0]).toHaveTextContent('15 分钟')
    expect(rows[1]).toHaveTextContent('切换 screen')
    expect(rows[1]).toHaveTextContent('s2')
  })

  it('显示器离线：刷新与切换不可用，不显示 current_screen，开关屏仍可用', async () => {
    seed()
    await mount({ status: statusOf({ online: false, current_screen: 's1', last_seen: '2026-10-01T14:00:00Z' }) })
    expect(screen.getByTestId('key-k1')).toBeDisabled()
    expect(within(screen.getByTestId('remote-kv')).queryByText('s1')).toBeNull()
    expect(within(screen.getByTestId('remote-state')).getByText('显示器离线')).toBeInTheDocument()
    expect(within(screen.getByTestId('remote-state')).queryByText(/s1/)).toBeNull()
    expect(screen.getByRole('button', { name: '切换' })).toBeDisabled()
    expect(screen.getByTestId('key-k3')).toBeEnabled()
  })

  it('操作记录里一次性指令按送达与否标注，状态类操作不标', async () => {
    seed()
    await mount({ ops: [op(3, 'refresh', { delivered: false }), op(2, 'switch', { delivered: true, params: { screen_id: 's1' } }), op(1, 'on')] })
    const rows = within(screen.getByTestId('remote-log')).getAllByRole('row')
    expect(rows[0]).toHaveTextContent('当时未送达')
    // 显示器已重新在线：历史上的未送达不再用警示色
    expect(rows[0].querySelector('.border-status-warn')).toBeNull()
    expect(rows[1]).toHaveTextContent('已下发')
    expect(rows[2]).not.toHaveTextContent('已下发')
    expect(rows[2]).not.toHaveTextContent('未送达')
  })

  it('显示器仍离线时，未送达的一次性指令用警示色标注', async () => {
    seed()
    await mount({ status: statusOf({ online: false }), ops: [op(3, 'refresh', { delivered: false })] })
    expect(within(screen.getByTestId('remote-log')).getAllByRole('row')[0].querySelector('.border-status-warn')).not.toBeNull()
  })

  it('没有 until 时不退回「现在」，改用不带时刻的文案', async () => {
    seed(stateOf({ reason: 'remote_on', next_change: undefined }))
    await mount({ status: statusOf({ state: stateOf({ reason: 'remote_on', next_change: undefined }) }) })
    const st = screen.getByTestId('remote-state')
    expect(within(st).getByText('远程开屏中，持续到下一个时段边界，之后回到时段计划。')).toBeInTheDocument()
    expect(st).not.toHaveTextContent('23:')
  })

  it('离线时 k2（切换 screen）整块置灰，与 k1 一致', async () => {
    seed()
    await mount({ status: statusOf({ online: false }) })
    expect(screen.getByTestId('key-k2')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByTestId('key-k2').className).toContain('opacity-50')
    expect(screen.getByRole('combobox', { name: '目标 screen' })).toBeDisabled()
  })

  it('相对时间随时间推移自动刷新，不需要重新取数', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'], now: Date.parse('2026-10-01T14:47:00Z') })
    try {
      seed()
      await mount()
      expect(within(screen.getByTestId('remote-state')).getByText(/（13 分钟后）/)).toBeInTheDocument()
      act(() => void vi.advanceTimersByTime(5 * 60_000))
      expect(within(screen.getByTestId('remote-state')).getByText(/（8 分钟后）/)).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('k6 二次确认：打开对话框说明后果但不发请求；取消不发；确认后重置并给出新链接的获取方式，记录刷新', async () => {
    seed()
    const user = userEvent.setup()
    const api = await mount()
    await user.click(screen.getByTestId('key-k6'))
    const dlg = await screen.findByRole('alertdialog')
    expect(within(dlg).getByText(/旧令牌立即失效/)).toBeInTheDocument()
    expect(within(dlg).getByText(/kiosk 需要用新令牌链接重新登录/)).toBeInTheDocument()
    expect(posts(api.calls, '/api/screen/token/reset')).toHaveLength(0)
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(posts(api.calls, '/api/screen/token/reset')).toHaveLength(0)

    await user.click(screen.getByTestId('key-k6'))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '重置令牌' }))
    await waitFor(() => expect(posts(api.calls, '/api/screen/token/reset')).toHaveLength(1))
    const done = await screen.findByTestId('token-done')
    expect(done).toHaveTextContent('screen.token')
    expect(done).toHaveTextContent('/screen/auth?token=')
    await user.click(screen.getByRole('button', { name: '完成' }))
    await waitFor(() => expect(within(screen.getByTestId('remote-log')).getAllByRole('row')[0]).toHaveTextContent('重置屏幕令牌'))
  })

  it('重置令牌失败时提示错误并保持对话框，不显示成功说明', async () => {
    seed()
    const user = userEvent.setup()
    await mount({ over: (req) => (req.url === '/api/screen/token/reset' ? apiError(500, 'internal') : undefined) })
    await user.click(screen.getByTestId('key-k6'))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '重置令牌' }))
    expect(await screen.findByText('中枢内部错误')).toBeInTheDocument()
    expect(screen.queryByTestId('token-done')).toBeNull()
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  })

  it('手机宽度：状态行之后紧跟按键区，再是缩略图与参数', async () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, onchange: null, dispatchEvent: () => false }))
    seed()
    await mount()
    const state = screen.getByTestId('remote-state')
    const keys = screen.getByTestId('remote-keys')
    const kv = screen.getByTestId('remote-kv')
    expect(state.compareDocumentPosition(keys) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(keys.compareDocumentPosition(kv) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // 手机上状态详情与操作记录的分区编号不与前面重复
    const nos = screen.getAllByText(/^02\.\d$/).map((e) => e.textContent)
    expect(new Set(nos).size).toBe(nos.length)
  })

  it('screen_state 推送变化后状态卡即时更新', async () => {
    seed()
    await mount()
    act(() => liveStore.applyPatch({ type: 'patch', entity: 'screen_state', server_time: '2026-10-01T14:47:00Z', screen_state: stateOf({ mode: 'off', reason: 'remote_off', until: '2026-10-01T15:00:00Z' }) } as unknown as Parameters<typeof liveStore.applyPatch>[0]))
    expect(within(screen.getByTestId('remote-state')).getByText('已关屏')).toBeInTheDocument()
  })
})
