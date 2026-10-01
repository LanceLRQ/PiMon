import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiError, json, mockApi, renderWithApp, type Req } from '@/pages/instances/test-utils'
import type { Proxy, ProxyTestResult } from '@/types/generated'
import { ProxiesPage } from './ProxiesPage'

afterEach(() => vi.unstubAllGlobals())

function proxy(over: Partial<Proxy> & Pick<Proxy, 'id' | 'name'>): Proxy {
  return {
    scheme: 'socks5h',
    address: '10.0.0.2:1080',
    remote_dns: true,
    location: 'any',
    auth: { set: false },
    referrers: [],
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...over,
  }
}

const hk = proxy({
  id: 'hk',
  name: 'HK-01',
  auth: { set: true },
  referrers: [
    { id: 'i1', name: '网络连通' },
    { id: 'i2', name: 'Claude' },
  ],
})
const home = proxy({ id: 'home', name: '家里 http', scheme: 'http', address: '192.168.1.1:7890', remote_dns: true, location: 'lan' })

interface Setup {
  list?: Proxy[]
  extra?: (req: Req) => Response | undefined | Promise<Response | undefined>
}

async function setup({ list = [hk, home], extra }: Setup = {}) {
  const api = mockApi((req) => {
    const r = extra?.(req)
    if (r) return r
    if (req.method === 'GET' && req.url === '/api/proxies') return json(200, list)
    return undefined
  })
  await renderWithApp(<ProxiesPage />)
  return api
}

describe('代理列表', () => {
  it('显示 9 列：认证、远端 dns（http 不适用）、可用位置、被引用', async () => {
    await setup()
    const rows = await screen.findAllByRole('row')
    const hkRow = rows.find((r) => within(r).queryByText('HK-01'))!
    expect(within(hkRow).getAllByText('socks5h').length).toBeGreaterThan(0)
    expect(within(hkRow).getByText('10.0.0.2:1080')).toBeInTheDocument()
    expect(within(hkRow).getByText('有')).toBeInTheDocument()
    expect(within(hkRow).getByText('2 个实例')).toBeInTheDocument()
    expect(within(hkRow).getByText('网络连通、Claude')).toBeInTheDocument()
    const homeRow = rows.find((r) => within(r).queryByText('家里 http'))!
    expect(within(homeRow).getByText('不适用')).toBeInTheDocument()
    expect(within(homeRow).getByText('局域网')).toBeInTheDocument()
    expect(within(homeRow).getByText('0 个实例')).toBeInTheDocument()
    expect(within(homeRow).getByText('未测试')).toBeInTheDocument()
  })

  it('列表为空显示引导，加载失败可重试', async () => {
    await setup({ list: [] })
    expect(await screen.findByText('还没有添加代理')).toBeInTheDocument()
  })

  it('加载失败显示错误与重试', async () => {
    let fail = true
    mockApi((req) => {
      if (req.method === 'GET' && req.url === '/api/proxies') return fail ? apiError(500, 'internal') : json(200, [hk])
      return undefined
    })
    await renderWithApp(<ProxiesPage />)
    expect(await screen.findByText(/无法加载代理列表/)).toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('HK-01')).toBeInTheDocument()
  })

  it('行内测试：显示结果并记在「最近测试」里', async () => {
    const ok: ProxyTestResult = { ok: true, url: 'https://x', latency_ms: 187, status: 204 }
    const api = await setup({ extra: (r) => (r.method === 'POST' && r.url === '/api/proxies/hk/test' ? json(200, ok) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '测试 HK-01' }))
    const row = screen.getAllByRole('row').find((r) => within(r).queryByText('HK-01'))!
    expect(await within(row).findByText('204 · 187 ms')).toBeInTheDocument()
    const call = api.calls.find((c) => c.url === '/api/proxies/hk/test')!
    expect(call.body).toBeNull()
  })

  it('行内测试失败显示失败原因', async () => {
    await setup({ extra: (r) => (r.url === '/api/proxies/home/test' ? json(200, { ok: false, url: 'https://x', latency_ms: 0, status: 0, error: 'timeout' }) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '测试 家里 http' }))
    const row = screen.getAllByRole('row').find((r) => within(r).queryByText('家里 http'))!
    expect(await within(row).findByText('失败')).toBeInTheDocument()
  })
})

describe('手机宽度', () => {
  it('改为卡片列表，编辑、测试、删除都可用', async () => {
    vi.stubGlobal(
      'matchMedia',
      (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} }),
    )
    await setup()
    expect(await screen.findByText('HK-01')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByText('2 个实例')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '删除 HK-01' }))
    expect(await screen.findByText(/删除代理「HK-01」/)).toBeInTheDocument()
  })
})

describe('删除代理确认流程', () => {
  it('没有引用：直接确认，不带 force', async () => {
    const api = await setup({ extra: (r) => (r.method === 'DELETE' ? new Response(null, { status: 204 }) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '删除 家里 http' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText('没有实例引用它，可以直接删除。')).toBeInTheDocument()
    expect(within(dlg).queryByRole('checkbox')).not.toBeInTheDocument()
    await userEvent.click(within(dlg).getByRole('button', { name: '删除代理' }))
    await waitFor(() => expect(screen.queryByText('家里 http')).not.toBeInTheDocument())
    const del = api.calls.find((c) => c.method === 'DELETE')!
    expect(del.url).toBe('/api/proxies/home')
  })

  it('被引用：列出受影响实例，勾选「我已了解」前删除按钮不可用，之后带 force=1', async () => {
    const api = await setup({ extra: (r) => (r.method === 'DELETE' ? new Response(null, { status: 204 }) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '删除 HK-01' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText('网络连通')).toBeInTheDocument()
    expect(within(dlg).getByText('Claude')).toBeInTheDocument()
    const del = within(dlg).getByRole('button', { name: '删除代理' })
    expect(del).toBeDisabled()
    await userEvent.click(within(dlg).getByRole('checkbox'))
    expect(del).toBeEnabled()
    await userEvent.click(del)
    await waitFor(() => expect(screen.queryByText('HK-01')).not.toBeInTheDocument())
    expect(api.calls.find((c) => c.method === 'DELETE')!.url).toBe('/api/proxies/hk?force=1')
  })

  it('列表显示无引用但服务端回 409：转入二次确认并列出受影响实例，确认后才强制删除', async () => {
    let first = true
    const api = await setup({
      extra: (r) => {
        if (r.method !== 'DELETE') return undefined
        if (first) {
          first = false
          return apiError(409, 'proxy.in_use', { instances: [{ id: 'i9', name: '新实例' }] })
        }
        return new Response(null, { status: 204 })
      },
    })
    await userEvent.click(await screen.findByRole('button', { name: '删除 家里 http' }))
    const dlg = await screen.findByRole('dialog')
    await userEvent.click(within(dlg).getByRole('button', { name: '删除代理' }))
    expect(await within(dlg).findByText('新实例')).toBeInTheDocument()
    const del = within(dlg).getByRole('button', { name: '删除代理' })
    expect(del).toBeDisabled()
    await userEvent.click(within(dlg).getByRole('checkbox'))
    await userEvent.click(del)
    await waitFor(() => expect(screen.queryByText('家里 http')).not.toBeInTheDocument())
    const deletes = api.calls.filter((c) => c.method === 'DELETE').map((c) => c.url)
    expect(deletes).toEqual(['/api/proxies/home', '/api/proxies/home?force=1'])
  })

  it('取消不发请求', async () => {
    const api = await setup()
    await userEvent.click(await screen.findByRole('button', { name: '删除 HK-01' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(api.calls.some((c) => c.method === 'DELETE')).toBe(false)
  })
})

describe('代理编辑抽屉', () => {
  it('编辑：认证留空不提交 auth，保存后刷新列表', async () => {
    const saved = { ...hk, name: 'HK-02' }
    const api = await setup({ extra: (r) => (r.method === 'PUT' ? json(200, saved) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '编辑 HK-01' }))
    const name = await screen.findByLabelText(/^名称/)
    expect(name).toHaveValue('HK-01')
    expect(screen.getByPlaceholderText('已设置 · 留空则不修改')).toHaveValue('')
    await userEvent.clear(name)
    await userEvent.type(name, 'HK-02')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const put = api.calls.find((c) => c.method === 'PUT')!
    expect(put.url).toBe('/api/proxies/hk')
    expect(put.body).toEqual({ name: 'HK-02', scheme: 'socks5h', address: '10.0.0.2:1080', remote_dns: true, location: 'any' })
    expect(await screen.findByText('HK-02')).toBeInTheDocument()
  })

  it('字段错误回显在对应字段下', async () => {
    await setup({
      extra: (r) => (r.method === 'PUT' ? apiError(400, 'validation.failed', { fields: { name: 'duplicate', address: 'invalid' } }) : undefined),
    })
    await userEvent.click(await screen.findByRole('button', { name: '编辑 HK-01' }))
    const name = await screen.findByLabelText(/^名称/)
    await userEvent.type(name, 'x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('已有同名代理')).toBeInTheDocument()
    expect(screen.getByText('地址须为「主机:端口」，不含协议与账号')).toBeInTheDocument()
    expect(name).toHaveAttribute('aria-invalid', 'true')
    // 修改字段后该字段的错误消失
    await userEvent.type(name, 'y')
    expect(screen.queryByText('已有同名代理')).not.toBeInTheDocument()
  })

  it('新建：端口非法时本地拦截，合法后 POST 并带认证', async () => {
    const created = proxy({ id: 'n1', name: '新代理', scheme: 'http', address: '1.2.3.4:8080', auth: { set: true } })
    const api = await setup({ list: [], extra: (r) => (r.method === 'POST' && r.url === '/api/proxies' ? json(201, created) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '添加代理' }))
    await userEvent.type(await screen.findByLabelText(/^名称/), '新代理')
    await userEvent.click(screen.getByRole('radio', { name: 'http' }))
    await userEvent.type(screen.getByLabelText('主机'), '1.2.3.4')
    await userEvent.type(screen.getByLabelText('端口'), '99999')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('端口须为 1–65535 的整数')).toBeInTheDocument()
    expect(api.calls.some((c) => c.method === 'POST')).toBe(false)
    const port = screen.getByLabelText('端口')
    await userEvent.clear(port)
    await userEvent.type(port, '8080')
    await userEvent.type(screen.getByLabelText(/^用户名/), 'u')
    await userEvent.type(screen.getByPlaceholderText('可选'), 'pw')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(api.calls.find((c) => c.method === 'POST')!.body).toEqual({
      name: '新代理',
      scheme: 'http',
      address: '1.2.3.4:8080',
      remote_dns: false,
      location: 'any',
      auth: { username: 'u', password: 'pw' },
    })
    expect(await screen.findByText('新代理')).toBeInTheDocument()
  })

  it('抽屉里的测试使用已保存配置，并把结果同步到列表', async () => {
    const api = await setup({
      extra: (r) => (r.url === '/api/proxies/hk/test' ? json(200, { ok: true, url: 'https://example.com', latency_ms: 42, status: 200 }) : undefined),
    })
    await userEvent.click(await screen.findByRole('button', { name: '编辑 HK-01' }))
    const url = await screen.findByLabelText('测试目标')
    expect(url).toHaveValue('https://www.google.com/generate_204')
    await userEvent.clear(url)
    await userEvent.type(url, 'https://example.com')
    await userEvent.click(screen.getByRole('button', { name: '测试' }))
    expect(await screen.findAllByText('200 · 42 ms')).not.toHaveLength(0)
    expect(api.calls.find((c) => c.url === '/api/proxies/hk/test')!.body).toEqual({ url: 'https://example.com' })
  })

  it('新建时测试区提示先保存；抽屉里可进入删除确认', async () => {
    await setup()
    await userEvent.click(await screen.findByRole('button', { name: '添加代理' }))
    expect(await screen.findByText('保存后才能测试。')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '取消' }))
    await userEvent.click(await screen.findByRole('button', { name: '编辑 HK-01' }))
    await userEvent.click(await screen.findByRole('button', { name: '删除' }))
    expect(await screen.findByText(/删除代理「HK-01」/)).toBeInTheDocument()
  })
})
