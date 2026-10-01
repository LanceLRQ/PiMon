import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { liveStore } from '@/store/live-store'
import type { Report } from '@/types/generated'
import { fixtureNow, makeInstance } from './fixtures'
import { InstancesPage } from './InstancesPage'
import type { InstanceDetailView } from './types'
import { apiError, json, mockApi, patchInstance, renderWithApp, seedStore } from './test-utils'

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => vi.unstubAllGlobals())

const report: Report = {
  status: 'warning',
  summary: 'CPU 37%',
  items: [
    { key: 'cpu.pi', type: 'gauge', value: 37, unit: '%', min: 0, max: 100 },
    { key: 'mem.pi', type: 'gauge', unit: '%' },
    { key: 'quota.codex.5h', type: 'quota', remaining_pct: 72, used: 28, total: 100, resets_at: fixtureNow + 3600_000 },
    { key: 'money.deepseek', type: 'money', amount: 86.4, currency: 'CNY' },
    { key: 'host[ubuntu]', type: 'state', state: 'critical', text: '磁盘将满' },
    { key: 'notes', type: 'text', text: '多云' },
    { key: 'tasks', type: 'table', columns: ['任务'], rows: [['编译']] },
  ],
}

function detailOf(over: Partial<InstanceDetailView> = {}): InstanceDetailView {
  return { ...makeInstance({ id: 'a1', name: '树莓派', summary: 'CPU 37%', display_state: 'warning' }), config: {}, report, ...over }
}

const history = {
  instance_id: 'a1',
  item: 'cpu.pi',
  field: 'value',
  range: '24h',
  tier: '5m',
  from: fixtureNow - 86400_000,
  to: fixtureNow,
  points: [
    { t: fixtureNow - 2000, avg: 30, min: 30, max: 30 },
    { t: fixtureNow - 1000, avg: 37, min: 37, max: 37 },
    { t: fixtureNow, avg: 35, min: 35, max: 35 },
  ],
}

async function openDrawer(detail: InstanceDetailView, extra?: Parameters<typeof mockApi>[0]) {
  const user = userEvent.setup()
  const api = mockApi((req) => {
    const custom = extra?.(req)
    if (custom) return custom
    if (req.url === `/api/instances/${detail.id}`) return json(200, detail)
    if (req.url.startsWith(`/api/instances/${detail.id}/history`)) return json(200, history)
    return undefined
  })
  const inst = makeInstance({ ...detail, config: undefined, report: undefined } as never)
  seedStore([inst])
  await renderWithApp(<InstancesPage />)
  await user.click(within(screen.getAllByRole('row')[1]).getByRole('button', { name: inst.name }))
  const dlg = await screen.findByRole('dialog')
  return { user, api, dlg }
}

describe('实例详情抽屉', () => {
  it('全部数据项按类型渲染，标题取自插件声明，缺失值显示未知', async () => {
    const { dlg } = await openDrawer(detailOf({ plugin_id: 'demo' }))
    expect(await within(dlg).findAllByText('树莓派 CPU')).not.toHaveLength(0)
    expect(within(dlg).getByText('37')).toBeInTheDocument()
    // 动态集合 host[*] 的成员用集合标题
    expect(within(dlg).getByText('主机')).toBeInTheDocument()
    expect(within(dlg).getByText('磁盘将满')).toBeInTheDocument()
    expect(within(dlg).getByText('剩余 72%')).toBeInTheDocument()
    expect(within(dlg).getByText(/86\.40/)).toBeInTheDocument()
    expect(within(dlg).getByText('多云')).toBeInTheDocument()
    expect(within(dlg).getByRole('table')).toBeInTheDocument()
    const types = [...dlg.querySelectorAll('[data-item-type]')].map((e) => e.getAttribute('data-item-type'))
    expect(types).toEqual(['gauge', 'gauge', 'quota', 'money', 'state', 'text', 'table'])
    // mem.pi 没有 value：未知，不是 0，也没有进度条
    const mem = dlg.querySelectorAll('[data-item-type="gauge"]')[1] as HTMLElement
    expect(within(mem).getByText('未知')).toBeInTheDocument()
    expect(within(mem).queryByRole('meter')).toBeNull()
    expect(within(dlg).getByText('数据项（7）')).toBeInTheDocument()
  })

  it('取 24 小时历史曲线并显示采样点数', async () => {
    const { dlg, api } = await openDrawer(detailOf())
    expect(await within(dlg).findByText('3 个点 · 5m 档')).toBeInTheDocument()
    const call = api.calls.find((c) => c.url.startsWith('/api/instances/a1/history'))
    expect(call?.url).toContain('item=cpu.pi')
    expect(call?.url).toContain('range=24h')
    // 选择数据项下拉里只有有数值历史的数据项
    const select = within(dlg).getByLabelText('选择数据项')
    expect([...select.querySelectorAll('option')].map((o) => o.value)).toEqual(['cpu.pi', 'mem.pi', 'quota.codex.5h', 'money.deepseek'])
  })

  it('切到 quota 数据项后可选字段并重新取历史', async () => {
    const { dlg, api, user } = await openDrawer(detailOf())
    await within(dlg).findByText('3 个点 · 5m 档')
    await user.selectOptions(within(dlg).getByLabelText('选择数据项'), 'quota.codex.5h')
    await user.click(await within(dlg).findByRole('radio', { name: '已用量' }))
    await waitFor(() => expect(api.calls.some((c) => c.url.includes('item=quota.codex.5h') && c.url.includes('field=used'))).toBe(true))
  })

  it('历史为空时如实说明', async () => {
    const { dlg } = await openDrawer(detailOf(), (req) => (req.url.includes('/history') ? json(200, { ...history, field: '', points: [] }) : undefined))
    expect(await within(dlg).findByText('最近 24 小时还没有采样点。')).toBeInTheDocument()
  })

  it('最近错误、失败次数与 issue 取自实例字段，已脱敏文字原样显示', async () => {
    const detail = detailOf({ display_state: 'error', last_error: 'exit status 1\nstderr: boom', failures: 3, issue: '插件已消失', report: undefined })
    const { dlg } = await openDrawer(detail)
    expect(within(dlg).getByText('最近错误与 stderr 摘要')).toBeInTheDocument()
    expect(within(dlg).getByText(/stderr: boom/)).toBeInTheDocument()
    expect(within(dlg).getByText('插件已消失')).toBeInTheDocument()
    expect(within(dlg).getByText('连续失败')).toBeInTheDocument()
    expect(await within(dlg).findByText('还没有成功采集过，暂无数据项')).toBeInTheDocument()
  })

  it('没有错误时说明最近一次采集成功', async () => {
    const { dlg } = await openDrawer(detailOf())
    expect(within(dlg).getByText('最近一次采集成功，没有错误。')).toBeInTheDocument()
  })

  it('采集失败保留的旧值带过期提示', async () => {
    const { dlg } = await openDrawer(detailOf({ report: { ...report, stale: true, items: [{ key: 'cpu.pi', type: 'gauge', value: 5, stale: true }] } }))
    expect(await within(dlg).findByText('最近一次采集失败，以下为失败前保留的旧值。')).toBeInTheDocument()
    expect(within(dlg).getAllByText('过期').length).toBeGreaterThan(0)
  })

  it('配置与插件不符的字段列出来', async () => {
    const { dlg } = await openDrawer(detailOf({ problems: { 'config.city': 'required' } }))
    expect(await within(dlg).findByText(/config\.city：required/)).toBeInTheDocument()
  })

  it('天气实例显示 Open-Meteo 署名，其他实例没有', async () => {
    const { dlg } = await openDrawer(detailOf({ plugin_id: 'weather' }))
    const link = within(dlg).getByRole('link', { name: 'Weather data by Open-Meteo.com' })
    expect(link).toHaveAttribute('href', 'https://open-meteo.com/')
    expect(within(dlg).getByText(/天气数据来源/)).toBeInTheDocument()
  })

  it('非天气实例不显示署名', async () => {
    const { dlg } = await openDrawer(detailOf({ plugin_id: 'demo' }))
    expect(within(dlg).queryByRole('link', { name: /Open-Meteo/ })).toBeNull()
  })

  it('详情请求失败时显示译文错误', async () => {
    const { dlg } = await openDrawer(detailOf(), (req) => (req.url === '/api/instances/a1' ? apiError(404, 'instance.not_found') : undefined))
    expect(await within(dlg).findByText('实例不存在')).toBeInTheDocument()
  })

  it('实时 patch 更新抽屉头部与状态，并重新取详情', async () => {
    const { dlg, api } = await openDrawer(detailOf())
    const before = api.calls.filter((c) => c.url === '/api/instances/a1').length
    expect(within(dlg).getAllByRole('img', { name: '警告' }).length).toBeGreaterThan(0)
    act(() => patchInstance(makeInstance({ id: 'a1', name: '树莓派', display_state: 'critical', summary: 'CPU 99%', last_success_at: new Date(fixtureNow).toISOString(), updated_at: '2026-10-01T12:00:01Z' })))
    await waitFor(() => expect(within(screen.getByRole('dialog')).getAllByRole('img', { name: '严重' }).length).toBeGreaterThan(0))
    await waitFor(() => expect(api.calls.filter((c) => c.url === '/api/instances/a1').length).toBe(before + 1))
  })

  it('实例被删除后抽屉自动关闭', async () => {
    await openDrawer(detailOf())
    act(() => liveStore.applyPatch({ type: 'patch', entity: 'instance_removed', id: 'a1', server_time: new Date().toISOString() } as never))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('抽屉里的立即采集与删除走同一套确认流程', async () => {
    const { dlg, user, api } = await openDrawer(detailOf(), (req) => (req.url === '/api/instances/a1/run' ? apiError(409, 'run.busy') : undefined))
    await user.click(within(dlg).getByRole('button', { name: '立即采集' }))
    expect(await screen.findByText('该实例正在运行，请稍后再试')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '删除实例' }))
    expect(await screen.findByText('删除所选的 1 个实例？')).toBeInTheDocument()
    expect(api.calls.some((c) => c.method === 'DELETE')).toBe(false)
  })
})
