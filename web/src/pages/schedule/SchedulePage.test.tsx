import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, json, mockApi, renderWithApp, seedStore, type ApiHandler, type Req } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { Schedule, ScreenStatus, Settings } from '@/types/generated'
import { SchedulePage } from './SchedulePage'

beforeEach(() => {
  liveStore.reset()
  // 「现在」固定为 2026-10-01T14:47:00Z，即 Asia/Shanghai 22:47（浏览器本地时区不影响结果）
  vi.useFakeTimers({ toFake: ['Date'], now: Date.parse('2026-10-01T14:47:00Z') })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const plan = (): Schedule => ({
  periods: [
    { start: '07:00', end: '19:00', theme: 'industrial' },
    { start: '19:00', end: '23:00', theme: 'ambient' },
    { start: '23:00', end: '07:00', theme: 'off' },
  ],
})
const settingsOf = (over: Partial<Settings> = {}): Settings =>
  ({
    language: 'zh', timezone: 'Asia/Shanghai', access_url: 'http://pimon.lan', https_enabled: false, trusted_proxies: ['10.0.0.1'], reduce_effects: false,
    screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1 },
    retention: { raw_hours: 24, five_min_days: 30, hour_days: 365 }, backup: { daily_at: '04:00', keep: 7 }, ...over,
  }) as Settings
const statusOf = (): ScreenStatus => ({ state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' }, online: true, coarse_pointer: false })

interface Opts {
  schedule?: Schedule
  settings?: () => Settings
  over?: ApiHandler
}
async function mount(o: Opts = {}) {
  const api = mockApi((req) => {
    const custom = o.over?.(req)
    if (custom) return custom
    if (req.method === 'GET' && req.url === '/api/schedule') return json(200, o.schedule ?? plan())
    if (req.method === 'GET' && req.url === '/api/settings') return json(200, (o.settings ?? settingsOf)())
    if (req.method === 'GET' && req.url === '/api/screen/status') return json(200, statusOf())
    if (req.method === 'PUT' && req.url === '/api/schedule') return json(200, req.body)
    if (req.method === 'PUT' && req.url === '/api/settings') return json(200, req.body)
    return undefined
  })
  seedStore([])
  await renderWithApp(<SchedulePage />)
  await screen.findByTestId('timeline')
  return api
}
const rowsOf = () => within(screen.getByTestId('period-list')).getAllByTestId(/^period-row-/)
const rangeOf = (row: HTMLElement) => row.getAttribute('data-range')
const ranges = () => rowsOf().map(rangeOf)
const puts = (calls: Req[], url: string) => calls.filter((c) => c.method === 'PUT' && c.url === url)
const timeInput = (name: string) => screen.getByRole('textbox', { name }) as HTMLInputElement
const edit = async (user: ReturnType<typeof userEvent.setup>, name: string, value: string) => {
  const el = timeInput(name)
  await user.clear(el)
  await user.type(el, value)
}
const segs = () => within(screen.getByTestId('timeline')).getAllByRole('button').filter((b) => b.hasAttribute('data-period'))

describe('时段计划页', () => {
  it('列出时段与时间轴，现在指针按设置时区（不是浏览器时区）显示，并标出当前时段与下一次变化', async () => {
    await mount()
    expect(ranges()).toEqual(['07:00-19:00', '19:00-23:00', '23:00-07:00'])
    expect(within(screen.getByTestId('timeline')).getByText('现在 22:47')).toBeInTheDocument()
    expect(within(rowsOf()[1]).getByText('当前')).toBeInTheDocument()
    expect(within(rowsOf()[2]).getByText('跨日')).toBeInTheDocument()
    expect(screen.getByTestId('timeline-foot')).toHaveTextContent('下一次变化 23:00')
    expect(screen.getByTestId('timeline-foot')).toHaveTextContent('13 分钟后')
    expect(screen.getByTestId('timeline-foot')).toHaveTextContent('Asia/Shanghai')
  })

  it('计划未改动且按计划运行时，倒计时取后端 next_change（权威，含夏令时），编辑后改用本地预览', async () => {
    seedStore([])
    liveStore.applySnapshot({ type: 'snapshot', build: 'b', role: 'admin', topics: [], server_time: '2026-10-01T14:47:00Z', instances: [], screen_state: { mode: 'on', theme_id: 'ambient', reason: 'schedule', next_change: '2026-10-01T15:07:00Z' } } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
    mockApi((req) => (req.url === '/api/schedule' ? json(200, plan()) : req.url === '/api/settings' ? json(200, settingsOf()) : req.url === '/api/screen/status' ? json(200, statusOf()) : undefined))
    const user = userEvent.setup()
    await renderWithApp(<SchedulePage />)
    await screen.findByTestId('timeline')
    expect(screen.getByTestId('timeline-foot')).toHaveTextContent('20 分钟后')
    await user.click(rowsOf()[0])
    await user.click(screen.getByRole('button', { name: /（mission-control）/ }))
    expect(screen.getByTestId('timeline-foot')).toHaveTextContent('13 分钟后')
  })

  it('保存期间时间输入框、时段类型、主题卡、降低特效与时间轴都被禁用', async () => {
    const user = userEvent.setup()
    let release: (r: Response) => void = () => {}
    await mount({ over: (req) => (req.method === 'PUT' && req.url === '/api/schedule' ? (new Promise<Response>((r) => (release = r)) as unknown as Response) : undefined) })
    await user.click(rowsOf()[1])
    await user.click(screen.getByRole('button', { name: /（mission-control）/ }))
    await user.click(screen.getByRole('button', { name: '保存并推送' }))
    await waitFor(() => expect(timeInput('开始时间 HH:MM')).toBeDisabled())
    expect(timeInput('结束时间 HH:MM')).toBeDisabled()
    expect(screen.getByRole('radio', { name: '关屏' })).toBeDisabled()
    expect(screen.getByRole('button', { name: /（ambient）/ })).toBeDisabled()
    expect(screen.getByRole('switch', { name: '降低特效' })).toBeDisabled()
    expect(screen.getByRole('slider', { name: /19:00/ })).toBeDisabled()
    release(json(200, plan()))
  })

  it('换成别的时区，「现在」随之变化', async () => {
    await mount({ settings: () => settingsOf({ timezone: 'America/Los_Angeles' }) })
    expect(within(screen.getByTestId('timeline')).getByText('现在 07:47')).toBeInTheDocument()
  })

  it('编辑时段起止时间：列表与时间轴实时联动，相邻段边界一并移动', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[1])
    await edit(user, '开始时间 HH:MM', '20:00')
    expect(ranges()).toEqual(['07:00-20:00', '20:00-23:00', '23:00-07:00'])
    // 时间轴上第一个色段随之变长（aria-label 里带起止）
    expect(segs().map((b) => b.getAttribute('aria-label'))).toEqual(
      expect.arrayContaining([expect.stringContaining('07:00–20:00'), expect.stringContaining('20:00–23:00')]),
    )
    await edit(user, '结束时间 HH:MM', '22:00')
    expect(ranges()).toEqual(['07:00-20:00', '20:00-22:00', '22:00-07:00'])
    expect(screen.getByRole('button', { name: '保存并推送' })).toBeEnabled()
  })

  it('时间格式不对时不改动计划并提示', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[1])
    await edit(user, '开始时间 HH:MM', '25:99')
    expect(ranges()).toEqual(['07:00-19:00', '19:00-23:00', '23:00-07:00'])
    expect(screen.getByText('时间格式应为 HH:MM')).toBeInTheDocument()
  })

  it('时间轴上点击色段选中，列表同步高亮并显示该段的编辑区', async () => {
    const user = userEvent.setup()
    await mount()
    const first = segs().find((b) => b.getAttribute('data-period') === '0')!
    await user.click(first)
    expect(rowsOf()[0]).toHaveAttribute('aria-current', 'true')
    expect(timeInput('开始时间 HH:MM').value).toBe('07:00')
  })

  it('时间轴边界用方向键移动，同步回列表', async () => {
    const user = userEvent.setup()
    await mount()
    const handle = screen.getByRole('slider', { name: /19:00/ })
    handle.focus()
    await user.keyboard('{ArrowRight}')
    expect(ranges()).toEqual(['07:00-19:05', '19:05-23:00', '23:00-07:00'])
    await user.keyboard('{Shift>}{ArrowLeft}{/Shift}')
    expect(ranges()).toEqual(['07:00-18:05', '18:05-23:00', '23:00-07:00'])
  })

  describe('指针拖动边界', () => {
    // 轴宽 1440 像素、左边缘 0：clientX 即分钟数
    const rect = vi.spyOn(Element.prototype, 'getBoundingClientRect')
    beforeEach(() => {
      rect.mockImplementation(function (this: Element) {
        const w = this.getAttribute('data-testid') === 'timeline' ? 1440 : 0
        return { left: 0, right: w, width: w, top: 0, bottom: 0, height: 0, x: 0, y: 0, toJSON: () => ({}) } as DOMRect
      })
    })
    afterEach(() => rect.mockReset())
    const ptr = (el: Element, type: string, x = 0) => fireEvent(el, new MouseEvent(type, { clientX: x, bubbles: true }))

    it('拖到轴的最右端不会跳到对侧：边界夹在相邻两段各至少 1 分钟之内，继续停在最右端也保持稳定', async () => {
      await mount()
      const handle = screen.getByRole('slider', { name: /19:00/ })
      ptr(handle, 'pointerdown', 1140)
      ptr(handle, 'pointermove', 1440)
      expect(ranges()).toEqual(['07:00-22:59', '22:59-23:00', '23:00-07:00'])
      ptr(handle, 'pointermove', 1440)
      expect(ranges()).toEqual(['07:00-22:59', '22:59-23:00', '23:00-07:00'])
      // 同一次拖动里再拖回中间，位置相对按下时计算，不受夹取影响
      ptr(handle, 'pointermove', 600)
      expect(ranges()).toEqual(['07:00-10:00', '10:00-23:00', '23:00-07:00'])
      ptr(handle, 'pointerup')
      expect(screen.queryByTestId('problems')).toBeNull()
    })

    it('跨午夜：23:00 的边界向右拖到轴端，变成 00:00，并且停在端点时不抖动', async () => {
      await mount()
      const handle = screen.getByRole('slider', { name: /23:00/ })
      ptr(handle, 'pointerdown', 1380)
      ptr(handle, 'pointermove', 1440)
      expect(ranges()).toEqual(['00:00-07:00', '07:00-19:00', '19:00-00:00'])
      ptr(handle, 'pointermove', 1440)
      expect(ranges()).toEqual(['00:00-07:00', '07:00-19:00', '19:00-00:00'])
      ptr(handle, 'pointerup')
      expect(screen.queryByTestId('problems')).toBeNull()
    })

    it('07:00 的边界向左拖到轴的最左端，变成 00:00，前一段（跨日段）缩到 23:00–00:00', async () => {
      await mount()
      const handle = screen.getByRole('slider', { name: /07:00/ })
      ptr(handle, 'pointerdown', 420)
      ptr(handle, 'pointermove', 0)
      expect(ranges()).toEqual(['00:00-19:00', '19:00-23:00', '23:00-00:00'])
      ptr(handle, 'pointermove', 0)
      expect(ranges()).toEqual(['00:00-19:00', '19:00-23:00', '23:00-00:00'])
    })
  })

  it('添加时段会拆分当前选中的时段，新时段被选中，计划仍覆盖全天', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[1])
    await user.click(screen.getByRole('button', { name: '添加时段' }))
    expect(ranges()).toEqual(['07:00-19:00', '19:00-21:00', '21:00-23:00', '23:00-07:00'])
    expect(rowsOf()[2]).toHaveAttribute('aria-current', 'true')
    expect(screen.queryByTestId('problems')).toBeNull()
  })

  it('点击主题卡把该主题赋给当前选中的时段，卡片标出已选与使用时段', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[0])
    const card = screen.getByRole('button', { name: /（mission-control）/ })
    expect(card).toHaveAttribute('aria-pressed', 'false')
    await user.click(card)
    expect(card).toHaveAttribute('aria-pressed', 'true')
    expect(within(card).getByText('用于 07:00–19:00')).toBeInTheDocument()
    expect(within(rowsOf()[0]).getByText('mission-control')).toBeInTheDocument()
    // 工业面板不再被使用
    expect(within(screen.getByRole('button', { name: /（industrial）/ })).getByText('未使用')).toBeInTheDocument()
  })

  it('主题卡缩略图用主题 token 实时渲染（容器带 data-theme），而不是图片', async () => {
    await mount()
    const cards = ['ambient', 'mission-control', 'industrial'].map((id) => screen.getByRole('button', { name: new RegExp(`（${id}）`) }))
    for (const [i, id] of ['ambient', 'mission-control', 'industrial'].entries()) {
      expect(cards[i].querySelector(`[data-theme="${id}"]`)).not.toBeNull()
      expect(cards[i].querySelector('img')).toBeNull()
    }
  })

  it('所选时段是关屏时主题卡不可点', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[2])
    expect(screen.getByRole('button', { name: /（ambient）/ })).toBeDisabled()
    expect(screen.getByText('所选时段为关屏')).toBeInTheDocument()
  })

  it('删除时段：前一段延长到被删段的结束；只剩一段时删除按钮不可用', async () => {
    const user = userEvent.setup()
    await mount({ schedule: { periods: [{ start: '00:00', end: '12:00', theme: 'ambient' }, { start: '12:00', end: '00:00', theme: 'off' }] } })
    await user.click(screen.getByRole('button', { name: '删除时段 2' }))
    expect(ranges()).toEqual(['00:00-00:00'])
    expect(screen.getByRole('button', { name: '删除时段 1' })).toBeDisabled()
  })

  it('保存时 PUT 排好序的整份计划，成功后不再有未保存提示', async () => {
    const user = userEvent.setup()
    const api = await mount()
    await user.click(rowsOf()[1])
    await user.click(screen.getByRole('button', { name: /（mission-control）/ }))
    await user.click(screen.getByRole('button', { name: '保存并推送' }))
    await waitFor(() => expect(puts(api.calls, '/api/schedule')).toHaveLength(1))
    expect(puts(api.calls, '/api/schedule')[0].body).toEqual({
      periods: [
        { start: '07:00', end: '19:00', theme: 'industrial' },
        { start: '19:00', end: '23:00', theme: 'mission-control' },
        { start: '23:00', end: '07:00', theme: 'off' },
      ],
    })
    await waitFor(() => expect(screen.getByRole('button', { name: '保存并推送' })).toBeDisabled())
    // 只改了计划，不碰设置
    expect(puts(api.calls, '/api/settings')).toHaveLength(0)
  })

  it('本地校验不通过时给出重叠提示并禁止保存，放弃修改后恢复', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[0])
    // 开始改到 20:00 会带动前一段结束，但整体越过了中间的时段，造成重叠
    await edit(user, '开始时间 HH:MM', '20:00')
    expect(screen.getByTestId('problems')).toHaveTextContent('被多个时段重叠')
    expect(screen.getByRole('button', { name: '保存并推送' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '放弃修改' }))
    expect(screen.queryByTestId('problems')).toBeNull()
    expect(ranges()).toEqual(['07:00-19:00', '19:00-23:00', '23:00-07:00'])
  })

  it('后端返回 schedule.invalid 时展示服务端的校验错误', async () => {
    const user = userEvent.setup()
    await mount({
      over: (req) =>
        req.method === 'PUT' && req.url === '/api/schedule'
          ? apiError(400, 'schedule.invalid', { problems: [{ kind: 'gap', from: '08:00', to: '10:00' }, { kind: 'theme', period: 1 }] })
          : undefined,
    })
    await user.click(rowsOf()[1])
    await user.click(screen.getByRole('button', { name: /（mission-control）/ }))
    await user.click(screen.getByRole('button', { name: '保存并推送' }))
    const box = await screen.findByTestId('problems-backend')
    expect(box).toHaveTextContent('服务端校验未通过')
    expect(box).toHaveTextContent('08:00–10:00 没有被任何时段覆盖')
    expect(box).toHaveTextContent('第 2 个时段：主题不在内置主题或关屏之内')
  })

  describe('降低特效（Ruling 10）', () => {
    it('保存前先 GET 最新设置再整份 PUT，只改 reduce_effects，其他字段取最新值而不是页面加载时的旧值', async () => {
      const user = userEvent.setup()
      let call = 0
      const latest = settingsOf({ language: 'en', access_url: 'http://changed.lan', trusted_proxies: ['10.0.0.9', '10.0.0.10'], retention: { raw_hours: 48, five_min_days: 60, hour_days: 400 }, backup: { daily_at: '03:30', keep: 3 }, https_enabled: true })
      const api = await mount({ settings: () => (call++ === 0 ? settingsOf() : latest) })
      await user.click(screen.getByRole('switch', { name: '降低特效' }))
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      await waitFor(() => expect(puts(api.calls, '/api/settings')).toHaveLength(1))
      const idxPut = api.calls.findIndex((c) => c.method === 'PUT' && c.url === '/api/settings')
      expect(api.calls.slice(0, idxPut).filter((c) => c.method === 'GET' && c.url === '/api/settings').length).toBeGreaterThanOrEqual(2)
      expect(puts(api.calls, '/api/settings')[0].body).toEqual({ ...latest, reduce_effects: true })
      // 只改了设置时不动计划
      expect(puts(api.calls, '/api/schedule')).toHaveLength(0)
    })

    it('计划与降低特效一起改：先存计划再存设置；设置失败时说明计划已保存', async () => {
      const user = userEvent.setup()
      const api = await mount({ over: (req) => (req.method === 'PUT' && req.url === '/api/settings' ? apiError(500, 'internal') : undefined) })
      await user.click(rowsOf()[1])
      await user.click(screen.getByRole('button', { name: /（mission-control）/ }))
      await user.click(screen.getByRole('switch', { name: '降低特效' }))
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      expect(await screen.findByText(/时段计划已保存，但「降低特效」未能保存：中枢内部错误/)).toBeInTheDocument()
      expect(puts(api.calls, '/api/schedule')).toHaveLength(1)
      // 开关仍保持未保存状态，可再次保存
      expect(screen.getByRole('switch', { name: '降低特效' })).toHaveAttribute('aria-checked', 'true')
      expect(screen.getByRole('button', { name: '保存并推送' })).toBeEnabled()
    })
  })

  it('唤醒规则只读：按设置里的输入方式显示触摸状态', async () => {
    await mount()
    const wake = screen.getByTestId('wake-rules')
    expect(within(wake).getByText('严重告警唤醒')).toBeInTheDocument()
    expect(within(wake).getByText('当前无触摸')).toBeInTheDocument()
    expect(within(wake).queryByRole('button')).toBeNull()
    expect(within(wake).queryByRole('switch')).toBeNull()
  })

  it('屏幕当前由远程操作接管时给出提示和远程页入口', async () => {
    seedStore([])
    liveStore.applySnapshot({ type: 'snapshot', build: 'b', role: 'admin', topics: [], server_time: '2026-10-01T14:47:00Z', instances: [], screen_state: { mode: 'off', theme_id: 'ambient', reason: 'remote_off', until: '2026-10-01T15:00:00Z' } } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
    const api = mockApi((req) => {
      if (req.url === '/api/schedule') return json(200, plan())
      if (req.url === '/api/settings') return json(200, settingsOf())
      if (req.url === '/api/screen/status') return json(200, statusOf())
      return undefined
    })
    void api
    await renderWithApp(<SchedulePage />)
    await screen.findByTestId('timeline')
    expect(screen.getByText(/屏幕当前由远程操作接管/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '去远程操作' })).toHaveAttribute('href', '/screens/remote')
  })

  it('读取失败时显示错误与重试', async () => {
    mockApi((req) => (req.url === '/api/schedule' ? apiError(500, 'internal') : undefined))
    seedStore([])
    await renderWithApp(<SchedulePage />)
    expect(await screen.findByText(/无法加载时段计划：中枢内部错误/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('放弃修改回到已保存的计划', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(rowsOf()[1])
    await user.click(screen.getByRole('button', { name: '添加时段' }))
    expect(rowsOf()).toHaveLength(4)
    await user.click(screen.getByRole('button', { name: '放弃修改' }))
    expect(ranges()).toEqual(['07:00-19:00', '19:00-23:00', '23:00-07:00'])
  })
})

