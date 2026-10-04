import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { device } from '@/pages/devices/test-utils'
import { liveStore } from '@/store/live-store'
import type { Instance } from '@/types/generated'
import { makeInstance } from './instances.fixtures'
import { InstancesPage } from './InstancesPage'
import { apiError, json, mockApi, renderWithApp, seedStore } from './test-utils'

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => vi.unstubAllGlobals())

const devices = [device('d1', { name: '飞牛 NAS 机' }), device('d2', { name: 'Build-Box' })]

const list: Instance[] = [
  makeInstance({ id: 'a', name: '本机采集', runs_on: 'hub' }),
  makeInstance({ id: 'b', name: 'NAS 磁盘', runs_on: 'd1', plugin_id: 'storage-health' }),
  makeInstance({ id: 'c', name: '旧设备上的', runs_on: 'gone9', plugin_id: 'host-metrics' }),
  makeInstance({ id: 'd', name: '暂停的远程', runs_on: 'd2', paused: true }),
]

function bodyRows() {
  return screen.getAllByRole('row').slice(1)
}
function rowOf(name: string): HTMLElement {
  return bodyRows().find((r) => within(r).queryByRole('button', { name })) as HTMLElement
}

async function mount(handler?: Parameters<typeof mockApi>[0]) {
  const api = mockApi(handler)
  seedStore(list, { devices })
  await renderWithApp(<InstancesPage />)
  return api
}

describe('列表的运行位置列', () => {
  it('显示设备名而不是 ID；已吊销的显示「引用失效」；中枢显示本机', async () => {
    await mount()
    expect(within(rowOf('NAS 磁盘')).getByText('飞牛 NAS 机')).toBeInTheDocument()
    expect(within(rowOf('NAS 磁盘')).queryByText('d1')).toBeNull()
    expect(within(rowOf('旧设备上的')).getByText('gone9（引用失效）')).toBeInTheDocument()
    expect(within(rowOf('本机采集')).getByText('本机')).toBeInTheDocument()
  })

  it('设备列表未加载时显示原始标识，不标失效', async () => {
    mockApi()
    seedStore(list)
    await renderWithApp(<InstancesPage />)
    expect(within(rowOf('旧设备上的')).getByText('gone9')).toBeInTheDocument()
  })

  it('runs: 搜索按设备名匹配', async () => {
    const user = userEvent.setup()
    await mount()
    await user.type(screen.getByLabelText('搜索实例，支持查询语法'), 'runs:build-box')
    expect(bodyRows()).toHaveLength(1)
    expect(within(bodyRows()[0]).getByRole('button', { name: '暂停的远程' })).toBeInTheDocument()
  })

  it('位置筛选的选项显示设备名', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(screen.getAllByRole('button', { name: /^运行位置$/ }).find((b) => b.getAttribute('aria-haspopup') === 'menu') as HTMLElement)
    expect(await screen.findByRole('menuitemradio', { name: '飞牛 NAS 机' })).toBeInTheDocument()
    expect(screen.getByRole('menuitemradio', { name: 'gone9（引用失效）' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitemradio', { name: 'Build-Box' }))
    expect(bodyRows()).toHaveLength(1)
  })
})

describe('暂停的远程实例不能测试', () => {
  it('行菜单的「立即采集」禁用并说明原因', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(within(rowOf('暂停的远程')).getByRole('button', { name: /更多操作/ }))
    const item = await screen.findByRole('menuitem', { name: /立即采集/ })
    expect(item).toHaveAttribute('aria-disabled', 'true')
    expect(item).toHaveAttribute('title', '已暂停的设备实例不能测试，请先恢复')
  })

  it('中枢上已暂停的实例仍可立即采集', async () => {
    const user = userEvent.setup()
    mockApi()
    seedStore([makeInstance({ id: 'h', name: '中枢暂停', paused: true })], { devices })
    await renderWithApp(<InstancesPage />)
    await user.click(within(rowOf('中枢暂停')).getByRole('button', { name: /更多操作/ }))
    expect(await screen.findByRole('menuitem', { name: /立即采集/ })).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('详情抽屉里「立即采集」禁用，并有提示', async () => {
    const user = userEvent.setup()
    await mount((req) => (req.url === '/api/instances/d' ? json(200, { ...list[3], config: {}, report: null }) : undefined))
    await user.click(screen.getByRole('button', { name: '暂停的远程' }))
    const btn = await screen.findByRole('button', { name: /立即采集/ })
    expect(btn).toBeDisabled()
    expect(await screen.findByText('已暂停的设备实例不能测试，请先恢复')).toBeInTheDocument()
  })

  it('批量采集跳过已暂停的设备实例，不发请求', async () => {
    const user = userEvent.setup()
    const { calls } = await mount((req) => (req.url.endsWith('/run') ? json(200, { instance: list[0] }) : undefined))
    for (const row of bodyRows()) await user.click(within(row).getByRole('checkbox'))
    await user.click(screen.getByRole('button', { name: /立即采集/ }))
    await screen.findByText(/已完成 \d+ 个实例的采集/)
    const runs = calls.filter((c) => c.url.endsWith('/run')).map((c) => c.url)
    expect(runs).not.toContain('/api/instances/d/run')
    expect(runs).toContain('/api/instances/b/run')
  })
})

describe('远程问题文案', () => {
  it('run.failed 显示 details.message', async () => {
    const user = userEvent.setup()
    await mount((req) => (req.url === '/api/instances/b/run' ? apiError(502, 'run.failed', { message: 'agent 未下发该实例' }) : undefined))
    await user.click(within(rowOf('NAS 磁盘')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /立即采集/ }))
    expect(await screen.findByText('NAS 磁盘：采集失败：agent 未下发该实例')).toBeInTheDocument()
  })

  it('device.incompatible 按原因区分升级 agent 还是中枢', async () => {
    const user = userEvent.setup()
    await mount((req) =>
      req.url === '/api/instances/b/run' ? apiError(409, 'device.incompatible', { reason: 'version_too_new', required_min: 1, hub_version: 'v1' }) : undefined,
    )
    await user.click(within(rowOf('NAS 磁盘')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /立即采集/ }))
    expect(await screen.findByText('NAS 磁盘：设备上的 agent 比中枢新，请升级中枢')).toBeInTheDocument()
  })
})

describe('详情抽屉的运行位置与问题', () => {
  it('显示设备名，并给出指向设备详情的链接', async () => {
    const user = userEvent.setup()
    await mount((req) => (req.url === '/api/instances/b' ? json(200, { ...list[1], config: {}, report: null }) : undefined))
    await user.click(screen.getByRole('button', { name: 'NAS 磁盘' }))
    const link = await screen.findByRole('link', { name: '飞牛 NAS 机' })
    expect(link).toHaveAttribute('href', '/devices?open=d1')
  })

  it('下发问题（issue）显示在详情的问题区', async () => {
    const user = userEvent.setup()
    const bad = { ...list[1], display_state: 'unconfigured', issue: '设备上没有该插件' }
    mockApi((req) => (req.url === '/api/instances/b' ? json(200, { ...bad, config: {}, report: null }) : undefined))
    seedStore([bad], { devices })
    await renderWithApp(<InstancesPage />)
    await user.click(screen.getByRole('button', { name: 'NAS 磁盘' }))
    expect(await within(await screen.findByRole('dialog')).findByText('设备上没有该插件')).toBeInTheDocument()
  })
})
