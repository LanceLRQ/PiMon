import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, json, mockApi, renderWithApp, seedStore, type Req } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { AgentInfo, Device, PairingResult, Proxy } from '@/types/generated'
import { device } from '../test-utils'
import { AddDeviceDialog } from './AddDeviceDialog'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

const info: AgentInfo = {
  agent_port: 31418,
  fingerprint: 'SHA256:hubfp',
  hub_version: 'v1.2.3',
  protocol_version: 1,
  min_compatible_version: 1,
  platforms: ['linux-amd64', 'linux-arm64'],
  release_url_template: 'https://github.com/LanceLRQ/PiMon/releases/download/v1.2.3/pimon-agent-<os>-<arch>',
  release_checksums: { 'linux-amd64': 'deadbeef' },
}

function pairing(expiresInMs = 15 * 60_000): PairingResult {
  const hub = 'wss://192.168.1.10:31418'
  const fp = 'SHA256:hubfp'
  const args = `--hub ${hub} --fp ${fp} --code 4F7K-9QXM`
  return {
    code: '4F7K-9QXM',
    expires_at: new Date(Date.now() + expiresInMs).toISOString(),
    urls: [hub, 'wss://pimon.home.lan:31418'],
    fingerprint: fp,
    command: `curl -fsSL http://192.168.1.10:31415/agent/install.sh | sh -s -- ${args}`,
    steps: [
      'PLATFORM=$(uname -s)',
      'curl -fsSLo pimon-agent http://192.168.1.10:31415/agent/download/$PLATFORM',
      'echo "$(curl -fsSL http://192.168.1.10:31415/agent/checksums)" | (command -v sha256sum >/dev/null && sha256sum -c - || shasum -a 256 -c -)',
      `$([ "$(uname -s)" = Darwin ] || echo sudo) ./pimon-agent install ${args}`,
    ],
  }
}

const proxies: Proxy[] = [
  { id: 'p1', name: 'HK-01', scheme: 'socks5h', address: '10.0.0.2:1080', remote_dns: true, location: 'any', auth: { set: false } } as unknown as Proxy,
]

interface Opts {
  pairingResult?: PairingResult
  agentInfo?: AgentInfo
  extra?: (req: Req) => Response | undefined | Promise<Response | undefined>
  devices?: Device[]
}

async function mount(opts: Opts = {}) {
  const onViewDetail = vi.fn()
  const api = mockApi((req) => {
    const r = opts.extra?.(req)
    if (r) return r
    if (req.method === 'GET' && req.url === '/api/agent/info') return json(200, opts.agentInfo ?? info)
    if (req.method === 'GET' && req.url === '/api/proxies') return json(200, proxies)
    if (req.method === 'POST' && req.url === '/api/devices/pairing') return json(201, opts.pairingResult ?? pairing())
    return undefined
  })
  seedStore([], { devices: opts.devices ?? [] })
  await renderWithApp(<AddDeviceDialog open onOpenChange={() => {}} onViewDetail={onViewDetail} />)
  return { api, onViewDetail }
}

const panel = () => within(screen.getByRole('tabpanel'))
const generateBtn = () => screen.getByRole('button', { name: '生成配对码与命令' })

describe('拨入', () => {
  it('名称超过 64 个字符时不发请求并提示', async () => {
    const { api } = await mount()
    await userEvent.type(panel().getByLabelText(/^名称/), '飞'.repeat(65))
    await userEvent.click(generateBtn())
    expect(await screen.findByText('名称最多 64 个字符')).toBeInTheDocument()
    expect(api.calls.some((c) => c.url === '/api/devices/pairing')).toBe(false)
  })

  it('开发机默认开启允许离线，请求体带名称、角色与开关', async () => {
    const { api } = await mount()
    const sw = screen.getByRole('switch', { name: '允许离线' })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    await userEvent.selectOptions(panel().getByLabelText(/^角色/), 'dev')
    expect(sw).toHaveAttribute('aria-checked', 'true')
    await userEvent.type(panel().getByLabelText(/^名称/), ' MacBook ')
    await userEvent.click(generateBtn())
    await screen.findByText('4F7K-9QXM')
    const post = api.calls.find((c) => c.url === '/api/devices/pairing')
    expect(post?.body).toEqual({ name: 'MacBook', role: 'dev', allow_offline: true, auto_host_metrics: true })
  })

  it('生成后命令包含地址、指纹与配对码；倒计时与等待状态显示', async () => {
    await mount()
    await userEvent.click(generateBtn())
    const one = await screen.findByText(/curl -fsSL http:\/\/192\.168\.1\.10:31415\/agent\/install\.sh/)
    expect(one.textContent).toContain('--hub wss://192.168.1.10:31418')
    expect(one.textContent).toContain('--fp SHA256:hubfp')
    expect(one.textContent).toContain('--code 4F7K-9QXM')
    expect(screen.getByText('4F7K-9QXM')).toBeInTheDocument()
    expect(screen.getAllByText('SHA256:hubfp').length).toBeGreaterThan(0)
    expect(screen.getByText('等待 agent 连接…')).toBeInTheDocument()
    expect(screen.getByText(/配对码剩余 1[45]:\d\d/)).toBeInTheDocument()
  })

  it('切换 hub 地址后命令里只出现所选地址', async () => {
    await mount()
    await userEvent.click(generateBtn())
    await screen.findByText('4F7K-9QXM')
    await userEvent.selectOptions(panel().getByLabelText('hub 地址'), 'wss://pimon.home.lan:31418')
    const one = screen.getByText(/agent\/install\.sh/)
    expect(one.textContent).toContain('--hub wss://pimon.home.lan:31418')
    expect(one.textContent).not.toContain('--hub wss://192.168.1.10:31418')
  })

  it('分步命令含 shasum -a 256；非开发机有 sudo，开发机没有', async () => {
    await mount()
    await userEvent.click(generateBtn())
    await userEvent.click(await screen.findByRole('radio', { name: '分步命令' }))
    expect(screen.getByText(/shasum -a 256 -c -/)).toBeInTheDocument()
    expect(screen.getByText(/echo sudo\) \.\/pimon-agent install/)).toBeInTheDocument()
  })

  it('开发机分步命令不带 sudo', async () => {
    await mount()
    await userEvent.selectOptions(panel().getByLabelText(/^角色/), 'dev')
    await userEvent.click(generateBtn())
    await userEvent.click(await screen.findByRole('radio', { name: '分步命令' }))
    expect(screen.queryByText(/echo sudo/)).toBeNull()
    expect(screen.getByText(/^\.\/pimon-agent install --hub/)).toBeInTheDocument()
    expect(screen.queryByText(/--sudoers。/)).toBeNull()
  })

  it('角色为 NAS 或服务器时提示末尾加 --sudoers', async () => {
    await mount()
    await userEvent.selectOptions(panel().getByLabelText(/^角色/), 'nas')
    await userEvent.click(generateBtn())
    expect(await screen.findByText('需要硬盘 SMART 等存储健康数据时，在命令末尾加 --sudoers。')).toBeInTheDocument()
  })

  it('中枢没有内嵌 agent 时提示，命令照常显示', async () => {
    await mount({ agentInfo: { ...info, platforms: [], release_url_template: '' } })
    await userEvent.click(generateBtn())
    expect(await screen.findByText(/当前中枢没有内嵌 agent/)).toBeInTheDocument()
    expect(screen.getByText(/agent\/install\.sh/)).toBeInTheDocument()
  })

  it('倒计时到 0 提示重新生成，返回修改后可再生成', async () => {
    const { api } = await mount({ pairingResult: pairing(-1000) })
    await userEvent.click(generateBtn())
    expect(await screen.findByText('配对码已过期')).toBeInTheDocument()
    expect(screen.queryByText('等待 agent 连接…')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: '返回修改' }))
    await userEvent.click(generateBtn())
    await screen.findByText('配对码已过期')
    expect(api.calls.filter((c) => c.url === '/api/devices/pairing')).toHaveLength(2)
  })

  it('设备上线事件到达后切换为已连接，可查看详情', async () => {
    const { onViewDetail } = await mount({ devices: [device('old')] })
    await userEvent.click(generateBtn())
    await screen.findByText('等待 agent 连接…')
    // 已有设备变化不算本次配对
    act(() => liveStore.applyPatch({ type: 'patch', entity: 'device', server_time: new Date().toISOString(), device: device('old', { presence: 'offline' }) } as never))
    expect(screen.queryByText('已连接')).toBeNull()
    const fresh = device('new1', { name: '飞牛 NAS', address: '192.168.1.24', created_at: new Date().toISOString(), presence: 'online' })
    act(() => liveStore.applyPatch({ type: 'patch', entity: 'device', server_time: new Date().toISOString(), device: fresh } as never))
    expect(await screen.findByText('已连接')).toBeInTheDocument()
    expect(screen.getByText(/飞牛 NAS 已接入 · 192\.168\.1\.24/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '查看设备详情' }))
    expect(onViewDetail).toHaveBeenCalledWith('new1')
  })

  it('agent 通道未启用时显示错误文案', async () => {
    await mount({ extra: (r) => (r.url === '/api/devices/pairing' ? apiError(409, 'agent.disabled') : undefined) })
    await userEvent.click(generateBtn())
    expect(await screen.findByRole('alert')).toHaveTextContent(/agent/)
  })

  it('复制在对话框内可用：回退路径复制的是命令原文', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: undefined })
    const exec = vi.fn(() => true)
    ;(document as unknown as { execCommand: unknown }).execCommand = exec
    await mount()
    await userEvent.click(generateBtn())
    const block = (await screen.findByText(/agent\/install\.sh/)).closest('div')!
    let copied = ''
    exec.mockImplementation(() => {
      copied = document.querySelector('textarea')?.value ?? ''
      return true
    })
    await userEvent.click(within(block).getByRole('button', { name: '复制命令' }))
    expect(exec).toHaveBeenCalledWith('copy')
    expect(copied).toContain('--code 4F7K-9QXM')
    expect(await screen.findByText('已复制')).toBeInTheDocument()
  })
})

async function openOut() {
  await userEvent.click(screen.getByRole('tab', { name: /拨出/ }))
}
const fillOut = async (over: { address?: string; fp?: string; code?: string } = {}) => {
  await userEvent.type(panel().getByLabelText(/^地址/), over.address ?? '203.0.113.8:31418')
  await userEvent.type(panel().getByLabelText(/^SPKI 指纹/), over.fp ?? 'SHA256:abc')
  await userEvent.type(panel().getByLabelText(/^配对码/), over.code ?? '5ZHM-1T8Q')
}
const submitOut = () => userEvent.click(screen.getByRole('button', { name: '配对并添加' }))

describe('拨出', () => {
  it('必填校验：地址、指纹、配对码为空时不发请求', async () => {
    const { api } = await mount()
    await openOut()
    await submitOut()
    expect(await screen.findByText('请填写地址')).toBeInTheDocument()
    expect(screen.getByText('请填写指纹')).toBeInTheDocument()
    expect(screen.getByText('请填写配对码')).toBeInTheDocument()
    expect(api.calls.some((c) => c.url === '/api/devices/dial-out')).toBe(false)
  })

  it('说明与 Releases 下载命令（带校验和）、安全组提示；代理下拉含直连与全局代理', async () => {
    await mount()
    await openOut()
    expect((await screen.findByText(/releases\/download\/v1\.2\.3\/pimon-agent-\$PLATFORM/)).textContent).toContain('deadbeef  pimon-agent-$PLATFORM')
    expect(screen.getByText(/install --listen :31418/)).toBeInTheDocument()
    expect(screen.getByText(/放行 TCP 31418|Open TCP 31418|安全组 \/ 防火墙放行 TCP 31418/)).toBeInTheDocument()
    expect(panel().getByLabelText('地址*', { exact: false })).toHaveAttribute('placeholder', '203.0.113.5:31418')
    const proxy = panel().getByLabelText('代理')
    expect(await within(proxy).findByRole('option', { name: /HK-01 · socks5h 10\.0\.0\.2:1080/ })).toBeInTheDocument()
    expect(within(proxy).getByRole('option', { name: '直连' })).toBeInTheDocument()
  })

  it('开发构建（没有 Release）隐藏下载命令并提示', async () => {
    await mount({ agentInfo: { ...info, release_url_template: '', release_checksums: {} } })
    await openOut()
    expect(await screen.findByText(/这是开发构建，没有对应的 GitHub Release/)).toBeInTheDocument()
    expect(screen.queryByText(/releases\/download/)).toBeNull()
    expect(screen.getByText(/install --listen :31418/)).toBeInTheDocument()
  })

  it('提交裸 host:port 原样发给后端，成功后显示配对成功与查看详情', async () => {
    const created = device('v1', { name: 'VPS 东京', direction: 'dial_out' })
    const { api, onViewDetail } = await mount({ extra: (r) => (r.url === '/api/devices/dial-out' ? json(201, created) : undefined) })
    await openOut()
    await fillOut()
    await userEvent.selectOptions(panel().getByLabelText('代理'), 'p1')
    await userEvent.type(panel().getByLabelText(/^名称/), 'VPS 东京')
    await submitOut()
    expect(await screen.findByText('配对成功')).toBeInTheDocument()
    const post = api.calls.find((c) => c.url === '/api/devices/dial-out')
    expect(post?.body).toEqual({
      address: '203.0.113.8:31418', fingerprint: 'SHA256:abc', code: '5ZHM-1T8Q', proxy_id: 'p1', name: 'VPS 东京', role: 'vps', auto_host_metrics: true,
    })
    await userEvent.click(screen.getByRole('button', { name: '查看设备详情' }))
    expect(onViewDetail).toHaveBeenCalledWith('v1')
  })

  it('后端字段错误：地址格式文案说明可接受的格式', async () => {
    await mount({ extra: (r) => (r.url === '/api/devices/dial-out' ? apiError(400, 'validation.failed', { fields: { address: 'invalid' } }) : undefined) })
    await openOut()
    await fillOut({ address: '::1' })
    await submitOut()
    expect(await screen.findByText(/host:port 或 wss:\/\/host:port（IPv6 写成 \[地址\]:端口）/)).toBeInTheDocument()
  })

  it('配对进行中显示进度并禁用提交', async () => {
    let release: (r: Response) => void = () => {}
    await mount({ extra: (r) => (r.url === '/api/devices/dial-out' ? new Promise<Response>((res) => { release = res }) : undefined) })
    await openOut()
    await fillOut()
    await submitOut()
    expect(await screen.findByText('正在配对，最长等待 20 秒')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '配对中…' })).toBeDisabled()
    await act(async () => release(json(201, device('v2', { name: 'x' }))))
    expect(await screen.findByText('配对成功')).toBeInTheDocument()
  })

  it.each([
    ['unreachable', '连不上 agent', /云厂商安全组与系统防火墙放行 TCP 31418/],
    ['pin_mismatch', '证书指纹不匹配', /逐字核对 pimon-agent pair 输出的「SPKI 指纹」/],
    ['code_expired', '配对码已过期', /重新执行 sudo pimon-agent pair/],
    ['bad_code', '配对码错误或已被使用', /配对码只能用一次/],
    ['rate_limited', '尝试次数过多', /暂时被锁定/],
    ['timeout', '配对超时', /检查中枢到 VPS 的网络/],
    ['unknown', '配对失败，原因未知', /查看中枢日志/],
  ])('失败原因 %s 给出对应指引', async (reason, why, tip) => {
    await mount({ extra: (r) => (r.url === '/api/devices/dial-out' ? apiError(502, 'pairing.failed', { reason }) : undefined) })
    await openOut()
    await fillOut()
    await submitOut()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('配对失败')
    expect(alert).toHaveTextContent(reason)
    expect(alert).toHaveTextContent(why)
    expect(alert).toHaveTextContent(tip)
    // 失败后表单保留，可以直接修改重试
    expect(panel().getByLabelText(/^地址/)).toHaveValue('203.0.113.8:31418')
  })

  it('不兼容但设备已保留时提供查看设备', async () => {
    const { onViewDetail } = await mount({
      extra: (r) => (r.url === '/api/devices/dial-out' ? apiError(502, 'pairing.failed', { reason: 'incompatible', device_id: 'kept1' }) : undefined),
    })
    await openOut()
    await fillOut()
    await submitOut()
    await userEvent.click(await screen.findByRole('button', { name: '查看已保留的设备' }))
    expect(onViewDetail).toHaveBeenCalledWith('kept1')
  })
})
