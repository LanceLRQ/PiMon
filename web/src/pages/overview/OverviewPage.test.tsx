import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fixtureNow, makeInstance, prototypeInstances } from '@/pages/instances/fixtures'
import { json, mockApi, patchInstance, renderWithApp, seedStore } from '@/pages/instances/test-utils'
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

  it('屏幕卡片本期是占位，按键不可点', async () => {
    mockApi()
    seedStore(prototypeInstances)
    await renderWithApp(<OverviewPage />)
    const screenCard = cell('屏幕')
    expect(within(screenCard).getByText('屏幕模块待接入')).toBeInTheDocument()
    for (const b of within(screenCard).getAllByRole('button')) expect(b).toBeDisabled()
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
    expect(within(cell('Screen')).getByText('Screen module not connected yet')).toBeInTheDocument()
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
