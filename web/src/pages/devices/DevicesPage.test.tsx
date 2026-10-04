import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeInstance } from '@/pages/instances/instances.fixtures'
import { json, mockApi, renderWithApp, seedStore } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { Device, SystemInfo } from '@/types/generated'
import { DevicesPage } from './DevicesPage'
import { device } from './test-utils'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

const sys = { version: 'v1.0.0', os: 'linux', arch: 'arm64', uptime_seconds: 3 * 86400 + 3600 } as SystemInfo

const nas = device('d1', { name: '飞牛 NAS', role: 'nas', address: '192.168.1.20', hostname: 'fnos', last_seen_at: new Date().toISOString() })
const ubuntu = device('d2', { name: 'Ubuntu 服务器', presence: 'offline', hostname: 'ubuntu-srv', address: '192.168.1.31', maintenance: true })
const vps = device('d3', { name: 'VPS 东京', role: 'vps', direction: 'dial_out', address: 'wss://203.0.113.8:31418', proxy_id: 'p1', presence: 'disconnected' })
const mac = device('d4', { name: 'MacBook', role: 'dev', presence: 'dormant', allow_offline: true, os: 'darwin', arch: 'arm64' })

async function mount(devices: Device[] | null, opts: { route?: string; mobile?: boolean } = {}) {
  if (opts.mobile) {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} }))
  }
  const api = mockApi((req) => (req.url === '/api/system' ? json(200, sys) : undefined))
  seedStore([makeInstance({ id: 'i1', runs_on: 'd1' }), makeInstance({ id: 'i2', runs_on: 'd1' }), makeInstance({ id: 'i3', runs_on: 'hub' })], devices ? { devices } : {})
  await renderWithApp(<DevicesPage />, { route: opts.route ?? '/devices' })
  return api
}

function bodyRows() {
  // 抽屉打开时页面其余部分被标为隐藏，行仍然在 DOM 里
  return screen.getAllByRole('row', { hidden: true }).slice(1)
}
function rowOf(name: string): HTMLElement {
  return bodyRows().find((r) => within(r).queryByRole('button', { name: new RegExp(name), hidden: true })) as HTMLElement
}

describe('设备列表', () => {
  it('中枢固定首行，显示版本、系统与已运行时间；设备按库里顺序排在其后', async () => {
    await mount([nas, ubuntu])
    const rows = bodyRows()
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveAttribute('data-device-id', 'hub')
    expect(within(rows[0]).getAllByText('中枢').length).toBeGreaterThan(0)
    expect(await within(rows[0]).findByText('v1.0.0')).toBeInTheDocument()
    expect(within(rows[0]).getByText('linux/arm64')).toBeInTheDocument()
    expect(within(rows[0]).getByText('已运行 3 天 1 小时')).toBeInTheDocument()
    expect(within(rows[0]).getByText('本机')).toBeInTheDocument()
    expect(within(rows[1]).getByText('飞牛 NAS')).toBeInTheDocument()
  })

  it('五种在线状态各用专用形状与文案，拨出设备显示代理', async () => {
    const pending = device('d5', { name: '书房小主机', presence: 'pending', confirmed: false })
    await mount([nas, ubuntu, vps, mac, pending])
    const expectState = (name: string, shape: string, label: string) => {
      const row = rowOf(name)
      expect(row.querySelector(`svg[data-shape="${shape}"]`)).not.toBeNull()
      expect(within(row).getAllByText(label).length).toBeGreaterThan(0)
    }
    expectState('飞牛 NAS', 'ok', '在线')
    expectState('Ubuntu 服务器', 'offline', '离线')
    expectState('VPS 东京', 'disconnected', '连接中断，等待重连')
    expectState('MacBook', 'dormant', '休眠')
    expectState('书房小主机', 'pending', '等待连接')
    expect(within(rowOf('VPS 东京')).getByText('拨出')).toBeInTheDocument()
    expect(within(rowOf('VPS 东京')).getByText('经 p1')).toBeInTheDocument()
    expect(within(rowOf('飞牛 NAS')).getByText('拨入')).toBeInTheDocument()
  })

  it('实例数按 runs_on 计，允许离线与维护中各自标出', async () => {
    await mount([nas, ubuntu, mac])
    const cells = within(rowOf('飞牛 NAS')).getAllByRole('cell')
    expect(cells.some((c) => c.textContent === '2')).toBe(true)
    expect(within(rowOf('Ubuntu 服务器')).getByText('维护中')).toBeInTheDocument()
    expect(within(rowOf('MacBook')).getByText('允许离线')).toBeInTheDocument()
    expect(within(rowOf('飞牛 NAS')).getByText('不允许离线')).toBeInTheDocument()
  })

  it('版本两级标记：不兼容为「需要升级 agent/中枢」，仅版本不同为较弱的「版本不同」，相同则无标记', async () => {
    const same = device('s', { name: '同版本', agent_version: 'v1.0.0' })
    const differs = device('df', { name: '版本不同机', agent_version: 'v0.9.0' })
    const old = device('o', { name: '过旧机', agent_version: 'v0.1.0', incompatible: { reason: 'version_too_old' } as never })
    const fresh = device('f', { name: '过新机', agent_version: 'v9.0.0', incompatible: { reason: 'version_too_new' } as never })
    await mount([same, differs, old, fresh])
    await within(bodyRows()[0]).findByText('v1.0.0')
    expect(within(rowOf('同版本')).queryByText(/升级|版本不同/)).toBeNull()
    expect(within(rowOf('版本不同机')).getByText('版本不同')).toBeInTheDocument()
    expect(within(rowOf('过旧机')).getByText('需要升级 agent')).toBeInTheDocument()
    expect(within(rowOf('过新机')).getByText('需要升级中枢')).toBeInTheDocument()
  })

  it('凭据失效的拨出设备单独提示需要重新配对', async () => {
    await mount([{ ...vps, conn_error: 'bad_credential' }])
    expect(within(rowOf('VPS 东京')).getByText('凭据已失效，需要重新配对')).toBeInTheDocument()
  })

  it('WS 补丁更新对应行，device_removed 后该行消失', async () => {
    await mount([nas, ubuntu])
    expect(rowOf('Ubuntu 服务器').querySelector('svg[data-shape="offline"]')).not.toBeNull()
    act(() =>
      liveStore.applyPatch({ type: 'patch', entity: 'device', server_time: new Date().toISOString(), device: { ...ubuntu, presence: 'online' } } as never),
    )
    expect(rowOf('Ubuntu 服务器').querySelector('svg[data-shape="ok"]')).not.toBeNull()
    act(() => liveStore.applyPatch({ type: 'patch', entity: 'device_removed', server_time: new Date().toISOString(), id: 'd2' } as never))
    expect(screen.queryByText('Ubuntu 服务器')).toBeNull()
    expect(bodyRows()).toHaveLength(2)
  })
})

describe('占位设备', () => {
  it('四种占位分别标识；悬挂占位可删除，走 DELETE 并提示', async () => {
    const user = userEvent.setup()
    const wait = device('p1', { name: '待连接', confirmed: false, presence: 'pending' })
    const repair = device('p2', { name: '重配占位', confirmed: false, presence: 'pending', replaces: 'd1' })
    const dangling = device('p3', { name: '悬挂占位机', confirmed: false, presence: 'pending', replaces: 'gone' })
    const upgrade = device('p4', { name: '升级占位', confirmed: false, presence: 'pending', incompatible: { reason: 'version_too_old' } as never })
    const api = await mount([nas, wait, repair, dangling, upgrade])
    expect(within(rowOf('待连接')).getAllByText('等待连接')).toHaveLength(1)
    expect(within(rowOf('待连接')).getByText('尚未连接，请在配对码有效期内执行安装命令')).toBeInTheDocument()
    expect(within(rowOf('重配占位')).getByText('重新配对中（替换 飞牛 NAS）')).toBeInTheDocument()
    expect(within(rowOf('悬挂占位机')).getByText('悬挂占位')).toBeInTheDocument()
    expect(within(rowOf('升级占位')).getByText('需要升级后才能接入')).toBeInTheDocument()
    expect(within(rowOf('待连接')).queryByRole('button', { name: '删除占位' })).toBeNull()
    await user.click(within(rowOf('悬挂占位机')).getByRole('button', { name: '删除占位' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '删除' }))
    await vi.waitFor(() => expect(api.calls.some((c) => c.method === 'DELETE' && c.url === '/api/devices/p3')).toBe(true))
  })
})

describe('筛选与搜索', () => {
  it('按状态筛选，中枢不参与；搜索按名称、主机名、地址', async () => {
    const user = userEvent.setup()
    await mount([nas, ubuntu, vps, mac])
    await user.click(screen.getByRole('button', { name: /^异常/ }))
    expect(bodyRows()).toHaveLength(3)
    expect(bodyRows()[0]).toHaveAttribute('data-device-id', 'hub')
    expect(screen.queryByText('飞牛 NAS')).toBeNull()
    await user.click(screen.getByRole('button', { name: /^全部/ }))
    await user.type(screen.getByRole('searchbox', { name: '搜索设备' }), 'ubuntu-srv')
    expect(bodyRows()).toHaveLength(2)
    expect(screen.getByText('Ubuntu 服务器')).toBeInTheDocument()
    await user.clear(screen.getByRole('searchbox', { name: '搜索设备' }))
    await user.type(screen.getByRole('searchbox', { name: '搜索设备' }), '没有这台')
    expect(screen.getByText(/没有匹配的设备/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '清除筛选' }))
    expect(bodyRows()).toHaveLength(5)
  })

  it('汇总条按状态计数', async () => {
    await mount([nas, ubuntu, vps, mac])
    const strip = screen.getByRole('region', { name: '设备状态汇总' })
    const cell = (label: string) => within(strip).getByText(label).closest('div')!.parentElement!
    expect(cell('在线')).toHaveTextContent('1')
    expect(cell('离线')).toHaveTextContent('1')
    expect(cell('休眠')).toHaveTextContent('1')
  })
})

describe('空状态与未加载', () => {
  it('列表为空显示引导与「添加设备」，中枢仍在', async () => {
    await mount([])
    expect(screen.getByText('还没有添加设备')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: '添加设备' }).length).toBeGreaterThan(0)
    expect(bodyRows()[0]).toHaveAttribute('data-device-id', 'hub')
  })

  it('设备列表未加载（null）显示加载中，不显示空状态', async () => {
    await mount(null)
    expect(screen.queryByText('还没有添加设备')).toBeNull()
    expect(screen.getByText('设备列表加载中')).toBeInTheDocument()
    expect(bodyRows()[0]).toHaveAttribute('data-device-id', 'hub')
  })

  it('实例未同步时中枢的实例数显示「—」而不是 0', async () => {
    mockApi((req) => (req.url === '/api/system' ? json(200, sys) : undefined))
    await renderWithApp(<DevicesPage />)
    const hubRow = bodyRows()[0]
    expect(within(hubRow).queryByText('0')).toBeNull()
    expect(within(hubRow).getAllByText('—').length).toBeGreaterThan(0)
  })

  it('手机宽度下页头的「添加设备」按钮仍可见可点', async () => {
    const user = userEvent.setup()
    await mount([nas], { mobile: true })
    const btn = screen.getAllByRole('button', { name: '添加设备' })[0]
    expect(btn.closest('.mobile\\:hidden')).toBeNull()
    await user.click(btn)
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('「添加设备」按钮打开占位对话框', async () => {
    const user = userEvent.setup()
    await mount([nas])
    await user.click(screen.getAllByRole('button', { name: '添加设备' })[0])
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

describe('深链与手机卡片', () => {
  it('?open=<id> 高亮对应设备行并打开详情抽屉；点击行写入 ?open', async () => {
    const user = userEvent.setup()
    await mount([nas, ubuntu], { route: '/devices?open=d2' })
    expect(rowOf('Ubuntu 服务器')).toHaveAttribute('aria-current', 'true')
    expect(rowOf('飞牛 NAS')).not.toHaveAttribute('aria-current')
    expect(await screen.findByRole('dialog', { name: /设备详情：Ubuntu 服务器/ })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(within(rowOf('飞牛 NAS')).getByRole('button', { name: /飞牛 NAS/, hidden: true }))
    expect(rowOf('飞牛 NAS')).toHaveAttribute('aria-current', 'true')
    expect(rowOf('Ubuntu 服务器')).not.toHaveAttribute('aria-current')
    expect(await screen.findByRole('dialog', { name: /设备详情：飞牛 NAS/ })).toBeInTheDocument()
  })

  it('手机宽度显示卡片而不是表格，卡片可点开', async () => {
    const user = userEvent.setup()
    await mount([nas, ubuntu], { mobile: true })
    expect(screen.queryByRole('table')).toBeNull()
    const cards = screen.getAllByRole('listitem')
    expect(cards).toHaveLength(3)
    await user.click(within(cards[2]).getByRole('button'))
    expect(cards[2]).toHaveAttribute('aria-current', 'true')
  })
})

describe('添加设备入口', () => {
  it('页头与空状态的添加设备都打开对话框，默认在拨入标签', async () => {
    await mount([])
    await userEvent.click(screen.getAllByRole('button', { name: '添加设备' })[0])
    const dialog = await screen.findByRole('dialog', { name: '添加设备' })
    expect(within(dialog).getByRole('tab', { name: /拨入/ })).toHaveAttribute('aria-selected', 'true')
    expect(within(dialog).getByRole('tab', { name: /拨出/ })).toHaveAttribute('aria-selected', 'false')
  })
})
