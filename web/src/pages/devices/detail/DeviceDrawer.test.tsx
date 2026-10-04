import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { createMemoryRouter, RouterProvider, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { makeInstance } from '@/pages/instances/instances.fixtures'
import { apiError, json, mockApi, renderWithApp, seedStore, type ApiHandler } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { Device, Proxy } from '@/types/generated'
import { ToastProvider } from '@/ui/toast'
import { DevicesPage } from '../DevicesPage'
import { device } from '../test-utils'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

const nas = device('d1', { name: '飞牛 NAS', role: 'nas', address: '192.168.1.20', hostname: 'fnos', agent_version: 'v1.2.3', os: 'linux', arch: 'amd64', protocol_version: 3, clock_skew_ms: 120 })
const vps = device('d3', {
  name: 'VPS 东京', role: 'vps', direction: 'dial_out', address: 'wss://203.0.113.8:31418', proxy_id: 'p1', agent_version: 'v1.2.3', presence: 'online',
})
const mac = device('d4', { name: 'MacBook', role: 'dev', os: 'darwin', arch: 'arm64', agent_version: 'v1.2.3' })

const proxyList: Proxy[] = [
  { id: 'p1', name: 'HK-01', scheme: 'socks5', address: '10.0.0.2:1080', remote_dns: true, location: 'any' } as Proxy,
  { id: 'p2', name: '家里 http', scheme: 'http', address: '10.0.0.3:8080', remote_dns: true, location: 'lan' } as Proxy,
  { id: 'p3', name: '只给 hub', scheme: 'http', address: '10.0.0.4:8080', remote_dns: true, location: 'hub' } as Proxy,
]

function SearchProbe() {
  return <div data-testid="search">{useLocation().search}</div>
}

async function mount(devices: Device[], route: string, handler: ApiHandler = () => undefined, version = 'v1.2.3') {
  const api = mockApi((req) => {
    const path = req.url.split('?')[0]
    if (path === '/api/system') return json(200, { version, os: 'linux', arch: 'arm64', uptime_seconds: 3600 })
    if (path === '/api/proxies') return json(200, proxyList)
    if (path === '/api/agent/info') {
      return json(200, { agent_port: 31418, fingerprint: 'SHA256:abcdef', hub_version: version, protocol_version: 3, min_compatible_version: 2, platforms: ['linux-amd64', 'darwin-arm64'], release_url_template: '', release_checksums: {} })
    }
    return handler(req)
  })
  seedStore([makeInstance({ id: 'i1', name: '主机指标', runs_on: 'd1', plugin_id: 'host-metrics' }), makeInstance({ id: 'i3', name: '本机温度', runs_on: 'hub' })], { devices })
  await renderWithApp(<><DevicesPage /><SearchProbe /></>, { route })
  return api
}

const drawer = () => screen.findByRole('dialog', { name: /设备详情/ })

describe('设备详情抽屉：属性编辑与保存', () => {
  it('点行打开抽屉，改名后显示未保存改动，保存发出 PUT 并清除未保存标记', async () => {
    const user = userEvent.setup()
    const api = await mount([nas, vps], '/devices?open=d1', (req) => {
      if (req.method === 'PUT' && req.url === '/api/devices/d1') return json(200, { ...nas, name: '新名字', updated_at: '2026-10-05T00:00:00Z' })
    })
    const dlg = await drawer()
    const name = within(dlg).getByLabelText('名称')
    expect(name).toHaveValue('飞牛 NAS')
    expect(within(dlg).queryByText(/未保存改动/)).toBeNull()
    expect(within(dlg).getByRole('button', { name: '保存' })).toBeDisabled()
    await user.clear(name)
    await user.type(name, '新名字')
    expect(within(dlg).getByText('未保存改动 1')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('switch', { name: '维护模式' }))
    expect(within(dlg).getByText('未保存改动 2')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(within(dlg).queryByText(/未保存改动/)).toBeNull())
    const put = api.calls.find((c) => c.method === 'PUT')
    expect(put?.body).toEqual({ name: '新名字', role: 'nas', allow_offline: false, maintenance: true })
    expect(screen.getByText('已保存设备「新名字」')).toBeInTheDocument()
    expect(within(dlg).getByLabelText('名称')).toHaveValue('新名字')
  })

  it('改回原值时不算改动；名称为空时禁止保存', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices?open=d1')
    const dlg = await drawer()
    const sw = within(dlg).getByRole('switch', { name: '允许离线' })
    await user.click(sw)
    expect(within(dlg).getByText('未保存改动 1')).toBeInTheDocument()
    await user.click(sw)
    expect(within(dlg).queryByText(/未保存改动/)).toBeNull()
    await user.clear(within(dlg).getByLabelText('名称'))
    expect(within(dlg).getByText('名称不能为空')).toBeInTheDocument()
    expect(within(dlg).getByRole('button', { name: '保存' })).toBeDisabled()
  })

  it('拨入设备没有地址与代理字段；拨出设备可改地址与代理，代理只列中枢可用的', async () => {
    const user = userEvent.setup()
    const api = await mount([nas, vps], '/devices?open=d3', (req) => {
      if (req.method === 'PUT') return json(200, { ...vps, updated_at: '2026-10-05T00:00:00Z' })
    })
    const dlg = await drawer()
    expect(within(dlg).getByLabelText('地址')).toHaveValue('203.0.113.8:31418')
    const proxy = within(dlg).getByLabelText('代理')
    await waitFor(() => expect(within(proxy).getAllByRole('option').map((o) => o.textContent)).toEqual(['直连', 'HK-01', '家里 http', '只给 hub']))
    expect(proxy).toHaveValue('p1')
    await user.selectOptions(proxy, '')
    await user.clear(within(dlg).getByLabelText('地址'))
    await user.type(within(dlg).getByLabelText('地址'), '198.51.100.9:41418')
    expect(within(dlg).getByText('未保存改动 2')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(api.calls.find((c) => c.method === 'PUT')?.body).toEqual({
      name: 'VPS 东京', role: 'vps', allow_offline: false, maintenance: false, address: 'wss://198.51.100.9:41418', proxy_id: '',
    })
  })

  it('拨入设备的抽屉里没有地址输入框', async () => {
    await mount([nas], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).queryByLabelText('地址')).toBeNull()
  })

  it('保存遇到 422 proxy.location_mismatch 时按 details.instances 列出冲突实例', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices?open=d1', (req) => {
      if (req.method === 'PUT') return apiError(422, 'proxy.location_mismatch', { instances: [{ id: 'i1', name: '主机指标' }, { id: 'i2', name: '探测' }] })
    })
    const dlg = await drawer()
    await user.selectOptions(within(dlg).getByLabelText('角色'), 'vps')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    expect(await within(dlg).findByText(/主机指标、探测/)).toBeInTheDocument()
    expect(within(dlg).getByText('未保存改动 1')).toBeInTheDocument()
  })

  it('字段校验失败显示在对应字段旁', async () => {
    const user = userEvent.setup()
    await mount([vps], '/devices?open=d3', (req) => {
      if (req.method === 'PUT') return apiError(400, 'validation.failed', { fields: { address: 'invalid' } })
    })
    const dlg = await drawer()
    await user.type(within(dlg).getByLabelText('地址'), 'x')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    expect(await within(dlg).findByText(/地址格式不对/)).toBeInTheDocument()
  })
})

describe('设备详情抽屉：未保存改动提示', () => {
  it('有改动时取消先确认：继续编辑保留改动，放弃改动关闭抽屉并清除 ?open=', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices?open=d1')
    const dlg = await drawer()
    await user.type(within(dlg).getByLabelText('名称'), '2')
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    const confirm = await screen.findByRole('dialog', { name: '放弃未保存的改动？' })
    await user.click(within(confirm).getByRole('button', { name: '继续编辑' }))
    expect(screen.queryByRole('dialog', { name: '放弃未保存的改动？' })).toBeNull()
    expect(within(dlg).getByLabelText('名称')).toHaveValue('飞牛 NAS2')
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    await user.click(within(await screen.findByRole('dialog', { name: '放弃未保存的改动？' })).getByRole('button', { name: '放弃改动' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(screen.getByTestId('search')).toHaveTextContent('')
  })

  it('没有改动时直接关闭；撤销全部还原所有改动', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices?open=d1')
    const dlg = await drawer()
    await user.type(within(dlg).getByLabelText('名称'), 'x')
    await user.click(within(dlg).getByRole('button', { name: '撤销全部' }))
    expect(within(dlg).getByLabelText('名称')).toHaveValue('飞牛 NAS')
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(screen.getByTestId('search')).toHaveTextContent('')
  })

  it('数据路由里放弃改动关闭抽屉时不再弹第二次离开保护', async () => {
    const user = userEvent.setup()
    mockApi((req) => (req.url === '/api/system' ? json(200, { version: 'v1.2.3', os: 'linux', arch: 'arm64', uptime_seconds: 1 }) : undefined))
    seedStore([], { devices: [nas] })
    const i18n = await createI18n('zh')
    const router = createMemoryRouter([{ path: '/devices', element: <DevicesPage /> }], { initialEntries: ['/devices?open=d1'] })
    render(
      <I18nextProvider i18n={i18n}>
        <ToastProvider>
          <RouterProvider router={router} />
        </ToastProvider>
      </I18nextProvider>,
    )
    const dlg = await drawer()
    await user.type(within(dlg).getByLabelText('名称'), 'x')
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    await user.click(within(await screen.findByRole('dialog', { name: '放弃未保存的改动？' })).getByRole('button', { name: '放弃改动' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('设备详情抽屉：连接、版本与升级', () => {
  it('连接信息：在线显示已连接时长，其余显示最后在线；时钟偏移与协议版本', async () => {
    const seen = new Date(Date.now() - 2 * 3600_000 - 5 * 60_000).toISOString()
    await mount([{ ...nas, last_seen_at: seen }], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).getByText('已连接时长').nextSibling).toHaveTextContent('2 小时 5 分')
    expect(within(dlg).getByText('+120 ms')).toBeInTheDocument()
    expect(within(dlg).getByText('fnos')).toBeInTheDocument()
    expect(within(dlg).getByText(/192\.168\.1\.20/)).toHaveTextContent('最近来源 IP')
  })

  it('离线设备显示最后在线而不是已连接时长', async () => {
    const seen = new Date(Date.now() - 3 * 3600_000).toISOString()
    await mount([{ ...nas, presence: 'offline', last_seen_at: seen }], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).queryByText('已连接时长')).toBeNull()
    expect(within(dlg).getByText('最后在线').nextSibling).toHaveTextContent('3 小时前')
  })

  it('拨入 Linux 设备：sudo pimon-agent upgrade；darwin 不加 sudo', async () => {
    await mount([nas, mac], '/devices?open=d1')
    expect(await within(await drawer()).findByText('sudo pimon-agent upgrade')).toBeInTheDocument()
  })

  it('darwin 拨入设备的升级命令不带 sudo', async () => {
    await mount([mac], '/devices?open=d4')
    const dlg = await drawer()
    expect(await within(dlg).findByText('pimon-agent upgrade')).toBeInTheDocument()
    expect(within(dlg).queryByText(/sudo/)).toBeNull()
  })

  it('拨出设备带 --release 中枢版本，经代理时提示 --proxy', async () => {
    await mount([vps], '/devices?open=d3')
    const dlg = await drawer()
    expect(within(dlg).getByText('sudo pimon-agent upgrade --release v1.2.3')).toBeInTheDocument()
    expect(await within(dlg).findByText(/--proxy socks5h:\/\/10\.0\.0\.2:1080/)).toBeInTheDocument()
  })

  it('中枢是开发构建时拨出设备不给命令并提示；拨入设备仍有命令', async () => {
    await mount([vps], '/devices?open=d3', () => undefined, 'dev')
    const dlg = await drawer()
    expect(await within(dlg).findByText(/当前中枢不是正式发布版本，没有对应的 Release/)).toBeInTheDocument()
    expect(within(dlg).queryByText(/pimon-agent upgrade/)).toBeNull()
  })

  it('版本与中枢一致或不同的标记', async () => {
    await mount([nas, { ...mac, agent_version: 'v1.0.0' }], '/devices?open=d4')
    expect(await within(await drawer()).findByText('版本不同')).toBeInTheDocument()
  })

  it('不兼容：抽屉顶部醒目提示，按原因区分升级对象', async () => {
    const old = { ...nas, incompatible: { reason: 'version_too_old', required_min: 2 } }
    await mount([old], '/devices?open=d1')
    const banner = within(await drawer()).getAllByRole('alert')[0]
    expect(banner).toHaveTextContent('需要升级 agent')
    expect(banner).toHaveTextContent('版本过旧')
    expect(banner).toHaveTextContent('不低于 2')
  })

  it('agent 比中枢新：提示升级中枢，不给 agent 升级命令', async () => {
    const newer = { ...nas, incompatible: { reason: 'version_too_new', required_min: 4 } }
    await mount([newer], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).getAllByRole('alert')[0]).toHaveTextContent('需要升级中枢')
    expect(within(dlg).getByText(/升级中枢后 agent 会自动重连/)).toBeInTheDocument()
    expect(within(dlg).queryByText('sudo pimon-agent upgrade')).toBeNull()
  })

  it('等待连接的占位只提示设备连上后显示版本', async () => {
    await mount([device('d9', { name: '新设备', presence: 'pending', confirmed: false, agent_version: '' })], '/devices?open=d9')
    expect(within(await drawer()).getByText('设备连上之后显示版本。')).toBeInTheDocument()
  })

  it('实例列表只列该设备上的实例，并链到实例页', async () => {
    await mount([nas], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).getByText('主机指标')).toBeInTheDocument()
    expect(within(dlg).queryByText('本机温度')).toBeNull()
    expect(within(dlg).getByRole('link', { name: '在实例列表查看' })).toHaveAttribute('href', '/instances')
  })

  it('吊销与重新配对入口未接入时禁用', async () => {
    await mount([nas], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).getByRole('button', { name: /吊销设备/ })).toBeDisabled()
    expect(within(dlg).getByRole('button', { name: /重新配对/ })).toBeDisabled()
  })
})

describe('设备详情抽屉：日志', () => {
  const logs = (entries: { level: string; message: string }[]) => json(200, { entries: entries.map((e) => ({ time: '2026-10-04T10:00:00Z', attrs: '', ...e })), capacity: 2000 })

  it('在线设备加载日志，按级别筛选与刷新各取一次，不自动轮询', async () => {
    const user = userEvent.setup()
    const api = await mount([nas], '/devices?open=d1', (req) => {
      if (req.url.startsWith('/api/devices/d1/logs')) return logs([{ level: 'info', message: 'agent 已启动' }])
    })
    const dlg = await drawer()
    expect(await within(dlg).findByText('agent 已启动')).toBeInTheDocument()
    const logCalls = () => api.calls.filter((c) => c.url.startsWith('/api/devices/d1/logs'))
    expect(logCalls()).toHaveLength(1)
    expect(logCalls()[0].url).toContain('limit=200')
    expect(logCalls()[0].url).not.toContain('level=')
    await user.click(within(dlg).getByRole('radio', { name: 'warn' }))
    await waitFor(() => expect(logCalls()).toHaveLength(2))
    expect(logCalls()[1].url).toContain('level=warn')
    await user.click(within(dlg).getByRole('button', { name: '刷新日志' }))
    await waitFor(() => expect(logCalls()).toHaveLength(3))
  })

  it('设备不在线时不请求日志，提示不可用', async () => {
    const api = await mount([{ ...nas, presence: 'offline' }], '/devices?open=d1')
    const dlg = await drawer()
    expect(within(dlg).getByText(/设备当前不在线，日志不可用/)).toBeInTheDocument()
    expect(within(dlg).getByRole('button', { name: '刷新日志' })).toBeDisabled()
    expect(api.calls.some((c) => c.url.includes('/logs'))).toBe(false)
  })

  it.each([
    ['run.timeout', 504, {}, '设备没有及时返回日志，请稍后刷新'],
    ['run.failed', 502, { message: 'agent 内部错误' }, '拉取日志失败：agent 内部错误'],
    ['device.offline', 409, {}, '设备离线，无法查看日志'],
    ['device.incompatible', 409, { reason: 'version_too_old' }, '设备上的 agent 版本过旧，请升级 agent'],
  ])('日志错误 %s 显示专用文案', async (code, status, details, text) => {
    await mount([nas], '/devices?open=d1', (req) => {
      if (req.url.startsWith('/api/devices/d1/logs')) return apiError(status, code, details)
    })
    const dlg = await drawer()
    expect(await within(dlg).findByText(text)).toBeInTheDocument()
  })
})

describe('设备详情抽屉：中枢只读与不存在的设备', () => {
  it('?open=hub 打开中枢抽屉：只读，没有保存与吊销，显示 agent 通道信息', async () => {
    await mount([nas], '/devices?open=hub')
    const dlg = await screen.findByRole('dialog', { name: /中枢/ })
    expect(within(dlg).getAllByText('只读').length).toBeGreaterThan(0)
    expect(within(dlg).queryByRole('button', { name: '保存' })).toBeNull()
    expect(within(dlg).queryByRole('button', { name: /吊销/ })).toBeNull()
    expect(within(dlg).queryByRole('textbox')).toBeNull()
    expect(await within(dlg).findByText('SHA256:abcdef')).toBeInTheDocument()
    expect(within(dlg).getByText('31418')).toBeInTheDocument()
    expect(within(dlg).getByText('linux-amd64 · darwin-arm64')).toBeInTheDocument()
    expect(within(dlg).getByText('本机温度')).toBeInTheDocument()
    expect(within(dlg).queryByText('主机指标')).toBeNull()
  })

  it('?open= 指向不存在的设备：提示并清除参数', async () => {
    await mount([nas], '/devices?open=ghost')
    expect(await screen.findByText('设备不存在或已吊销')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('search')).toHaveTextContent(''))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('设备列表还没加载时不判定不存在', async () => {
    mockApi()
    seedStore([], {})
    await renderWithApp(<><DevicesPage /><SearchProbe /></>, { route: '/devices?open=d1' })
    expect(screen.getByTestId('search')).toHaveTextContent('?open=d1')
    expect(screen.queryByText('设备不存在或已吊销')).toBeNull()
  })

  it('抽屉里的设备被吊销（从列表消失）后抽屉关闭并提示', async () => {
    await mount([nas, vps], '/devices?open=d1')
    await drawer()
    liveStore.applyPatch({ type: 'patch', entity: 'device_removed', id: 'd1', server_time: new Date().toISOString() } as unknown as Parameters<typeof liveStore.applyPatch>[0])
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(screen.getByText('设备不存在或已吊销')).toBeInTheDocument()
  })
})
