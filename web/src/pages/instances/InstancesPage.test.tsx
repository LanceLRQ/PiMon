import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { liveStore } from '@/store/live-store'
import type { Instance } from '@/types/generated'
import { fixtureNow, makeInstance, prototypeInstances } from './fixtures'
import { InstancesPage } from './InstancesPage'
import { apiError, defaultPlugins, json, mockApi, patchInstance, renderWithApp, seedStore } from './test-utils'

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function bodyRows(): HTMLElement[] {
  return screen.getAllByRole('row').slice(1)
}
function rowNames(): string[] {
  return bodyRows().map((r) => within(r).getAllByRole('button')[0].textContent ?? '')
}
function rowOf(name: string): HTMLElement {
  return bodyRows().find((r) => within(r).queryByRole('button', { name })) as HTMLElement
}

async function mount(list: Instance[] = prototypeInstances, handler?: Parameters<typeof mockApi>[0]) {
  const api = mockApi(handler)
  seedStore(list)
  await renderWithApp(<InstancesPage />)
  return api
}

describe('实例列表页', () => {
  it('默认按状态严重优先排序，名称列两行（名称 + 插件 id）', async () => {
    await mount()
    const names = rowNames()
    expect(names).toHaveLength(17)
    expect(names.slice(0, 2)).toEqual(['ubuntu-srv', '家里 NAS 网页'])
    const first = bodyRows()[0]
    expect(within(first).getByText('host-metrics')).toBeInTheDocument()
    expect(within(first).getByRole('img', { name: '严重' })).toHaveAttribute('data-shape', 'critical')
    expect(screen.getByText(/显示 17 \/ 17/)).toBeInTheDocument()
  })

  it('没有收到 snapshot 时显示加载中而不是空列表', async () => {
    mockApi()
    await renderWithApp(<InstancesPage />)
    expect(screen.getAllByText('加载中').length).toBeGreaterThan(0)
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByText('还没有监控实例')).toBeNull()
  })

  it('没有实例时给出引导', async () => {
    await mount([])
    expect(screen.getByText('还没有监控实例')).toBeInTheDocument()
  })

  it('查询语法过滤，结果数同步，清除筛选恢复', async () => {
    const user = userEvent.setup()
    await mount()
    await user.type(screen.getByLabelText('搜索实例，支持查询语法'), 'status:warning')
    expect(rowNames().sort()).toEqual(['Claude', '网络连通'].sort())
    expect(screen.getByText(/显示 2 \/ 17/)).toBeInTheDocument()
    await user.clear(screen.getByLabelText('搜索实例，支持查询语法'))
    await user.type(screen.getByLabelText('搜索实例，支持查询语法'), 'nothing-matches')
    expect(screen.getByText(/没有匹配的实例/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '清除筛选' }))
    expect(bodyRows()).toHaveLength(17)
  })

  it('需关注、插件菜单与查询叠加', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(screen.getByRole('button', { name: /需关注/ }))
    expect(rowNames()).toHaveLength(4)
    await user.click(screen.getByRole('button', { name: /^插件$/ }))
    await user.click(await screen.findByRole('menuitemradio', { name: 'http-check' }))
    expect(rowNames()).toEqual(['家里 NAS 网页'])
    await user.click(screen.getByRole('button', { name: /插件：http-check/ }))
    await user.click(await screen.findByRole('menuitemradio', { name: '全部插件' }))
    await user.click(screen.getByRole('button', { name: /^全部/ }))
    expect(bodyRows()).toHaveLength(17)
  })

  it('点击表头排序并切换方向', async () => {
    const user = userEvent.setup()
    await mount()
    const nameHead = screen.getByRole('columnheader', { name: /名称/ })
    expect(nameHead).toHaveAttribute('aria-sort', 'none')
    await user.click(within(nameHead).getByRole('button'))
    expect(nameHead).toHaveAttribute('aria-sort', 'ascending')
    const asc = rowNames()
    expect(asc).toEqual([...asc].sort((a, b) => a.localeCompare(b, 'zh')))
    await user.click(within(nameHead).getByRole('button'))
    expect(nameHead).toHaveAttribute('aria-sort', 'descending')
    expect(rowNames()[0]).toBe(asc.at(-1))
  })

  it('patch 到达后只更新对应行，不重新请求实例列表', async () => {
    const { calls } = await mount()
    const before = calls.length
    expect(within(rowOf('ubuntu-srv')).getByText('磁盘 95%')).toBeInTheDocument()
    const old = prototypeInstances[0]
    act(() => patchInstance({ ...old, display_state: 'ok', summary: '磁盘 40%', report_status: 'ok' }))
    const row = rowOf('ubuntu-srv')
    expect(within(row).getByText('磁盘 40%')).toBeInTheDocument()
    expect(within(row).getByRole('img', { name: '正常' })).toBeInTheDocument()
    // 状态变好后不再排第一，其余行不受影响
    expect(rowNames()[0]).toBe('家里 NAS 网页')
    expect(within(rowOf('Claude')).getByText('周剩余 38%')).toBeInTheDocument()
    expect(calls.slice(before).filter((c) => c.url.startsWith('/api/instances'))).toHaveLength(0)
  })

  it('新增与删除通过 patch 反映到表里', async () => {
    await mount()
    act(() => patchInstance(makeInstance({ id: 'new1', name: '新实例', plugin_id: 'demo' })))
    expect(rowNames()).toContain('新实例')
    act(() =>
      liveStore.applyPatch({ type: 'patch', entity: 'instance_removed', id: 'new1', server_time: new Date().toISOString() } as never),
    )
    expect(rowNames()).not.toContain('新实例')
  })

  it('更新于每秒刷新，不发请求', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(fixtureNow)
    const { calls } = await mount()
    const before = calls.length
    expect(within(rowOf('ubuntu-srv')).getByText('12 秒前')).toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(within(rowOf('ubuntu-srv')).getByText('15 秒前')).toBeInTheDocument()
    expect(calls.length).toBe(before)
  })

  it('从未成功的实例显示「从未成功」而不是刚刚', async () => {
    await mount([makeInstance({ id: 'x', name: 'x', display_state: 'error', last_success_at: undefined, last_error: '连接被拒绝' })])
    const row = bodyRows()[0]
    expect(within(row).getByText('从未成功')).toBeInTheDocument()
    expect(within(row).getByText('连接被拒绝')).toBeInTheDocument()
  })

  it('按插件分组', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(screen.getByRole('radio', { name: '按插件分组' }))
    const groupRow = bodyRows().find((r) => r.textContent?.startsWith('http-check') && r.textContent.includes('2 个实例'))
    expect(groupRow).toBeTruthy()
    expect(within(groupRow as HTMLElement).getByRole('img', { name: '采集失败' })).toBeInTheDocument()
    expect(screen.getByText(/显示 17 \/ 17.*· 按插件分组$/)).toBeInTheDocument()
  })

  it('exec 目录提示取自 /api/plugins，重新扫描显示新发现的插件数', async () => {
    const user = userEvent.setup()
    const added = { ...defaultPlugins.plugins[0], id: 'probe', name: '探针' }
    await mount(prototypeInstances, (req) =>
      req.method === 'POST' && req.url.startsWith('/api/plugins/rescan')
        ? json(200, { ...defaultPlugins, plugins: [...defaultPlugins.plugins, added] })
        : undefined,
    )
    expect(await screen.findByText('/var/lib/pimon/plugins')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新扫描' }))
    expect(await screen.findByText(/发现 1 个新插件/)).toBeInTheDocument()
  })
})

describe('手机宽度', () => {
  it('显示只读列表而不是表格，点行打开详情抽屉', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('matchMedia', (q: string) => ({
      matches: q.includes('max-width'),
      media: q,
      addEventListener() {},
      removeEventListener() {},
    }))
    mockApi((req) => (req.url === '/api/instances/i01' ? json(200, { ...prototypeInstances[0], config: {}, report: null }) : undefined))
    seedStore(prototypeInstances)
    await renderWithApp(<InstancesPage />)
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByLabelText('全选')).toBeNull()
    await user.click(screen.getAllByRole('button', { name: /ubuntu-srv/ })[0])
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

describe('实例操作', () => {
  it('立即采集走 POST run；run.busy 提示稍后再试', async () => {
    const user = userEvent.setup()
    const { calls } = await mount(prototypeInstances, (req) =>
      req.url === '/api/instances/i05/run' ? apiError(409, 'run.busy') : req.url === '/api/instances/i06/run' ? json(200, { instance: prototypeInstances[5] }) : undefined,
    )
    await user.click(within(rowOf('树莓派本机')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /立即采集/ }))
    expect(await screen.findByText('该实例正在运行，请稍后再试')).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'POST' && c.url === '/api/instances/i05/run')).toBe(true)

    await user.click(within(rowOf('飞牛 NAS')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /立即采集/ }))
    expect(await screen.findByText('已采集「飞牛 NAS」')).toBeInTheDocument()
  })

  it('采集失败时附上中枢给出的脱敏文字', async () => {
    const user = userEvent.setup()
    await mount(prototypeInstances, (req) =>
      req.url.endsWith('/run') ? apiError(502, 'run.failed', { message: 'exit status 1: boom' }) : undefined,
    )
    await user.click(within(rowOf('树莓派本机')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /立即采集/ }))
    expect(await screen.findByText('采集失败：exit status 1: boom')).toBeInTheDocument()
  })

  it('暂停与恢复按当前状态切换菜单项', async () => {
    const user = userEvent.setup()
    const paused = { ...prototypeInstances[4], paused: true }
    const list = prototypeInstances.map((i) => (i.id === paused.id ? paused : i))
    const { calls } = await mount(list, (req) => (req.url.endsWith('/resume') ? json(200, { ...paused, paused: false }) : undefined))
    expect(within(rowOf('树莓派本机')).getByText('已暂停')).toBeInTheDocument()
    await user.click(within(rowOf('树莓派本机')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /恢复采集/ }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/instances/i05/resume')).toBe(true))
  })

  it('复制为新实例后跳到编辑页', async () => {
    const user = userEvent.setup()
    await mount(prototypeInstances, (req) =>
      req.url === '/api/instances/i05/copy' ? json(201, { ...prototypeInstances[4], id: 'copy1', name: '树莓派本机 (副本)' }) : undefined,
    )
    await user.click(within(rowOf('树莓派本机')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /复制为新实例/ }))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/instances/copy1/edit'))
  })

  it('删除：先确认；取消不发请求；确认后 DELETE', async () => {
    const user = userEvent.setup()
    const { calls } = await mount(prototypeInstances, (req) =>
      req.method === 'DELETE' ? json(200, { affected_screens: [] }) : undefined,
    )
    await user.click(within(rowOf('路由器')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /删除实例/ }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText('删除所选的 1 个实例？')).toBeInTheDocument()
    expect(within(dlg).getByText('路由器')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)

    await user.click(within(rowOf('路由器')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /删除实例/ }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.url)).toEqual(['/api/instances/i14']))
    expect(await screen.findByText('已删除 1 个实例')).toBeInTheDocument()
  })

  it('被 screen 引用时列出受影响的 screen，二次确认后带 confirm=1 再删', async () => {
    const user = userEvent.setup()
    const { calls } = await mount(prototypeInstances, (req) => {
      if (req.method !== 'DELETE') return undefined
      return req.url.includes('confirm=1')
        ? json(200, { affected_screens: [{ id: 's1', name: '客厅' }] })
        : apiError(409, 'instance.in_use', { screens: [{ id: 's1', name: '客厅' }, { id: 's2', name: '书房' }] })
    })
    await user.click(within(rowOf('路由器')).getByRole('button', { name: /更多操作/ }))
    await user.click(await screen.findByRole('menuitem', { name: /删除实例/ }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '删除' }))
    const dlg = await screen.findByRole('dialog')
    expect(await within(dlg).findByText('这些实例仍被屏幕引用')).toBeInTheDocument()
    expect(within(dlg).getByText(/客厅、书房/)).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '仍然删除' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.url)).toEqual(['/api/instances/i14', '/api/instances/i14?confirm=1']))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })
})

describe('批量操作', () => {
  async function selectTwo(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByLabelText('选择 路由器'))
    await user.click(screen.getByLabelText('选择 天气'))
  }

  it('勾选后出现批量条，全选只作用于当前筛选结果', async () => {
    const user = userEvent.setup()
    await mount()
    expect(screen.queryByRole('toolbar')).toBeNull()
    await selectTwo(user)
    expect(screen.getByRole('toolbar')).toHaveTextContent('已选 2 项')
    await user.click(screen.getByRole('button', { name: '取消选择' }))
    expect(screen.queryByRole('toolbar')).toBeNull()

    await user.type(screen.getByLabelText('搜索实例，支持查询语法'), 'plugin:host-metrics')
    await user.click(screen.getByLabelText('全选'))
    expect(screen.getByRole('toolbar')).toHaveTextContent('已选 4 项')
  })

  it('批量删除先确认，确认后逐个 DELETE', async () => {
    const user = userEvent.setup()
    const { calls } = await mount(prototypeInstances, (req) => (req.method === 'DELETE' ? json(200, { affected_screens: [] }) : undefined))
    await selectTwo(user)
    await user.click(within(screen.getByRole('toolbar')).getByRole('button', { name: '删除实例' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText('删除所选的 2 个实例？')).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    await user.click(within(dlg).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(2))
    expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.url).sort()).toEqual(['/api/instances/i12', '/api/instances/i14'])
    await waitFor(() => expect(screen.queryByRole('toolbar')).toBeNull())
  })

  it('批量暂停跳过已暂停的实例；批量采集逐个 POST run', async () => {
    const user = userEvent.setup()
    const list = prototypeInstances.map((i) => (i.id === 'i14' ? { ...i, paused: true } : i))
    const { calls } = await mount(list, (req) => (req.method === 'POST' ? json(200, {}) : undefined))
    await selectTwo(user)
    await user.click(within(screen.getByRole('toolbar')).getByRole('button', { name: '暂停采集' }))
    await waitFor(() => expect(calls.filter((c) => c.url.endsWith('/pause')).map((c) => c.url)).toEqual(['/api/instances/i12/pause']))
    await user.click(within(screen.getByRole('toolbar')).getByRole('button', { name: '立即采集' }))
    await waitFor(() => expect(calls.filter((c) => c.url.endsWith('/run'))).toHaveLength(2))
  })
})
