import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { liveStore } from '@/store/live-store'
import type { PluginInfo, PluginList } from '@/types/generated'
import { device } from '@/pages/devices/test-utils'
import { InstanceEditPage } from '../InstanceEditPage'
import { InstanceNewPage } from '../InstanceNewPage'
import { apiError, json, mockApi, renderWithApp, type ApiHandler, type Req } from '../test-utils'
import { kitchenPlugin, makeDetail, proxyFixtures, simplePlugin } from './editor.fixtures'

// 两处都能跑的插件（含 lookup 与 proxy 字段）
const both: PluginInfo = { ...kitchenPlugin, runs_on: ['hub', 'agent'] }
// 只能在设备上跑的插件
const agentOnly: PluginInfo = { ...simplePlugin, id: 'agentonly', name: '设备专用', origin: 'builtin', runs_on: ['agent'] }
// 只存在于设备 d1 上的 exec 插件
const devProbe: PluginInfo = { ...simplePlugin, id: 'devprobe', name: '设备探针', runs_on: ['hub', 'agent'] }

const list = (plugins: PluginInfo[]): PluginList => ({ plugins, errors: [], conflicts: [], plugin_dir: '' })
const hubList = list([both, agentOnly, simplePlugin])
const d1List = list([both, agentOnly, devProbe])

const devices = [
  device('d1', { name: 'NAS-1', role: 'nas' }),
  device('d2', { name: 'VPS-1', role: 'vps', presence: 'offline' }),
  device('d3', { name: '占位设备', confirmed: false, presence: 'pending' }),
]

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => vi.unstubAllGlobals())

function seedDevices(list: typeof devices = devices) {
  liveStore.applySnapshot({
    type: 'snapshot',
    build: 'test',
    role: 'admin',
    topics: [],
    server_time: new Date().toISOString(),
    instances: [],
    devices: list,
  } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
}

const base: ApiHandler = (req) => {
  const [path, qs] = req.url.split('?')
  if (req.method === 'GET' && path === '/api/plugins') return json(200, qs?.includes('machine=d1') ? d1List : qs?.includes('machine=d2') ? list([both]) : hubList)
  if (req.method === 'GET' && path === '/api/proxies') return json(200, proxyFixtures)
  return undefined
}
const handler = (extra: ApiHandler = () => undefined): ApiHandler => async (req) => (await extra(req)) ?? base(req)

async function mountNew(extra?: ApiHandler) {
  const api = mockApi(handler(extra))
  await renderWithApp(
    <Routes>
      <Route path="/instances/new" element={<InstanceNewPage />} />
      <Route path="/instances/:id/edit" element={<InstanceEditPage />} />
      <Route path="/instances" element={<div>列表页</div>} />
    </Routes>,
    { route: '/instances/new' },
  )
  return api
}

async function mountEdit(detail = makeDetail({ plugin_id: 'kitchen', runs_on: 'd1' }), extra?: ApiHandler) {
  const api = mockApi(
    handler(async (req) => {
      if (req.method === 'GET' && req.url === `/api/instances/${detail.id}`) return json(200, detail)
      return extra?.(req)
    }),
  )
  await renderWithApp(
    <Routes>
      <Route path="/instances/:id/edit" element={<InstanceEditPage />} />
      <Route path="/instances" element={<div>列表页</div>} />
    </Routes>,
    { route: `/instances/${detail.id}/edit` },
  )
  return api
}

const locationSelect = async () => (await screen.findByRole('combobox', { name: '运行位置' })) as HTMLSelectElement
const optionTexts = (sel: HTMLElement) => within(sel).getAllByRole('option').map((o) => o.textContent)
const sentPlugins = (calls: Req[]) => calls.filter((c) => c.method === 'GET' && c.url.startsWith('/api/plugins')).map((c) => c.url)

describe('运行位置选项', () => {
  it('新建：目录上方的选择器列出中枢与已确认的设备（带在线状态），离线设备也可选', async () => {
    seedDevices()
    await mountNew()
    const sel = await locationSelect()
    expect(sel.value).toBe('hub')
    expect(optionTexts(sel)).toEqual(['hub 本机', 'NAS-1 · 在线', 'VPS-1 · 离线'])
  })

  it('不兼容的设备显示「需要升级」且不可选', async () => {
    seedDevices([device('d9', { name: '老设备', incompatible: { reason: 'version_too_old' } as never })])
    await mountNew()
    const opt = within(await locationSelect()).getByRole('option', { name: /老设备/ })
    expect(opt).toBeDisabled()
    expect(opt.textContent).toContain('需要升级 agent')
  })

  it('选了插件才知道约束：只能在 hub 跑的插件不显示设备选项', async () => {
    seedDevices()
    const user = userEvent.setup()
    await mountNew()
    await user.click(await screen.findByRole('button', { name: /简单检测/ }))
    await screen.findByLabelText(/^主机/)
    expect(screen.queryByRole('combobox', { name: '运行位置' })).toBeNull()
  })
})

describe('切换设备后插件、代理随之变化', () => {
  it('选设备后插件目录改用 ?machine=，能选到只存在于该设备的 exec 插件', async () => {
    seedDevices()
    const user = userEvent.setup()
    const api = await mountNew()
    expect(screen.queryByRole('button', { name: /设备探针/ })).toBeNull()
    await user.selectOptions(await locationSelect(), 'd1')
    expect(await screen.findByRole('button', { name: /设备探针/ })).toBeInTheDocument()
    expect(sentPlugins(api.calls).some((u) => u.includes('machine=d1'))).toBe(true)
    // 中枢列表里的 hub 专用插件不在设备目录里
    expect(screen.queryByRole('button', { name: /简单检测/ })).toBeNull()
  })

  it('代理下拉按位置过滤：中枢 hub/lan/any，非 VPS 设备 lan/any，VPS 只有 any', async () => {
    seedDevices()
    const user = userEvent.setup()
    await mountNew()
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    const proxy = async () => within(await screen.findByLabelText(/^代理/)).getAllByRole('option').map((o) => o.textContent)
    await waitFor(async () => expect(await proxy()).toEqual(['直连（不走代理）', 'HK-01 · socks5h', '仅局域网 · http', '通用 · http']))
    await user.selectOptions(await locationSelect(), 'd1')
    await waitFor(async () => expect(await proxy()).toEqual(['直连（不走代理）', '仅局域网 · http', '通用 · http']))
    await user.selectOptions(await locationSelect(), 'd2')
    await waitFor(async () => expect(await proxy()).toEqual(['直连（不走代理）', '通用 · http']))
  })
})

describe('只能在设备上跑的插件', () => {
  it('选中后自动改到第一台可用设备，目录改用该设备的插件', async () => {
    seedDevices()
    const user = userEvent.setup()
    const api = await mountNew()
    await user.click(await screen.findByRole('button', { name: /设备专用/ }))
    await waitFor(async () => expect((await locationSelect()).value).toBe('d1'))
    expect(sentPlugins(api.calls).some((u) => u.includes('machine=d1'))).toBe(true)
    expect(screen.queryByText(/本期暂不能创建/)).toBeNull()
  })

  it('没有可用设备时提示并禁用保存', async () => {
    seedDevices([])
    const user = userEvent.setup()
    await mountNew()
    await user.click(await screen.findByRole('button', { name: /设备专用/ }))
    expect(await screen.findByText(/当前没有可用设备/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
  })
})

describe('离线设备与请求体', () => {
  it('选离线设备时提示，保存并测试不可用，保存请求带 runs_on', async () => {
    seedDevices()
    const user = userEvent.setup()
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'n', runs_on: 'd2' }))
    })
    await user.selectOptions(await locationSelect(), 'd2')
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    expect(await screen.findByText('设备离线，保存后会在设备上线时生效；测试运行不可用。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /保存并测试/ })).toBeDisabled()
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('列表页')
    const body = [...api.calls].reverse().find((c) => c.method === 'POST' && c.url === '/api/instances')?.body as Record<string, unknown>
    expect(body.runs_on).toBe('d2')
  })

  it('在线设备不提示离线，中枢保存带 runs_on=hub', async () => {
    seedDevices()
    const user = userEvent.setup()
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'n' }))
    })
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    expect(screen.queryByText(/设备离线/)).toBeNull()
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('列表页')
    expect([...api.calls].reverse().find((c) => c.method === 'POST' && c.url === '/api/instances')?.body).toMatchObject({ runs_on: 'hub' })
  })
})

describe('lookup 在设备上降级', () => {
  it('位置不是中枢时 lookup 变成普通文本输入并提示，不发候选查询', async () => {
    seedDevices()
    const user = userEvent.setup()
    const api = await mountNew()
    await user.selectOptions(await locationSelect(), 'd1')
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    const city = await screen.findByLabelText(/^城市/)
    expect(city).not.toHaveAttribute('role', 'combobox')
    expect(screen.getByText('设备上的插件不支持候选查询，请直接填写。')).toBeInTheDocument()
    await user.type(city, 'Paris')
    expect(city).toHaveValue('Paris')
    expect(api.calls.some((c) => c.url.includes('/lookup/'))).toBe(false)
  })

  it('中枢上仍是候选查询控件', async () => {
    seedDevices()
    const user = userEvent.setup()
    await mountNew()
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    expect(await screen.findByLabelText(/^城市/)).toHaveAttribute('role', 'combobox')
  })
})

describe('编辑已有实例', () => {
  it('按实例所在设备取插件，选择器显示当前设备', async () => {
    seedDevices()
    const api = await mountEdit()
    expect((await locationSelect()).value).toBe('d1')
    expect(sentPlugins(api.calls).every((u) => u.includes('machine=d1'))).toBe(true)
  })

  it('只能在中枢跑的插件：编辑页不给设备选项，只读显示并说明', async () => {
    seedDevices()
    await mountEdit(makeDetail({ plugin_id: 'simple', runs_on: 'hub' }))
    expect(await screen.findByText('这个插件只能在中枢本机运行。')).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '运行位置' })).toBeNull()
  })

  it('改运行位置时提示私有状态从头开始，保存带新的 runs_on', async () => {
    seedDevices()
    const user = userEvent.setup()
    const api = await mountEdit(makeDetail({ plugin_id: 'kitchen', runs_on: 'd1' }), (req) => {
      if (req.method === 'PUT') return json(200, makeDetail({ runs_on: 'hub' }))
    })
    const sel = await locationSelect()
    expect(screen.queryByText(/私有状态会从头开始/)).toBeNull()
    await user.selectOptions(sel, 'hub')
    expect(await screen.findByText(/私有状态会从头开始/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(api.calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ runs_on: 'hub' })
  })

  it('所选位置上没有这个插件时给出提示，并保留选择器以便改回', async () => {
    seedDevices()
    const user = userEvent.setup()
    await mountEdit(makeDetail({ plugin_id: 'devprobe', runs_on: 'd1' }))
    const sel = await locationSelect()
    await user.selectOptions(sel, 'hub')
    expect(await screen.findByText(/插件「devprobe」在所选位置不可用/)).toBeInTheDocument()
    expect((await locationSelect()).value).toBe('hub')
    await user.selectOptions(await locationSelect(), 'd1')
    expect(await screen.findByLabelText(/^主机/)).toBeInTheDocument()
  })
})

describe('暂停的远程实例与测试结果来源', () => {
  const okRun = (runsOn: string) => ({
    instance: { ...makeDetail({ runs_on: runsOn }), display_state: 'ok', last_error: '' },
    report: { status: 'ok', summary: '好', duration_ms: 10, collected_at: 1, items: [] },
  })

  it('已暂停且在设备上：「保存并测试」禁用并说明原因，保存仍可用', async () => {
    seedDevices()
    await mountEdit(makeDetail({ plugin_id: 'kitchen', runs_on: 'd1', paused: true }))
    await screen.findByLabelText(/^地址/)
    expect(screen.getByRole('button', { name: /保存并测试/ })).toBeDisabled()
    expect(screen.getByText('已暂停的设备实例不能测试，请先恢复')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeEnabled()
  })

  it('已暂停但在中枢上：照常可以测试', async () => {
    seedDevices()
    await mountEdit(makeDetail({ plugin_id: 'kitchen', runs_on: 'hub', paused: true }))
    await screen.findByLabelText(/^地址/)
    expect(screen.getByRole('button', { name: /保存并测试/ })).toBeEnabled()
    expect(screen.queryByText('已暂停的设备实例不能测试，请先恢复')).toBeNull()
  })

  it('测试成功时结果里显示来源设备名', async () => {
    seedDevices()
    const user = userEvent.setup()
    const detail = makeDetail({ plugin_id: 'kitchen', runs_on: 'd1' })
    await mountEdit(detail, (req) => {
      if (req.method === 'PUT') return json(200, detail)
      if (req.method === 'POST' && req.url === `/api/instances/${detail.id}/run`) return json(200, okRun('d1'))
    })
    await screen.findByLabelText(/^地址/)
    await user.click(screen.getByRole('button', { name: /保存并测试/ }))
    expect(await screen.findByText('来源设备：NAS-1')).toBeInTheDocument()
  })

  it('测试失败时同样显示来源设备，不兼容按原因出文案', async () => {
    seedDevices()
    const user = userEvent.setup()
    const detail = makeDetail({ plugin_id: 'kitchen', runs_on: 'd1' })
    await mountEdit(detail, (req) => {
      if (req.method === 'PUT') return json(200, detail)
      if (req.method === 'POST' && req.url === `/api/instances/${detail.id}/run`)
        return apiError(409, 'device.incompatible', { reason: 'version_too_old' })
    })
    await screen.findByLabelText(/^地址/)
    await user.click(screen.getByRole('button', { name: /保存并测试/ }))
    expect(await screen.findByText('来源设备：NAS-1')).toBeInTheDocument()
    expect(screen.getAllByText('设备上的 agent 版本过旧，请升级 agent').length).toBeGreaterThan(0)
  })
})
