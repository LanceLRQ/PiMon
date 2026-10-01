import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fixtureNow, makeInstance, prototypeInstances } from '@/pages/instances/instances.fixtures'
import { apiError, json, mockApi, patchInstance, renderWithApp, seedStore } from '@/pages/instances/test-utils'
import type { LayoutState, ScreenStatus } from '@/types/generated'
import { liveStore } from '@/store/live-store'
import { OverviewPage } from './OverviewPage'

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function cell(title: string): HTMLElement {
  return screen.getByRole('region', { name: title })
}

describe('总览页', () => {
  it('健康汇总：总数、最严重结论与其余状态', async () => {
    mockApi()
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const health = cell('健康汇总')
    expect(within(health).getByText('17')).toBeInTheDocument()
    expect(within(health).getByText('1 项严重：ubuntu-srv 磁盘 95%')).toBeInTheDocument()
    expect(within(health).getByText('另有 1 项采集失败、2 项警告')).toBeInTheDocument()
    const counts = within(health).getAllByRole('term').map((dt) => [dt.textContent, dt.nextElementSibling?.textContent])
    expect(counts).toEqual([
      ['正常', '13'],
      ['警告', '2'],
      ['严重', '1'],
      ['采集失败', '1'],
      ['过期', '0'],
      ['引用失效', '0'],
      ['离线', '0'],
      ['未配置', '0'],
      ['已暂停', '0'],
      ['未知/其他', '0'],
    ])
    expect(within(health).getByRole('img', { name: '17 个实例的状态分布' }).children).toHaveLength(17)
  })

  it('全部正常与空集合的结论', async () => {
    mockApi()
    seedStore([makeInstance({ id: 'a', name: 'a' })])
    await renderWithApp(<OverviewPage />)
    expect(within(cell('健康汇总')).getByText('全部正常')).toBeInTheDocument()
    expect(within(cell('需要处理')).getByText('没有需要处理的项')).toBeInTheDocument()
    act(() => liveStore.applyPatch({ type: 'patch', entity: 'instance_removed', id: 'a', server_time: new Date().toISOString() } as never))
    expect(within(cell('健康汇总')).getByText('还没有监控实例，先添加一个吧。')).toBeInTheDocument()
  })

  it('其余状态（未知等）单独成类，不被算进正常', async () => {
    mockApi()
    seedStore([makeInstance({ id: 'a', name: 'a' }), makeInstance({ id: 'b', name: 'b', display_state: 'unknown' })])
    await renderWithApp(<OverviewPage />)
    expect(within(cell('健康汇总')).getByText('1 项状态未知：b')).toBeInTheDocument()
  })

  it('没有 snapshot 时显示加载中，数字为占位', async () => {
    mockApi()
    await renderWithApp(<OverviewPage />)
    const health = cell('健康汇总')
    expect(within(health).getByText('加载中')).toBeInTheDocument()
    expect(within(health).queryByText('0')).toBeNull()
  })

  it('hub 概况：snapshot 到达前实例数显示未知，不显示 0', async () => {
    mockApi()
    await renderWithApp(<OverviewPage />)
    const hub = cell('hub 概况')
    expect(within(hub).queryByText('0')).toBeNull()
    expect(within(hub).getByText('实例数').parentElement).toHaveTextContent('未知')
  })

  it('需要处理按严重度排序，只有采集失败的行有「立即重试」', async () => {
    const user = userEvent.setup()
    const { calls } = mockApi((req) => (req.url === '/api/instances/i04/run' ? json(200, { instance: prototypeInstances[3] }) : undefined))
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const todo = cell('需要处理')
    expect(within(todo).getByText('4 项 · 按严重度')).toBeInTheDocument()
    const items = within(todo).getAllByRole('listitem')
    expect(items.map((li) => li.getAttribute('data-state-row'))).toEqual(['critical', 'error', 'warning', 'warning'])
    expect(within(items[0]).getByText('ubuntu-srv')).toBeInTheDocument()
    expect(within(items[1]).getByText('连接被拒绝')).toBeInTheDocument()
    expect(within(todo).getAllByRole('button', { name: '立即重试' })).toHaveLength(1)
    await user.click(within(todo).getByRole('button', { name: '立即重试' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/instances/i04/run' && c.method === 'POST')).toBe(true))
  })

  it('需要处理纳入 broken、offline、unconfigured 并按严重度排序，排除暂停、维护与未知', async () => {
    mockApi()
    seedStore([
      makeInstance({ id: 'u', name: 'u', display_state: 'unconfigured' }),
      makeInstance({ id: 'o', name: 'o', display_state: 'offline' }),
      makeInstance({ id: 'b', name: 'b', display_state: 'broken' }),
      makeInstance({ id: 'p', name: 'p', display_state: 'critical', paused: true }),
      makeInstance({ id: 'm', name: 'm', display_state: 'maintenance' }),
      makeInstance({ id: 'k', name: 'k', display_state: 'unknown' }),
    ])
    await renderWithApp(<OverviewPage />)
    const todo = cell('需要处理')
    expect(within(todo).getByText('3 项 · 按严重度')).toBeInTheDocument()
    expect(within(todo).getAllByRole('listitem').map((li) => li.getAttribute('data-state-row'))).toEqual(['broken', 'offline', 'unconfigured'])
  })

  it('健康汇总：broken、offline、unconfigured 各自计数，不再算作状态未知', async () => {
    mockApi()
    seedStore([
      makeInstance({ id: 'b', name: 'b', display_state: 'broken' }),
      makeInstance({ id: 'o1', name: 'o1', display_state: 'offline' }),
      makeInstance({ id: 'o2', name: 'o2', display_state: 'offline' }),
      makeInstance({ id: 'u', name: 'u', display_state: 'unconfigured' }),
      makeInstance({ id: 'k', name: 'k', display_state: 'unknown' }),
    ])
    await renderWithApp(<OverviewPage />)
    const health = cell('健康汇总')
    const count = (label: string) => within(health).getByText(label, { selector: 'dt' }).parentElement!.querySelector('dd')!.textContent
    expect(count('引用失效')).toBe('1')
    expect(count('离线')).toBe('2')
    expect(count('未配置')).toBe('1')
    expect(count('未知/其他')).toBe('1')
    expect(within(health).getByText(/^1 项引用失效：b/)).toBeInTheDocument()
  })

  it('健康汇总：已暂停的严重实例不影响结论，单独计入已暂停', async () => {
    mockApi()
    seedStore([
      makeInstance({ id: 'p', name: 'p', display_state: 'critical', paused: true }),
      makeInstance({ id: 'a', name: 'a' }),
    ])
    await renderWithApp(<OverviewPage />)
    const health = cell('健康汇总')
    expect(within(health).getByText('全部正常')).toBeInTheDocument()
    expect(within(health).getByText('已暂停', { selector: 'dt' }).parentElement!.querySelector('dd')!.textContent).toBe('1')
    expect(within(health).getByText('严重', { selector: 'dt' }).parentElement!.querySelector('dd')!.textContent).toBe('0')
    expect(within(cell('需要处理')).getByText('没有需要处理的项')).toBeInTheDocument()
  })

  it('健康汇总：全部实例已暂停时给出中性结论，不显示全部正常', async () => {
    mockApi()
    seedStore([
      makeInstance({ id: 'p1', name: 'p1', paused: true }),
      makeInstance({ id: 'p2', name: 'p2', display_state: 'critical', paused: true }),
    ])
    await renderWithApp(<OverviewPage />)
    const health = cell('健康汇总')
    expect(within(health).getByText('没有运行中的实例（2 个已暂停）')).toBeInTheDocument()
    expect(within(health).queryByText('全部正常')).toBeNull()
  })

  it('查看实例打开详情抽屉', async () => {
    const user = userEvent.setup()
    mockApi((req) => (req.url === '/api/instances/i01' ? json(200, { ...prototypeInstances[0], config: {}, report: null }) : undefined))
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    await user.click(within(cell('需要处理')).getAllByRole('button', { name: '查看实例' })[0])
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByRole('heading', { name: 'ubuntu-srv' })).toBeInTheDocument()
  })

  it('patch 到达后汇总、结论与需要处理同步变化', async () => {
    mockApi()
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    act(() => patchInstance({ ...prototypeInstances[0], display_state: 'ok', summary: '磁盘 40%' }))
    const health = cell('健康汇总')
    expect(within(health).getByText(/^1 项采集失败：/)).toBeInTheDocument()
    expect(within(cell('需要处理')).getByText('3 项 · 按严重度')).toBeInTheDocument()
    expect(within(cell('需要处理')).queryByText('ubuntu-srv')).toBeNull()
  })

  describe('屏幕卡片', () => {
    const layoutOf = (): LayoutState => ({
      version: 4,
      source: 'edit',
      created_at: '2026-10-01T00:00:00Z',
      broken: [],
      layout: {
        grid: { cols: 8, rows: 5 },
        screens: [
          { id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets: [] },
          { id: 's1', name: '主机', dwell_seconds: 0, in_rotation: true, widgets: [] },
          { id: 's2', name: '网络', dwell_seconds: 0, in_rotation: true, widgets: [] },
        ],
      },
    })
    const statusOf = (over: Partial<ScreenStatus> = {}): ScreenStatus => ({
      state: { mode: 'on', theme_id: 'ambient', reason: 'schedule', next_change: '2026-10-01T15:00:00Z' },
      viewport: { w: 1024, h: 600, dpr: 1 },
      coarse_pointer: false,
      current_screen: 's1',
      online: true,
      ...over,
    })
    function seedScreen(mode = 'on') {
      seedStore(prototypeInstances)
      liveStore.applySnapshot({
        type: 'snapshot', build: 'b', role: 'admin', topics: [], server_time: new Date().toISOString(), instances: prototypeInstances,
        settings: { reduce_effects: false, timezone: 'Asia/Shanghai', screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1 } },
        layout: layoutOf(),
        screen_state: { mode, theme_id: 'ambient', reason: 'schedule' },
      } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
    }
    const key = (name: RegExp | string) => within(cell('屏幕')).getByRole('button', { name })

    it('显示在线状态、当前 screen 的实时缩略图与参数', async () => {
      mockApi((req) => (req.url === '/api/screen/status' ? json(200, statusOf()) : undefined))
      seedScreen()
      await renderWithApp(<OverviewPage />)
      const card = cell('屏幕')
      expect(await within(card).findByText('显示器在线')).toBeInTheDocument()
      const thumb = within(card).getByTestId('screen-thumb')
      expect(thumb).toHaveAttribute('data-screen-id', 's1')
      expect(within(card).getByText('1024×600')).toBeInTheDocument()
      expect(within(card).getByText('8×5')).toBeInTheDocument()
      expect(within(card).getByText('无触摸')).toBeInTheDocument()
      expect(within(card).getByText('s1')).toBeInTheDocument()
      expect(within(card).getByText('ambient')).toBeInTheDocument()
    })

    it('离线时不显示 current_screen（服务端不会清空），缩略图回到首页，刷新与切换不可用', async () => {
      mockApi((req) => (req.url === '/api/screen/status' ? json(200, statusOf({ online: false, last_seen: '2026-10-01T08:00:00Z' })) : undefined))
      seedScreen()
      await renderWithApp(<OverviewPage />)
      const card = cell('屏幕')
      expect(await within(card).findByText('显示器离线')).toBeInTheDocument()
      expect(within(card).queryByText('s1')).toBeNull()
      expect(within(card).getByTestId('screen-thumb')).toHaveAttribute('data-screen-id', 'index')
      expect(key(/刷新/)).toBeDisabled()
      expect(key(/切换 screen/)).toBeDisabled()
      expect(key(/关屏/)).toBeEnabled()
    })

    it('状态读取失败时提示，按键不可点', async () => {
      mockApi()
      seedScreen()
      await renderWithApp(<OverviewPage />)
      const card = cell('屏幕')
      expect(await within(card).findByText('屏幕状态暂时无法读取')).toBeInTheDocument()
      for (const b of within(card).getAllByRole('button')) expect(b).toBeDisabled()
    })

    it('k1 刷新、k2 切到下一个 screen、k4 临时亮屏各发一次控制请求', async () => {
      const api = mockApi((req) => {
        if (req.url === '/api/screen/status') return json(200, statusOf())
        if (req.url === '/api/screen/control') return json(200, { op: { id: 1, action: 'x', params: {}, client_ip: '', delivered: true, at: '2026-10-01T00:00:00Z' }, state: statusOf().state })
        return undefined
      })
      seedScreen()
      const user = userEvent.setup()
      await renderWithApp(<OverviewPage />)
      await within(cell('屏幕')).findByText('显示器在线')
      await user.click(key(/刷新/))
      await user.click(key(/切换 screen/))
      await user.click(key(/临时亮屏/))
      const posts = api.calls.filter((c) => c.method === 'POST' && c.url === '/api/screen/control').map((c) => c.body)
      expect(posts).toEqual([{ action: 'refresh' }, { action: 'switch', screen_id: 's2' }, { action: 'wake', minutes: 30 }])
      expect(await screen.findByText('已发送：临时亮屏')).toBeInTheDocument()
    })

    it('k3：亮屏时是关屏，关屏中变成开屏', async () => {
      const api = mockApi((req) => {
        if (req.url === '/api/screen/status') return json(200, statusOf({ state: { mode: 'off', theme_id: 'ambient', reason: 'remote_off' } }))
        if (req.url === '/api/screen/control') return json(200, { op: { id: 1, action: 'on', params: {}, client_ip: '', delivered: true, at: '2026-10-01T00:00:00Z' }, state: statusOf().state })
        return undefined
      })
      seedScreen('off')
      const user = userEvent.setup()
      await renderWithApp(<OverviewPage />)
      await within(cell('屏幕')).findByText('显示器在线')
      expect(within(cell('屏幕')).getByText('屏幕已关闭')).toBeInTheDocument()
      await user.click(key(/开屏/))
      expect(api.calls.find((c) => c.method === 'POST' && c.url === '/api/screen/control')?.body).toEqual({ action: 'on' })
    })

    it('控制失败时 toast 提示错误', async () => {
      mockApi((req) => {
        if (req.url === '/api/screen/status') return json(200, statusOf())
        if (req.url === '/api/screen/control') return apiError(500, 'internal')
        return undefined
      })
      seedScreen()
      const user = userEvent.setup()
      await renderWithApp(<OverviewPage />)
      await within(cell('屏幕')).findByText('显示器在线')
      await user.click(key(/刷新/))
      expect(await screen.findByRole('alert')).toBeInTheDocument()
      expect(screen.queryByText('已发送：刷新')).toBeNull()
    })
  })

  it('hub 概况：版本、连接状态与最近备份', async () => {
    mockApi((req) =>
      req.url === '/api/backups'
        ? json(200, [
            { name: 'a', reason: 'daily', created_at: '2026-09-29T04:00:00Z', size: 1 },
            { name: 'b', reason: 'daily', created_at: '2026-09-30T04:00:00Z', size: 1 },
          ])
        : undefined,
    )
    seedStore(prototypeInstances, { build: 'v0.1.0-test' })
    await renderWithApp(<OverviewPage />)
    const hub = cell('hub 概况')
    expect(within(hub).getByText('v0.1.0-test')).toBeInTheDocument()
    expect(within(hub).getByText('hub 在线')).toBeInTheDocument()
    expect(await within(hub).findByText(/9\/30/)).toBeInTheDocument()
  })

  it('hub 概况：运行时长取自 /api/system 的 uptime_seconds（不用浏览器时间），取不到时显示未知', async () => {
    mockApi((req) => (req.url === '/api/system' ? json(200, { version: 'v', started_at: '2000-01-01T00:00:00Z', uptime_seconds: 2 * 86400 + 5 * 3600 + 60 }) : undefined))
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const hub = cell('hub 概况')
    await waitFor(() => expect(within(hub).getByText('运行时长').nextElementSibling).toHaveTextContent('2 天 5 小时'))
  })

  it('hub 概况：/api/system 失败时运行时长显示未知，不当作 0', async () => {
    mockApi((req) => (req.url === '/api/system' ? new Response('boom', { status: 500 }) : undefined))
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const hub = cell('hub 概况')
    await waitFor(() => expect(within(hub).getByText('运行时长').nextElementSibling).toHaveTextContent('未知'))
  })

  it('备份接口失败时最近备份显示未知，而不是「暂无备份」', async () => {
    mockApi((req) => (req.url === '/api/backups' ? new Response('boom', { status: 500 }) : undefined))
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const hub = cell('hub 概况')
    await waitFor(() => expect(within(hub).getByText('最近备份').nextElementSibling).toHaveTextContent('未知'))
    expect(within(hub).queryByText('暂无备份')).toBeNull()
  })

  it('实例一览与实例列表共用同一张表，查询语法可用', async () => {
    const user = userEvent.setup()
    mockApi()
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const table = cell('实例一览')
    await user.type(within(table).getByLabelText('搜索实例，支持查询语法'), 'http 博客')
    expect(within(table).getAllByRole('row')).toHaveLength(2)
    expect(within(table).getByRole('button', { name: '个人博客' })).toBeInTheDocument()
  })

  it('英文界面', async () => {
    mockApi()
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />, { lng: 'en' })
    expect(within(cell('Health summary')).getByText('1 critical: ubuntu-srv 磁盘 95%')).toBeInTheDocument()
    expect(await within(cell('Screen')).findByText('The display status cannot be read right now')).toBeInTheDocument()
  })

  it('时钟不影响汇总（时间只用于「更新于」）', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(fixtureNow)
    mockApi()
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    expect(within(cell('实例一览')).getByText('12 秒前')).toBeInTheDocument()
  })
})
