import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { createMemoryRouter, RouterProvider, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { makeInstance } from '@/pages/instances/instances.fixtures'
import { apiError, json, mockApi, renderWithApp, seedStore, type ApiHandler } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { Device, DeviceImpact, PairingResult, Proxy } from '@/types/generated'
import { ToastProvider } from '@/ui/toast'
import { DevicesPage } from '../DevicesPage'
import { device } from '../test-utils'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

const nas = device('d1', { name: '飞牛 NAS', role: 'nas', address: '192.168.1.20', hostname: 'fnos', agent_version: 'v1.2.3', os: 'linux', arch: 'amd64', protocol_version: 3 })
const mac = device('d4', { name: 'MacBook', role: 'dev', os: 'darwin', arch: 'arm64', agent_version: 'v1.2.3' })
const vps = device('d3', { name: 'VPS 东京', role: 'vps', direction: 'dial_out', address: 'wss://203.0.113.8:31418', proxy_id: 'p1', agent_version: 'v1.2.3', presence: 'online' })

const proxyList: Proxy[] = [{ id: 'p1', name: 'HK-01', scheme: 'socks5', address: '10.0.0.2:1080', remote_dns: true, location: 'any' } as Proxy]
const impact: DeviceImpact = {
  instances: [{ id: 'i1', name: '主机指标' }, { id: 'i2', name: '硬盘健康' }],
  screens: [{ id: 's1', name: '首页' }],
  rules: [],
}
const emptyImpact: DeviceImpact = { instances: [], screens: [], rules: [] }
const pairing: PairingResult = {
  code: 'ABCD-EFGH-JKMN', expires_at: new Date(Date.now() + 15 * 60_000).toISOString(), urls: ['wss://192.168.1.5:31418'], fingerprint: 'SHA256:hubfp',
  command: 'curl -fsSL http://pi/agent/install.sh | sh -s -- --hub wss://192.168.1.5:31418 --fp SHA256:hubfp --code ABCD-EFGH-JKMN',
  steps: ['s1', 's2', 's3', '$([ "$(uname -s)" = Darwin ] || echo sudo) ./pimon-agent install --hub wss://192.168.1.5:31418 --fp SHA256:hubfp --code ABCD-EFGH-JKMN'],
}

function baseHandler(version = 'v1.2.3'): ApiHandler {
  return (req) => {
    const path = req.url.split('?')[0]
    if (path === '/api/system') return json(200, { version, os: 'linux', arch: 'arm64', uptime_seconds: 3600 })
    if (path === '/api/proxies') return json(200, proxyList)
    if (path === '/api/agent/info') {
      return json(200, { agent_port: 31418, fingerprint: 'SHA256:hubfp', hub_version: version, protocol_version: 3, min_compatible_version: 2, platforms: ['linux-amd64'], release_url_template: '', release_checksums: {} })
    }
  }
}

function SearchProbe() {
  return <div data-testid="search">{useLocation().search}</div>
}

async function mount(devices: Device[], route: string, handler: ApiHandler = () => undefined) {
  const base = baseHandler()
  const api = mockApi((req) => base(req) ?? handler(req))
  seedStore([makeInstance({ id: 'i1', name: '主机指标', runs_on: 'd1' })], { devices })
  await renderWithApp(<><DevicesPage /><SearchProbe /></>, { route })
  return api
}

const drawer = () => screen.findByRole('dialog', { name: /设备详情/ })
const revokeDialog = () => screen.findByRole('alertdialog')
const removePatch = (id: string) =>
  liveStore.applyPatch({ type: 'patch', entity: 'device_removed', id, server_time: new Date().toISOString() } as unknown as Parameters<typeof liveStore.applyPatch>[0])

describe('吊销设备', () => {
  it('抽屉里点吊销：先拉影响列表，列出实例与 screen，输入设备名后按钮才可用，吊销成功后抽屉关闭并提示', async () => {
    const user = userEvent.setup()
    const api = await mount([nas], '/devices?open=d1', (req) => {
      if (req.url === '/api/devices/d1/impact') return json(200, impact)
      if (req.method === 'DELETE' && req.url === '/api/devices/d1') return new Response(null, { status: 204 })
    })
    await user.click(within(await drawer()).getByRole('button', { name: /吊销设备/ }))
    const dlg = await revokeDialog()
    expect(within(dlg).getByRole('heading', { name: '吊销设备「飞牛 NAS」？' })).toBeInTheDocument()
    expect(await within(dlg).findByText('主机指标')).toBeInTheDocument()
    expect(within(dlg).getByText('硬盘健康')).toBeInTheDocument()
    expect(within(dlg).getByText('首页')).toBeInTheDocument()
    expect(within(dlg).getByText('没有告警规则引用')).toBeInTheDocument()
    expect(api.calls.some((c) => c.url === '/api/devices/d1/impact')).toBe(true)

    const submit = within(dlg).getByRole('button', { name: '吊销设备' })
    expect(submit).toBeDisabled()
    const input = within(dlg).getByLabelText('输入设备名确认')
    await user.type(input, '飞牛')
    expect(submit).toBeDisabled()
    await user.type(input, ' NAS')
    expect(submit).toBeEnabled()
    await user.click(submit)
    await waitFor(() => expect(api.calls.some((c) => c.method === 'DELETE' && c.url === '/api/devices/d1')).toBe(true))
    expect(await screen.findByText('已吊销设备「飞牛 NAS」')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(screen.getByTestId('search')).toHaveTextContent('')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('影响列表为空时显示三个「没有」', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices?open=d1', (req) => (req.url === '/api/devices/d1/impact' ? json(200, emptyImpact) : undefined))
    await user.click(within(await drawer()).getByRole('button', { name: /吊销设备/ }))
    const dlg = await revokeDialog()
    expect(await within(dlg).findByText('没有实例')).toBeInTheDocument()
    expect(within(dlg).getByText('没有 screen 引用')).toBeInTheDocument()
    expect(within(dlg).getByText('没有告警规则引用')).toBeInTheDocument()
  })

  it('影响列表读取失败时不能吊销，重试成功后恢复', async () => {
    const user = userEvent.setup()
    let fail = true
    await mount([nas], '/devices?open=d1', (req) => {
      if (req.url === '/api/devices/d1/impact') return fail ? apiError(500, 'internal') : json(200, emptyImpact)
    })
    await user.click(within(await drawer()).getByRole('button', { name: /吊销设备/ }))
    const dlg = await revokeDialog()
    await user.type(within(dlg).getByLabelText('输入设备名确认'), '飞牛 NAS')
    expect(await within(dlg).findByText(/读取影响范围失败/)).toBeInTheDocument()
    expect(within(dlg).getByRole('button', { name: '吊销设备' })).toBeDisabled()
    fail = false
    await user.click(within(dlg).getByRole('button', { name: '重试' }))
    expect(await within(dlg).findByText('没有实例')).toBeInTheDocument()
    expect(within(dlg).getByRole('button', { name: '吊销设备' })).toBeEnabled()
  })

  it('吊销请求失败时留在对话框内显示错误', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices?open=d1', (req) => {
      if (req.url === '/api/devices/d1/impact') return json(200, emptyImpact)
      if (req.method === 'DELETE') return apiError(500, 'internal')
    })
    await user.click(within(await drawer()).getByRole('button', { name: /吊销设备/ }))
    const dlg = await revokeDialog()
    await within(dlg).findByText('没有实例')
    await user.type(within(dlg).getByLabelText('输入设备名确认'), '飞牛 NAS')
    await user.click(within(dlg).getByRole('button', { name: '吊销设备' }))
    expect(await within(dlg).findByRole('alert')).toBeInTheDocument()
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  })

  it('取消不发出吊销请求', async () => {
    const user = userEvent.setup()
    const api = await mount([nas], '/devices?open=d1', (req) => (req.url === '/api/devices/d1/impact' ? json(200, emptyImpact) : undefined))
    await user.click(within(await drawer()).getByRole('button', { name: /吊销设备/ }))
    const dlg = await revokeDialog()
    await user.click(within(dlg).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.calls.some((c) => c.method === 'DELETE')).toBe(false)
    expect(await drawer()).toBeInTheDocument()
  })

  it('行菜单吊销：设备页不开抽屉也能吊销；中枢行没有菜单', async () => {
    const user = userEvent.setup()
    const api = await mount([nas], '/devices', (req) => {
      if (req.url === '/api/devices/d1/impact') return json(200, impact)
      if (req.method === 'DELETE') return new Response(null, { status: 204 })
    })
    const hubRow = document.querySelector('tr[data-device-id="hub"]') as HTMLElement
    expect(within(hubRow).queryByRole('button', { name: '更多操作' })).toBeNull()
    const row = document.querySelector('tr[data-device-id="d1"]') as HTMLElement
    await user.click(within(row).getByRole('button', { name: '更多操作' }))
    await user.click(await screen.findByRole('menuitem', { name: '吊销设备' }))
    const dlg = await revokeDialog()
    await within(dlg).findByText('主机指标')
    await user.type(within(dlg).getByLabelText('输入设备名确认'), '飞牛 NAS')
    await user.click(within(dlg).getByRole('button', { name: '吊销设备' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'DELETE')).toBe(true))
    expect(await screen.findByText('已吊销设备「飞牛 NAS」')).toBeInTheDocument()
  })

  it('抽屉有未保存改动时吊销：设备先从列表消失也不再弹未保存提示，不提示「不存在」', async () => {
    const user = userEvent.setup()
    const base = baseHandler()
    mockApi((req) => {
      if (req.url === '/api/devices/d1/impact') return json(200, emptyImpact)
      if (req.method === 'DELETE') {
        // WS 的 device_removed 先于 DELETE 响应到达
        act(() => removePatch('d1'))
        return new Response(null, { status: 204 })
      }
      return base(req)
    })
    seedStore([], { devices: [nas] })
    const i18n = await createI18n('zh')
    const router = createMemoryRouter([{ path: '/devices', element: <><DevicesPage /><SearchProbe /></> }], { initialEntries: ['/devices?open=d1'] })
    render(
      <I18nextProvider i18n={i18n}>
        <ToastProvider>
          <RouterProvider router={router} />
        </ToastProvider>
      </I18nextProvider>,
    )
    const dlg = await drawer()
    await user.type(within(dlg).getByLabelText('名称'), 'x')
    expect(within(dlg).getByText('未保存改动 1')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: /吊销设备/ }))
    const rev = await revokeDialog()
    await within(rev).findByText('没有实例')
    await user.type(within(rev).getByLabelText('输入设备名确认'), '飞牛 NAS')
    await user.click(within(rev).getByRole('button', { name: '吊销设备' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(await screen.findByText('已吊销设备「飞牛 NAS」')).toBeInTheDocument()
    expect(screen.queryByText('设备不存在或已吊销')).toBeNull()
    expect(screen.queryByRole('dialog', { name: '放弃未保存的改动？' })).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('吊销成功而设备仍在列表里（推送稍后到）时，未保存改动也不拦截关闭', async () => {
    const user = userEvent.setup()
    const base = baseHandler()
    mockApi((req) => {
      if (req.url === '/api/devices/d1/impact') return json(200, emptyImpact)
      if (req.method === 'DELETE') return new Response(null, { status: 204 })
      return base(req)
    })
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
    await user.click(within(dlg).getByRole('button', { name: /吊销设备/ }))
    const rev = await revokeDialog()
    await within(rev).findByText('没有实例')
    await user.type(within(rev).getByLabelText('输入设备名确认'), '飞牛 NAS')
    await user.click(within(rev).getByRole('button', { name: '吊销设备' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(screen.queryByRole('dialog', { name: '放弃未保存的改动？' })).toBeNull()
  })
})

describe('吊销的边角', () => {
  it('吊销请求进行中按 Esc 不关闭对话框', async () => {
    const user = userEvent.setup()
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => { release = r })
    await mount([nas], '/devices?open=d1', async (req) => {
      if (req.url === '/api/devices/d1/impact') return json(200, emptyImpact)
      if (req.method === 'DELETE') {
        await gate
        return new Response(null, { status: 204 })
      }
    })
    await user.click(within(await drawer()).getByRole('button', { name: /吊销设备/ }))
    const dlg = await revokeDialog()
    await within(dlg).findByText('没有实例')
    await user.type(within(dlg).getByLabelText('输入设备名确认'), '飞牛 NAS')
    await user.click(within(dlg).getByRole('button', { name: '吊销设备' }))
    await user.keyboard('{Escape}')
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    release()
    expect(await screen.findByText('已吊销设备「飞牛 NAS」')).toBeInTheDocument()
  })
})

describe('重新配对：占位与 repin', () => {
  it('占位设备在抽屉里不提供生成新配对码', async () => {
    const ph = device('d8', { name: '书房', presence: 'pending', confirmed: false })
    await mount([ph], '/devices?open=d8')
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    expect(within(section).getByText(/不能重新配对/)).toBeInTheDocument()
    expect(within(section).queryByRole('button', { name: '生成新配对码' })).toBeNull()
  })

  it('repin 命令填入中枢真实指纹', async () => {
    await mount([nas], '/devices?open=d1')
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    expect(await within(section).findByText(/repin --fp SHA256:hubfp/)).toBeInTheDocument()
  })
})

describe('重新配对：拨入设备', () => {
  it('说明适用场景与 repin；生成新配对码调用 repair 并显示命令、配对码与指纹', async () => {
    const user = userEvent.setup()
    const api = await mount([nas], '/devices?open=d1', (req) => (req.method === 'POST' && req.url === '/api/devices/d1/repair' ? json(201, pairing) : undefined))
    const dlg = await drawer()
    const section = within(dlg).getByRole('region', { name: '重新配对' })
    expect(within(section).getByText(/换了一台机器接替原设备/)).toBeInTheDocument()
    expect(within(section).getByText(/sudo pimon-agent repin --fp/)).toBeInTheDocument()
    await user.click(within(section).getByRole('button', { name: '生成新配对码' }))
    expect(await within(section).findByText('ABCD-EFGH-JKMN')).toBeInTheDocument()
    expect(api.calls.find((c) => c.url === '/api/devices/d1/repair')?.method).toBe('POST')
    expect(within(section).getByText('SHA256:hubfp')).toBeInTheDocument()
    expect(within(section).getByText(/等待 agent 连接/)).toBeInTheDocument()
    expect(within(section).getByText(/15 分钟内有效，使用一次即作废；过期后点「重新生成」/)).toBeInTheDocument()
    expect(within(section).getByRole('button', { name: '重新生成' })).toBeInTheDocument()
    expect(document.body.textContent ?? '').not.toContain('返回修改')
  })

  it('darwin 设备的 repin 提示不带 sudo', async () => {
    await mount([mac], '/devices?open=d4')
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    expect(within(section).getByText(/pimon-agent repin --fp/).textContent).not.toContain('sudo')
  })

  it('已出现的占位设备不兼容时显示其状态而不是一直等待连接', async () => {
    const user = userEvent.setup()
    const placeholder = device('d9', { name: '飞牛 NAS', replaces: 'd1', presence: 'incompatible', confirmed: false, incompatible: { reason: 'version_too_old', required_min: 3 } })
    await mount([nas, placeholder], '/devices?open=d1', (req) => (req.method === 'POST' && req.url === '/api/devices/d1/repair' ? json(201, pairing) : undefined))
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    await user.click(within(section).getByRole('button', { name: '生成新配对码' }))
    expect(await within(section).findByText(/已找到本次配对的设备/)).toBeInTheDocument()
    expect(within(section).queryByText(/等待 agent 连接/)).toBeNull()
  })

  it('生成失败显示错误；列表菜单的重新配对打开抽屉并带 repair 参数', async () => {
    const user = userEvent.setup()
    await mount([nas], '/devices')
    const row = document.querySelector('tr[data-device-id="d1"]') as HTMLElement
    await user.click(within(row).getByRole('button', { name: '更多操作' }))
    await user.click(await screen.findByRole('menuitem', { name: '重新配对' }))
    expect(await drawer()).toBeInTheDocument()
    expect(screen.getByTestId('search')).toHaveTextContent('?open=d1&repair=1')
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    await user.click(within(section).getByRole('button', { name: '生成新配对码' }))
    expect(await within(section).findByRole('alert')).toBeInTheDocument()
  })
})

describe('重新配对：拨出设备', () => {
  async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>, section: HTMLElement) {
    await user.type(within(section).getByLabelText(/SPKI 指纹/), 'SHA256:newfp')
    await user.type(within(section).getByLabelText(/配对码/), 'AAAA-BBBB')
    await user.click(within(section).getByRole('button', { name: '重新配对' }))
  }

  it('表单预填旧地址，提交到 repair，不带名称与角色；成功后提示并转到新设备', async () => {
    const user = userEvent.setup()
    const next = device('d9', { name: 'VPS 东京', role: 'vps', direction: 'dial_out', address: 'wss://203.0.113.8:31418', proxy_id: 'p1' })
    const api = await mount([vps, next], '/devices?open=d3', (req) => (req.method === 'POST' && req.url === '/api/devices/d3/repair' ? json(201, next) : undefined))
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    expect(within(section).getByText(/直接在「属性」里改地址/)).toBeInTheDocument()
    expect(within(section).getByLabelText(/地址/)).toHaveValue('wss://203.0.113.8:31418')
    expect(within(section).queryByLabelText('角色')).toBeNull()
    await fillAndSubmit(user, section)
    await waitFor(() => expect(api.calls.some((c) => c.url === '/api/devices/d3/repair')).toBe(true))
    const body = api.calls.find((c) => c.url === '/api/devices/d3/repair')?.body as Record<string, unknown>
    expect(body).toEqual({ address: 'wss://203.0.113.8:31418', fingerprint: 'SHA256:newfp', code: 'AAAA-BBBB', proxy_id: 'p1' })
    expect('name' in body).toBe(false)
    expect('role' in body).toBe(false)
    expect(await screen.findByText('重新配对完成，设备「VPS 东京」已接替旧设备')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('search')).toHaveTextContent('?open=d9'))
  })

  it('改回直连时提交 direct；新设备还没推送到时不提示「不存在」', async () => {
    const user = userEvent.setup()
    const next = device('d9', { name: 'VPS 东京', role: 'vps', direction: 'dial_out', address: 'wss://203.0.113.8:31418' })
    const api = await mount([vps], '/devices?open=d3', (req) => (req.method === 'POST' && req.url === '/api/devices/d3/repair' ? json(201, next) : undefined))
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    await user.selectOptions(within(section).getByLabelText('代理'), '')
    await fillAndSubmit(user, section)
    await waitFor(() => expect(api.calls.some((c) => c.url === '/api/devices/d3/repair')).toBe(true))
    expect((api.calls.find((c) => c.url === '/api/devices/d3/repair')?.body as Record<string, unknown>).proxy_id).toBe('direct')
    await screen.findByText(/重新配对完成/)
    expect(screen.queryByText('设备不存在或已吊销')).toBeNull()
    expect(screen.queryByText('重新配对完成，旧设备已被新设备接替')).toBeNull()
    // 之后设备推送到达，抽屉显示它
    await act(async () => {
      liveStore.applyPatch({ type: 'patch', entity: 'device', device: next, server_time: new Date().toISOString() } as unknown as Parameters<typeof liveStore.applyPatch>[0])
    })
    const nd = await screen.findByRole('dialog', { name: /设备详情：VPS 东京/ })
    expect(within(within(nd).getByRole('region', { name: '属性' })).getByLabelText('代理')).toHaveValue('')
    expect(within(nd).queryByText(/direct/)).toBeNull()
  })

  it('配对失败按原因给出指引，指引里的端口来自 agentListenPort', async () => {
    const user = userEvent.setup()
    await mount([vps], '/devices?open=d3', (req) => (req.url === '/api/devices/d3/repair' ? apiError(502, 'pairing.failed', { reason: 'unreachable' }) : undefined))
    const section = within(await drawer()).getByRole('region', { name: '重新配对' })
    await fillAndSubmit(user, section)
    expect(await within(section).findByText(/放行 TCP 31418/)).toBeInTheDocument()
  })
})

describe('拨出设备抽屉的属性保存', () => {
  it('只改名称时不提交地址与代理（代理已被删除也不会被拒）', async () => {
    const user = userEvent.setup()
    const api = await mount([vps], '/devices?open=d3', (req) => (req.method === 'PUT' ? json(200, { ...vps, name: '新名', updated_at: '2026-10-05T00:00:00Z' }) : undefined))
    const dlg = await drawer()
    await user.clear(within(dlg).getByLabelText('名称'))
    await user.type(within(dlg).getByLabelText('名称'), '新名')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(api.calls.find((c) => c.method === 'PUT')?.body).toEqual({ name: '新名', role: 'vps', allow_offline: false, maintenance: false })
  })

  it('地址输入框不自拼 wss://：带 scheme 的地址原样显示，粘贴的地址原样提交', async () => {
    const user = userEvent.setup()
    const api = await mount([vps], '/devices?open=d3', (req) => (req.method === 'PUT' ? json(200, vps) : undefined))
    const dlg = await drawer()
    const addr = within(dlg).getByLabelText('地址')
    expect(addr).toHaveValue('wss://203.0.113.8:31418')
    expect(within(dlg).queryByText('wss://', { selector: 'span' })).toBeNull()
    await user.clear(addr)
    await user.type(addr, 'wss://198.51.100.9:41418')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'PUT')).toBe(true))
    expect((api.calls.find((c) => c.method === 'PUT')?.body as Record<string, unknown>).address).toBe('wss://198.51.100.9:41418')
  })
})
