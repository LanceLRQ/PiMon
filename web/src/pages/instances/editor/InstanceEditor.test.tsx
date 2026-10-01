import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { liveStore } from '@/store/live-store'
import { InstanceEditPage } from '../InstanceEditPage'
import { InstanceNewPage } from '../InstanceNewPage'
import { apiError, json, mockApi, renderWithApp, type ApiHandler, type Req } from '../test-utils'
import { editorPlugins, kitchenPlugin, makeDetail, proxyFixtures } from './editor.fixtures'

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const base: ApiHandler = (req) => {
  const path = req.url.split('?')[0]
  if (req.method === 'GET' && path === '/api/plugins') return json(200, editorPlugins)
  if (req.method === 'GET' && path === '/api/proxies') return json(200, proxyFixtures)
  return undefined
}

function handler(extra: ApiHandler = () => undefined): ApiHandler {
  return async (req) => (await extra(req)) ?? base(req)
}

async function mountNew(extra?: ApiHandler, route = '/instances/new') {
  const api = mockApi(handler(extra))
  await renderWithApp(
    <Routes>
      <Route path="/instances/new" element={<InstanceNewPage />} />
      <Route path="/instances/:id/edit" element={<InstanceEditPage />} />
      <Route path="/instances" element={<div>列表页</div>} />
    </Routes>,
    { route },
  )
  return api
}

async function mountEdit(detail = makeDetail(), extra?: ApiHandler) {
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

const lastBody = (calls: Req[], method: string, url: string) => [...calls].reverse().find((c) => c.method === method && c.url.startsWith(url))?.body as Record<string, unknown>

async function pickKitchen(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
  await screen.findByLabelText(/^地址/)
}

describe('新建实例：插件目录', () => {
  it('按类别分组、只列数据源插件，可搜索，选中后出现该插件的表单', async () => {
    const user = userEvent.setup()
    await mountNew()
    expect(await screen.findByRole('button', { name: /简单检测/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /通知渠道/ })).toBeNull()
    expect(screen.getByText('其他')).toBeInTheDocument()
    await user.type(screen.getByLabelText('搜索插件名称或 id'), 'kitchen')
    expect(screen.queryByRole('button', { name: /简单检测/ })).toBeNull()
    await pickKitchen(user)
    expect(screen.getByRole('button', { name: /^厨房水槽/ })).toHaveAttribute('aria-pressed', 'true')
  })

  it('?plugin= 直接选中插件', async () => {
    await mountNew(undefined, '/instances/new?plugin=simple')
    expect(await screen.findByLabelText(/^主机/)).toBeInTheDocument()
  })
})

describe('自动表单：每种字段类型的渲染与取值', () => {
  it('各类型控件齐全，填写后随创建请求提交（含默认值）', async () => {
    const user = userEvent.setup()
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'new1' }))
    })
    await pickKitchen(user)

    // 控件渲染
    expect(screen.getByLabelText(/^地址/)).toHaveValue('')
    expect(screen.getByRole('radio', { name: 'GET' })).toHaveAttribute('aria-checked', 'true') // enum 少量选项
    expect(screen.getByLabelText(/^重试次数/)).toHaveValue('3')
    expect(screen.getByLabelText(/^超时/)).toHaveValue('10s')
    expect(screen.getByRole('switch', { name: '跟随重定向' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByLabelText(/^备注/).tagName).toBe('TEXTAREA')
    expect(screen.getByLabelText(/^回调地址/)).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText(/^代理/)).toHaveValue('')
    expect(screen.getByLabelText('第 1 行的值')).toHaveAttribute('type', 'password') // kv 密钥值
    expect(screen.getByLabelText('城市')).toHaveAttribute('role', 'combobox')
    expect(screen.getByText('允许查询参数')).toBeInTheDocument() // url 校验选项

    await user.type(screen.getByLabelText(/^实例名称/), '  新实例 ')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example/x?y=1')
    await user.click(screen.getByRole('radio', { name: 'HEAD' }))
    await user.clear(screen.getByLabelText(/^重试次数/))
    await user.type(screen.getByLabelText(/^重试次数/), '5')
    await user.click(screen.getByRole('radio', { name: '5s' }))
    await user.click(screen.getByRole('switch', { name: '跟随重定向' }))
    await user.type(screen.getByLabelText(/^备注/), '第一行')
    await user.type(screen.getByLabelText(/^回调地址/), 'https://hooks.example/abc')
    await user.type(screen.getByLabelText('第 1 行名称'), 'Authorization')
    await user.type(screen.getByLabelText('第 1 行的值'), 'Bearer x')
    await user.type(screen.getByLabelText('主机列表 第 1 项'), 'a.example')
    await user.click(screen.getAllByRole('button', { name: '添加一项' })[1])
    await user.type(screen.getByLabelText('主机列表 第 2 项'), 'b.example')
    await user.selectOptions(screen.getByLabelText(/^代理/), 'px1')
    await user.click(screen.getByRole('button', { name: '添加一行' }))
    await user.type(screen.getByLabelText(/^用户名/), 'bob')
    await user.type(screen.getByLabelText(/^密码/), 'pw')

    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'POST', '/api/instances')).toBeDefined())
    expect(lastBody(api.calls, 'POST', '/api/instances')).toEqual({
      plugin_id: 'kitchen',
      name: '新实例',
      interval_seconds: 0,
      config: {
        url: 'https://a.example/x?y=1',
        method: 'HEAD',
        retries: 5,
        timeout: '5s',
        follow: false,
        notes: '第一行',
        mode: 'open',
        hook: 'https://hooks.example/abc',
        headers: { Authorization: 'Bearer x' },
        hosts: ['a.example', 'b.example'],
        accounts: [{ user: 'bob', password: 'pw' }],
        proxy: 'px1',
      },
    })
    expect(await screen.findByText('列表页')).toBeInTheDocument()
    // 逐字输入的控件多，全量并发运行时会超过默认 5 秒
  }, 15_000)

  it('代理下拉：直连 + 本期可用位置（hub、any）的代理', async () => {
    const user = userEvent.setup()
    await mountNew()
    await pickKitchen(user)
    const sel = screen.getByLabelText(/^代理/)
    await waitFor(() => expect(within(sel).getAllByRole('option').length).toBe(3))
    expect(within(sel).getAllByRole('option').map((o) => o.textContent)).toEqual(['直连（不走代理）', 'HK-01 · socks5h', '通用 · http'])
  })

  it('刷新间隔：默认只读，勾选覆盖后按最小间隔校验并以秒提交', async () => {
    const user = userEvent.setup()
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'n' }))
    })
    await pickKitchen(user)
    const ivl = screen.getByLabelText('刷新间隔')
    expect(ivl).toHaveValue('1m')
    expect(ivl).toHaveAttribute('readonly')
    await user.click(screen.getByRole('checkbox', { name: /覆盖插件默认值/ }))
    await user.clear(ivl)
    await user.type(ivl, '10s') // 低于该插件 min_interval 30s
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('超出允许范围')).toBeInTheDocument()
    expect(api.calls.some((c) => c.method === 'POST' && c.url === '/api/instances')).toBe(false)
    await user.clear(ivl)
    await user.type(ivl, '2m')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'POST', '/api/instances')?.interval_seconds).toBe(120))
  })
})

describe('visible_when', () => {
  it('条件不成立时隐藏字段，隐藏的字段不提交也不校验必填', async () => {
    const user = userEvent.setup()
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'n' }))
    })
    await pickKitchen(user)
    expect(screen.queryByLabelText(/^令牌/)).toBeNull()
    await user.click(screen.getByRole('radio', { name: '认证' }))
    const token = await screen.findByLabelText(/^令牌/)
    expect(screen.getByText(/visible_when: mode = auth/)).toBeInTheDocument()
    await user.type(token, 'secret-token')
    // 切回开放：令牌字段消失，残留的输入不能被提交
    await user.click(screen.getByRole('radio', { name: '开放' }))
    expect(screen.queryByLabelText(/^令牌/)).toBeNull()
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'POST', '/api/instances')).toBeDefined())
    const cfg = lastBody(api.calls, 'POST', '/api/instances').config as Record<string, unknown>
    expect(cfg).not.toHaveProperty('token')
    expect(cfg.mode).toBe('open')
  })

  it('条件成立后必填生效', async () => {
    const user = userEvent.setup()
    const api = await mountNew()
    await pickKitchen(user)
    await user.click(screen.getByRole('radio', { name: '认证' }))
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findAllByText('必填项不能为空')).toHaveLength(1)
    expect(api.calls.some((c) => c.method === 'POST' && c.url === '/api/instances')).toBe(false)
  })
})

describe('编辑实例：密钥', () => {
  const secretDetail = () =>
    makeDetail({
      config: {
        url: 'https://a.example',
        mode: 'auth',
        token: { set: true },
        hook: { set: true },
        headers: { Authorization: { set: true } },
        accounts: [{ user: 'bob', password: { set: true, ref: 0 } }],
      },
    })

  it('已设置的密钥显示「已设置」，留空保存时请求体里没有该字段', async () => {
    const user = userEvent.setup()
    const api = await mountEdit(secretDetail(), (req) => {
      if (req.method === 'PUT' && req.url === '/api/instances/i1') return json(200, secretDetail())
    })
    const token = await screen.findByLabelText(/^令牌/)
    expect(token).toHaveValue('')
    expect(token).toHaveAttribute('placeholder', '已设置，留空则保持不变')
    expect(screen.getAllByText('已设置').length).toBeGreaterThan(0)

    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'PUT', '/api/instances/i1')).toBeDefined())
    const body = lastBody(api.calls, 'PUT', '/api/instances/i1')
    expect(body).not.toHaveProperty('plugin_id')
    const cfg = body.config as Record<string, unknown>
    expect(cfg).not.toHaveProperty('token')
    expect(cfg).not.toHaveProperty('hook')
    // kv 的值与 object_list 内的密钥按服务端约定回传标记
    expect(cfg.headers).toEqual({ Authorization: { set: true } })
    expect(cfg.accounts).toEqual([{ user: 'bob', password: { set: true, ref: 0 } }])
  })

  it('输入新值才会提交新的密钥', async () => {
    const user = userEvent.setup()
    const api = await mountEdit(secretDetail(), (req) => {
      if (req.method === 'PUT') return json(200, secretDetail())
    })
    await user.type(await screen.findByLabelText(/^令牌/), 'new-token')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'PUT', '/api/instances/i1')).toBeDefined())
    expect((lastBody(api.calls, 'PUT', '/api/instances/i1').config as Record<string, unknown>).token).toBe('new-token')
  })

  it('配置损坏需重填：提示重新填写，已设置标记不再沿用', async () => {
    await mountEdit(makeDetail({ config: { url: 'https://a.example', mode: 'auth' }, problems: { _config: 'invalid' } }))
    expect(await screen.findByText(/配置已损坏，或其中的密钥无法解密/)).toBeInTheDocument()
    expect(screen.getByLabelText(/^令牌/)).toHaveAttribute('placeholder', '输入密钥')
  })

  it('复制出的实例：密钥为空，服务端的必填问题直接标在字段上', async () => {
    await mountEdit(makeDetail({ config: { url: 'https://a.example', mode: 'auth' }, problems: { token: 'required' } }))
    expect(await screen.findByText('必填项不能为空')).toBeInTheDocument()
    expect(screen.getByLabelText(/^令牌/)).toHaveAttribute('aria-invalid', 'true')
  })
})

describe('lookup', () => {
  const candidates = [
    { value: '{"name":"杭州","lat":30.2,"lon":120.1}', label: '杭州, 浙江, 中国' },
    { value: '{"name":"杭州湾","lat":30.3,"lon":121.2}', label: '杭州湾新区, 浙江, 中国' },
  ]

  it('输入停顿后才请求（防抖），连续输入只发最后一次；选中候选后随请求提交原始值', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url.startsWith('/api/plugins/kitchen/lookup/city')) return json(200, { candidates })
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'n' }))
    })
    await pickKitchen(user)
    const lookupCalls = () => api.calls.filter((c) => c.url.includes('/lookup/city'))
    const box = screen.getByLabelText('城市')
    await user.type(box, '杭州')
    expect(lookupCalls()).toHaveLength(0) // 还在防抖
    await act(async () => {
      await vi.advanceTimersByTimeAsync(350)
    })
    expect(lookupCalls()).toHaveLength(1)
    expect(lookupCalls()[0].body).toEqual({ query: '杭州' })
    expect(lookupCalls()[0].url).toContain('lang=zh')
    const options = within(await screen.findByRole('listbox')).getAllByRole('option')
    expect(options.map((o) => o.textContent)).toEqual(['杭州, 浙江, 中国', '杭州湾新区, 浙江, 中国'])

    await user.click(options[0])
    expect(box).toHaveValue('杭州, 浙江, 中国')

    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'POST', '/api/instances')).toBeDefined())
    expect((lastBody(api.calls, 'POST', '/api/instances').config as Record<string, unknown>).city).toBe(candidates[0].value)
  })

  it('没有匹配时提示，插件不支持时给出说明', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    let mode: 'empty' | 'denied' = 'empty'
    await mountNew((req) => {
      if (req.method === 'POST' && req.url.includes('/lookup/city')) {
        return mode === 'empty' ? json(200, { candidates: [] }) : apiError(400, 'validation.failed', { fields: { city: 'invalid' } })
      }
    })
    await pickKitchen(user)
    const box = screen.getByLabelText('城市')
    await user.type(box, 'zzz')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(350)
    })
    expect(await screen.findByText('没有匹配的结果')).toBeInTheDocument()
    mode = 'denied'
    await user.clear(box)
    await user.type(box, 'abc')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(350)
    })
    expect(await screen.findByText('此插件暂不支持候选查询')).toBeInTheDocument()
  })

  it('编辑时已选城市显示名称，重新输入即放弃旧值', async () => {
    const user = userEvent.setup()
    await mountEdit(makeDetail({ config: { url: 'https://a.example', mode: 'open', city: '{"name":"杭州","lat":1,"lon":2}' } }))
    const box = await screen.findByLabelText('城市')
    expect(box).toHaveValue('杭州')
    await user.type(box, 'x')
    expect(box).toHaveValue('杭州x')
  })
})

describe('服务端字段错误', () => {
  it('逐个定位到字段，含 object_list 内嵌路径与 kv、列表项；修改字段后清除该处错误', async () => {
    const user = userEvent.setup()
    await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') {
        return apiError(400, 'validation.failed', {
          fields: {
            name: 'required',
            url: 'invalid',
            'hosts[0]': 'pattern_mismatch',
            'accounts[0].user': 'required',
            proxy: 'invalid',
            gone: 'invalid',
          },
        })
      }
    })
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.type(screen.getByLabelText('主机列表 第 1 项'), 'ok.host')
    await user.click(screen.getByRole('button', { name: '添加一行' }))
    await user.type(screen.getByLabelText(/^用户名/), 'u')
    await user.type(screen.getByLabelText(/^密码/), 'p')
    await user.click(screen.getByRole('button', { name: '保存' }))

    const row = (path: string) => document.querySelector(`[data-field="${path}"]`) as HTMLElement
    await waitFor(() => expect(within(row('url')).getByText('格式不正确')).toBeInTheDocument())
    expect(within(row('url')).getByLabelText(/^地址/)).toHaveAttribute('aria-invalid', 'true')
    expect(within(row('hosts')).getByText('格式不符合要求')).toBeInTheDocument()
    expect(within(row('accounts[0].user')).getByText('必填项不能为空')).toBeInTheDocument()
    expect(within(row('proxy')).getByText('格式不正确')).toBeInTheDocument()
    // 实例名称（通用字段）
    expect(screen.getAllByText('必填项不能为空').length).toBeGreaterThanOrEqual(2)
    // 无法对应到字段的错误单独列出
    expect(screen.getByText('以下问题无法对应到表单字段：')).toBeInTheDocument()
    expect(screen.getByText(/gone：格式不正确/)).toBeInTheDocument()

    await user.type(screen.getByLabelText(/^地址/), '/p')
    expect(within(row('url')).queryByText('格式不正确')).toBeNull()
    expect(within(row('proxy')).getByText('格式不正确')).toBeInTheDocument()
  })

  it('客户端即时校验不通过时不发请求，并提示数量', async () => {
    const user = userEvent.setup()
    const api = await mountNew()
    await pickKitchen(user)
    await user.click(screen.getByRole('button', { name: '保存' }))
        expect(screen.getByText(/还有 2 处需要修正/)).toBeInTheDocument()
    expect(api.calls.some((c) => c.method === 'POST' && c.url === '/api/instances')).toBe(false)
  })
})

describe('保存并测试', () => {
  const okRun = {
    instance: { ...makeDetail(), display_state: 'ok', last_error: '' },
    report: {
      status: 'ok',
      summary: '200 · 312 ms',
      duration_ms: 318,
      collected_at: 1,
      items: [
        { key: 'latency', type: 'number', value: 312, unit: 'ms' },
        { key: 'up', type: 'state', status: 'ok' },
      ],
      state: 'PRIVATE-STATE',
    },
  }

  it('成功：显示状态、耗时、数据项（带插件声明的标题）与可折叠的原始报告，原始报告不含私有 state', async () => {
    const user = userEvent.setup()
    const api = await mountEdit(makeDetail(), (req) => {
      if (req.method === 'PUT') return json(200, makeDetail())
      if (req.method === 'POST' && req.url === '/api/instances/i1/run') return json(200, okRun)
    })
    await screen.findByLabelText(/^地址/)
    expect(screen.getByText(/尚未测试/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    expect(await screen.findByText(/耗时 318 ms/)).toBeInTheDocument()
    expect(screen.getByText('200 · 312 ms')).toBeInTheDocument()
    expect(screen.getByText('延迟')).toBeInTheDocument()
    expect(screen.getByText('数据项（2）')).toBeInTheDocument()
    const order = api.calls.filter((c) => c.method !== 'GET').map((c) => `${c.method} ${c.url}`)
    expect(order).toEqual(['PUT /api/instances/i1', 'POST /api/instances/i1/run'])
    const raw = screen.getByText('原始报告 JSON').closest('details') as HTMLElement
    expect(raw).not.toHaveAttribute('open')
    expect(raw.textContent).not.toContain('PRIVATE-STATE')
    expect(raw.textContent).toContain('"duration_ms": 318')
  })

  it('失败：显示错误信息，并说明实例已保存', async () => {
    const user = userEvent.setup()
    await mountEdit(makeDetail(), (req) => {
      if (req.method === 'PUT') return json(200, makeDetail())
      if (req.method === 'POST' && req.url === '/api/instances/i1/run') {
        return apiError(502, 'run.failed', { message: 'dial tcp 10.0.0.1:80: connection refused' })
      }
    })
    await screen.findByLabelText(/^地址/)
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    const alert = await screen.findByText(/connection refused/)
    expect(alert.textContent).toContain('采集失败')
    expect(screen.getByText(/实例已保存，但本次采集失败/)).toBeInTheDocument()
    expect(screen.queryByText(/耗时/)).toBeNull()
  })

  it('新建后保存并测试：创建 → 运行 → 切到编辑页并保留结果', async () => {
    const user = userEvent.setup()
    const created = makeDetail({ id: 'n1', name: '新的' })
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, created)
      if (req.method === 'GET' && req.url === '/api/instances/n1') return json(200, created)
      if (req.method === 'POST' && req.url === '/api/instances/n1/run') return json(200, okRun)
    })
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), '新的')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    expect(await screen.findByText(/耗时 318 ms/)).toBeInTheDocument()
    expect(screen.getByTestId('loc')).toHaveTextContent('/instances/n1/edit')
    expect(screen.getByLabelText(/^实例名称/)).toHaveValue('新的')
    expect(api.calls.filter((c) => c.url.endsWith('/run'))).toHaveLength(1)
  })

  it('保存请求带上按插件 timeout 算出的超时（不少于 60 秒）', async () => {
    const user = userEvent.setup()
    const spy = vi.spyOn(globalThis, 'setTimeout')
    await mountEdit(makeDetail(), (req) => {
      if (req.method === 'PUT') return json(200, makeDetail())
      if (req.method === 'POST' && req.url.endsWith('/run')) return json(200, okRun)
    })
    await screen.findByLabelText(/^地址/)
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    await screen.findByText(/耗时 318 ms/)
    // timeout_seconds=20 → max(60s, 2×20+10=50s) = 60s
    expect(spy.mock.calls.some((c) => c[1] === Math.max(60_000, (2 * kitchenPlugin.timeout_seconds + 10) * 1000))).toBe(true)
    spy.mockRestore()
  })
})

describe('编辑页状态', () => {
  it('实例不存在', async () => {
    mockApi(handler((req) => (req.url === '/api/instances/zz' ? apiError(404, 'instance.not_found') : undefined)))
    await renderWithApp(
      <Routes>
        <Route path="/instances/:id/edit" element={<InstanceEditPage />} />
      </Routes>,
      { route: '/instances/zz/edit' },
    )
    expect(await screen.findByText(/这个实例不存在/)).toBeInTheDocument()
  })

  it('weather 插件显示 Open-Meteo 署名', async () => {
    const weather = { ...editorPlugins.plugins[1], id: 'weather', name: '天气', origin: 'builtin' as const }
    mockApi(
      handler((req) => {
        if (req.method === 'GET' && req.url.startsWith('/api/plugins')) return json(200, { ...editorPlugins, plugins: [weather] })
      }),
    )
    const user = userEvent.setup()
    await renderWithApp(<InstanceNewPage />)
    await user.click(await screen.findByRole('button', { name: /^天气/ }))
    const link = await screen.findByRole('link', { name: 'Weather data by Open-Meteo.com' })
    expect(link).toHaveAttribute('href', 'https://open-meteo.com/')
  })

  it('英文界面使用英文文案', async () => {
    mockApi(handler())
    await renderWithApp(<InstanceNewPage />, { lng: 'en' })
    expect(await screen.findByText('Search plugin name or id', { selector: 'input' }).catch(() => screen.getByPlaceholderText('Search plugin name or id'))).toBeDefined()
    expect(screen.getByText('New instance')).toBeInTheDocument()
  })
})

describe('列表、键值与行状态', () => {
  async function fillHosts(user: ReturnType<typeof userEvent.setup>, rows: string[]) {
    for (let i = 0; i < rows.length; i++) {
      if (i > 0) await user.click(screen.getAllByRole('button', { name: '添加一项' })[1])
      if (rows[i]) await user.type(screen.getByLabelText(`主机列表 第 ${i + 1} 项`), rows[i])
    }
  }
  const invalidRows = () => [1, 2, 3].map((n) => screen.queryByLabelText(`主机列表 第 ${n} 项`)?.getAttribute('aria-invalid') === 'true')

  it('list 中间有空行时，客户端校验标红的是真正出错的行', async () => {
    const user = userEvent.setup()
    await mountNew()
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await fillHosts(user, ['a', '', 'B1'])
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('格式不符合要求')
    expect(invalidRows()).toEqual([false, false, true])
  })

  it('list 中间有空行时，服务端按压缩后下标返回的错误也标到正确的行', async () => {
    const user = userEvent.setup()
    await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return apiError(400, 'validation.failed', { fields: { 'hosts[1]': 'pattern_mismatch' } })
    })
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await fillHosts(user, ['a', '', 'ok'])
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('格式不符合要求')
    expect(invalidRows()).toEqual([false, false, true])
  })

  it('密钥 kv 只填键名、值留空：该行值框标红并阻止提交；键名首尾空格不影响定位', async () => {
    const user = userEvent.setup()
    const api = await mountNew()
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.type(screen.getByLabelText('第 1 行名称'), ' X-New ')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.getByLabelText('第 1 行的值')).toHaveAttribute('aria-invalid', 'true'))
    expect(api.calls.some((c) => c.method === 'POST' && c.url === '/api/instances')).toBe(false)
  })

  it('kv 重复键：重复的那一行键名标红', async () => {
    const user = userEvent.setup()
    await mountNew()
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), 'x')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.type(screen.getByLabelText('第 1 行名称'), 'A')
    await user.type(screen.getByLabelText('第 1 行的值'), '1')
    await user.click(screen.getAllByRole('button', { name: '添加一项' })[0])
    await user.type(screen.getByLabelText('第 2 行名称'), 'A')
    await user.type(screen.getByLabelText('第 2 行的值'), '2')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.getByLabelText('第 2 行名称')).toHaveAttribute('aria-invalid', 'true'))
  })

  it('object_list 删中间行后，剩余行的已设置密钥仍带各自的原下标；行内控件状态不错位', async () => {
    const user = userEvent.setup()
    const detail = makeDetail({
      config: {
        url: 'https://a.example',
        mode: 'open',
        accounts: [
          { user: 'u0', password: { set: true, ref: 0 } },
          { user: 'u1', password: { set: true, ref: 1 } },
          { user: 'u2', password: { set: true, ref: 2 } },
        ],
      },
    })
    const api = await mountEdit(detail, (req) => {
      if (req.method === 'PUT') return json(200, detail)
    })
    await screen.findByLabelText(/^地址/)
    // 第 3 行的密码框切到明文显示，删掉第 2 行后它应仍是明文（状态跟着行走，不跟着位置走）
    const pwBefore = screen.getAllByLabelText(/^密码/)
    await user.click(within(pwBefore[2].parentElement as HTMLElement).getByRole('button', { name: '显示' }))
    expect(pwBefore[2]).toHaveAttribute('type', 'text')
    await user.click(screen.getByRole('button', { name: '删除第 2 项' }))
    const pwAfter = screen.getAllByLabelText(/^密码/)
    expect(pwAfter).toHaveLength(2)
    expect(pwAfter[0]).toHaveAttribute('type', 'password')
    expect(pwAfter[1]).toHaveAttribute('type', 'text')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'PUT', '/api/instances/i1')).toBeDefined())
    expect((lastBody(api.calls, 'PUT', '/api/instances/i1').config as Record<string, unknown>).accounts).toEqual([
      { user: 'u0', password: { set: true, ref: 0 } },
      { user: 'u2', password: { set: true, ref: 2 } },
    ])
  })
})

describe('enum 与 number', () => {
  const plugin = {
    ...kitchenPlugin,
    config_schema: [
      { key: 'req', type: 'enum', title: '必选项', required: true, options: [{ value: 'a', title: 'A' }, { value: 'b', title: 'B' }] },
      { key: 'opt', type: 'enum', title: '可选项', required: false, options: [{ value: 'x', title: 'X' }, { value: 'y', title: 'Y' }] },
      { key: 'n', type: 'number', title: '数量', required: false, default: 3 },
    ],
  }
  const mount = () =>
    mountNew((req) => {
      if (req.method === 'GET' && req.url.startsWith('/api/plugins')) return json(200, { ...editorPlugins, plugins: [plugin] })
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, makeDetail({ id: 'n' }))
    })

  it('必填 enum 没有默认值时不预选，提交报必填；非必填 enum 可取消选择', async () => {
    const user = userEvent.setup()
    const api = await mount()
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    await user.type(await screen.findByLabelText(/^实例名称/), 'x')
    const group = screen.getByRole('radiogroup', { name: '必选项' })
    expect(within(group).queryByRole('radio', { checked: true })).toBeNull()
    await user.click(screen.getByRole('radio', { name: 'X' }))
    await user.click(screen.getByRole('radio', { name: '未选择' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('必填项不能为空')
    expect(api.calls.some((c) => c.method === 'POST' && c.url === '/api/instances')).toBe(false)
    await user.click(within(group).getByRole('radio', { name: 'B' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastBody(api.calls, 'POST', '/api/instances')).toBeDefined())
    expect((lastBody(api.calls, 'POST', '/api/instances').config as Record<string, unknown>)).toEqual({ req: 'b', n: 3 })
  })

  it('数字框里输入非法文本报格式不正确，而不是回落默认值', async () => {
    const user = userEvent.setup()
    const api = await mount()
    await user.click(await screen.findByRole('button', { name: /^厨房水槽/ }))
    await user.type(await screen.findByLabelText(/^实例名称/), 'x')
    await user.click(screen.getByRole('radio', { name: 'A' }))
    const n = screen.getByLabelText(/^数量/)
    await user.clear(n)
    await user.type(n, '1e')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('格式不正确')).toBeInTheDocument()
    expect(api.calls.some((c) => c.method === 'POST' && c.url === '/api/instances')).toBe(false)
  })
})

describe('保存并测试：补充', () => {
  it('该实例正在采集（run.busy）单独提示，不当作采集失败', async () => {
    const user = userEvent.setup()
    await mountEdit(makeDetail(), (req) => {
      if (req.method === 'PUT') return json(200, makeDetail())
      if (req.method === 'POST' && req.url.endsWith('/run')) return apiError(409, 'run.busy')
    })
    await screen.findByLabelText(/^地址/)
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    expect(await screen.findByText(/实例已保存；该实例正在采集中/)).toBeInTheDocument()
    expect(screen.queryByText(/实例已保存，但本次采集失败/)).toBeNull()
  })

  it('新建后先创建并立即切到编辑页，再在编辑页发起运行；路由 state 用后即清', async () => {
    const user = userEvent.setup()
    const created = makeDetail({ id: 'n9', name: '新的' })
    const api = await mountNew((req) => {
      if (req.method === 'POST' && req.url === '/api/instances') return json(200, created)
      if (req.method === 'GET' && req.url === '/api/instances/n9') return json(200, created)
      if (req.method === 'POST' && req.url === '/api/instances/n9/run') return new Promise<Response>(() => {}) // 一直不返回：模拟运行中途中断
    })
    await pickKitchen(user)
    await user.type(screen.getByLabelText(/^实例名称/), '新的')
    await user.type(screen.getByLabelText(/^地址/), 'https://a.example')
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/instances/n9/edit'))
    expect(await screen.findByText(/正在采集，请稍候/)).toBeInTheDocument()
    expect(api.calls.filter((c) => c.method === 'POST' && c.url === '/api/instances')).toHaveLength(1)
  })

  it('保存并测试的运行超时随插件 timeout 变化：timeout 40s → 90s', async () => {
    const user = userEvent.setup()
    const slow = { ...kitchenPlugin, timeout_seconds: 40 }
    const spy = vi.spyOn(globalThis, 'setTimeout')
    await mountEdit(makeDetail(), (req) => {
      if (req.method === 'GET' && req.url.startsWith('/api/plugins')) return json(200, { ...editorPlugins, plugins: [slow] })
      if (req.method === 'PUT') return json(200, makeDetail())
      if (req.method === 'POST' && req.url.endsWith('/run')) return apiError(502, 'run.failed', { message: 'x' })
    })
    await screen.findByLabelText(/^地址/)
    await user.click(screen.getByRole('button', { name: '保存并测试' }))
    await screen.findByText(/实例已保存，但本次采集失败/)
    expect(spy.mock.calls.some((c) => c[1] === 90_000)).toBe(true)
    spy.mockRestore()
  })
})

describe('lookup：竞态', () => {
  it('旧请求晚到的响应被丢弃，不覆盖新输入的候选', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    const resolvers: ((r: Response) => void)[] = []
    await mountNew((req) => {
      if (req.method === 'POST' && req.url.includes('/lookup/city')) return new Promise<Response>((res) => resolvers.push(res))
    })
    await pickKitchen(user)
    const box = screen.getByLabelText('城市')
    await user.type(box, 'a')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(350)
    })
    await user.type(box, 'b')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(350)
    })
    expect(resolvers).toHaveLength(2)
    // 新请求先返回，旧请求后返回
    await act(async () => {
      resolvers[1](json(200, { candidates: [{ value: 'new', label: '新结果' }] }))
    })
    expect(await screen.findByText('新结果')).toBeInTheDocument()
    await act(async () => {
      resolvers[0](json(200, { candidates: [{ value: 'old', label: '旧结果' }] }))
    })
    expect(screen.queryByText('旧结果')).toBeNull()
    expect(screen.getByText('新结果')).toBeInTheDocument()
  })
})
