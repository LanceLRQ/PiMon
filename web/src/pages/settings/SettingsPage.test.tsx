import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { createMemoryRouter, Link, RouterProvider } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { apiError, json, mockApi, type Req } from '@/pages/instances/test-utils'
import type { BackupInfo, BackupRemote, Settings } from '@/types/generated'
import { ToastProvider } from '@/ui/toast'
import { SettingsPage } from './SettingsPage'

afterEach(() => vi.unstubAllGlobals())

const baseSettings: Settings = {
  language: 'zh',
  timezone: 'Asia/Shanghai',
  access_url: '',
  trusted_proxies: ['192.168.1.2/32'],
  https_enabled: false,
  reduce_effects: false,
  screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1, daily_restart: { enabled: false, at: '04:00' } },
  retention: { raw_hours: 24, five_min_days: 30, hour_days: 365 },
  backup: { daily_at: '04:00', keep: 7 },
}

const backups: BackupInfo[] = [
  { name: 'pimon-backup-20260927-040000-daily.tar.gz', reason: 'daily', created_at: '2026-09-27T04:00:00Z', size: 4_100_000 },
  { name: 'pimon-backup-20260928-040000-daily.tar.gz', reason: 'daily', created_at: '2026-09-28T04:00:00Z', size: 4_200_000 },
]

interface Opts {
  settings?: Settings
  extra?: (req: Req) => Response | undefined | Promise<Response | undefined>
}

async function setup({ settings = baseSettings, extra }: Opts = {}) {
  const api = mockApi((req) => {
    const r = extra?.(req)
    if (r) return r
    if (req.method === 'GET' && req.url === '/api/settings') return json(200, settings)
    if (req.method === 'GET' && req.url === '/api/backups') return json(200, backups)
    return undefined
  })
  const i18n = await createI18n('zh')
  const router = createMemoryRouter(
    [
      {
        path: '/settings',
        element: (
          <>
            <SettingsPage />
            <Link to="/other">去别处</Link>
          </>
        ),
      },
      { path: '/other', element: <div>其他页面</div> },
    ],
    { initialEntries: ['/settings'] },
  )
  render(
    <I18nextProvider i18n={i18n}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </I18nextProvider>,
  )
  await screen.findByLabelText('访问地址')
  return { api, router }
}

const saveBar = () => screen.queryByRole('region', { name: /未保存/ })

describe('设置页脏状态与保存条', () => {
  it('加载后没有保存条；改动出现保存条并计数，改回原值消失', async () => {
    await setup()
    expect(saveBar()).not.toBeInTheDocument()
    const url = screen.getByLabelText('访问地址')
    await userEvent.type(url, 'https://pimon.home.arpa')
    expect(saveBar()).toHaveTextContent('有 1 处未保存的修改')
    await userEvent.click(screen.getByRole('switch', { name: '降低特效' }))
    expect(saveBar()).toHaveTextContent('有 2 处未保存的修改')
    await userEvent.clear(url)
    expect(saveBar()).toHaveTextContent('有 1 处未保存的修改')
    await userEvent.click(screen.getByRole('switch', { name: '降低特效' }))
    expect(saveBar()).not.toBeInTheDocument()
  })

  it('放弃：恢复为已保存的值', async () => {
    await setup()
    const url = screen.getByLabelText('访问地址')
    await userEvent.type(url, 'https://x.example')
    await userEvent.click(screen.getByRole('button', { name: '放弃' }))
    expect(url).toHaveValue('')
    expect(saveBar()).not.toBeInTheDocument()
  })

  it('保存：提交规整后的整份设置，保存条消失并提示', async () => {
    const saved = { ...baseSettings, access_url: 'https://pimon.home.arpa', backup: { daily_at: '04:00', keep: 8 } }
    const { api } = await setup({ extra: (r) => (r.method === 'PUT' ? json(200, saved) : undefined) })
    await userEvent.type(screen.getByLabelText('访问地址'), '  https://pimon.home.arpa  ')
    await userEvent.click(screen.getByRole('button', { name: '+' }))
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(saveBar()).not.toBeInTheDocument())
    const put = api.calls.find((c) => c.method === 'PUT')!
    expect(put.url).toBe('/api/settings')
    expect(put.body).toEqual({ ...baseSettings, access_url: 'https://pimon.home.arpa', backup: { daily_at: '04:00', keep: 8 } })
    expect(await screen.findByText('已保存设置')).toBeInTheDocument()
  })

  it('HTTPS 开关旁提示重启后生效，改动后保存给出重启提示', async () => {
    const { api } = await setup({ extra: (r) => (r.method === 'PUT' ? json(200, { ...baseSettings, https_enabled: true }) : undefined) })
    expect(screen.getByText('重启后生效')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('switch', { name: 'HTTPS' }))
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/HTTPS 的改动需重启 hub 后生效/)).toBeInTheDocument()
    expect((api.calls.find((c) => c.method === 'PUT')!.body as Settings).https_enabled).toBe(true)
  })

  it('HTTPS 开关处提示本机 kiosk 屏幕需要 HTTP，建议用 nginx 反代', async () => {
    await setup()
    expect(await screen.findByText('本机 kiosk 屏幕需要 HTTP，开启 HTTPS 后屏幕会断开；需要 HTTPS 请用 nginx 反代。')).toBeInTheDocument()
  })

  it('服务端字段错误回显到对应字段，保存条显示未通过数；修改字段后该错误消失', async () => {
    await setup({
      extra: (r) =>
        r.method === 'PUT'
          ? apiError(400, 'validation.failed', {
              fields: { access_url: 'invalid', 'trusted_proxies[0]': 'invalid', 'retention.raw_hours': 'out_of_range', 'backup.daily_at': 'invalid' },
            })
          : undefined,
    })
    const url = screen.getByLabelText('访问地址')
    await userEvent.type(url, 'ftp://x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('访问地址须是 http:// 或 https:// 开头的有效地址')).toBeInTheDocument()
    expect(screen.getByText('不是合法的 IP 或 CIDR')).toBeInTheDocument()
    expect(screen.getByText('原始采样保留 1–168 小时')).toBeInTheDocument()
    expect(screen.getByText('备份时刻须为 HH:MM')).toBeInTheDocument()
    expect(url).toHaveAttribute('aria-invalid', 'true')
    expect(saveBar()).toHaveTextContent('4 处未通过校验')
    await userEvent.type(url, 'x')
    expect(screen.queryByText('访问地址须是 http:// 或 https:// 开头的有效地址')).not.toBeInTheDocument()
    expect(saveBar()).toHaveTextContent('3 处未通过校验')
  })

  it('非字段错误显示在顶部并保留修改', async () => {
    await setup({ extra: (r) => (r.method === 'PUT' ? apiError(500, 'internal') : undefined) })
    await userEvent.type(screen.getByLabelText('访问地址'), 'https://a.example')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/保存失败：中枢内部错误/)).toBeInTheDocument()
    expect(saveBar()).toBeInTheDocument()
  })
})

describe('受信任反代列表', () => {
  it('增删行，重复来源给出提示，并说明只采信哪些头', async () => {
    await setup()
    expect(screen.getByText('192.168.1.2/32')).toBeInTheDocument()
    expect(screen.getByText('X-Forwarded-*')).toBeInTheDocument()
    const input = screen.getByLabelText('添加受信任反代')
    await userEvent.type(input, '10.0.0.0/24{Enter}')
    expect(screen.getByText('10.0.0.0/24')).toBeInTheDocument()
    expect(saveBar()).toHaveTextContent('有 1 处未保存的修改')
    await userEvent.type(input, '10.0.0.0/24{Enter}')
    expect(screen.getByText('这个来源已在列表里')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '移除 10.0.0.0/24' }))
    expect(screen.queryByText('10.0.0.0/24')).not.toBeInTheDocument()
    expect(saveBar()).not.toBeInTheDocument()
  })
})

describe('离开页面前的未保存确认', () => {
  it('没有修改时直接离开', async () => {
    const { router } = await setup()
    await userEvent.click(screen.getByRole('link', { name: '去别处' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/other'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('有修改时路由切换被自建对话框拦截：留下保留修改，确认离开才跳转', async () => {
    const { router } = await setup()
    await userEvent.type(screen.getByLabelText('访问地址'), 'https://a.example')
    await userEvent.click(screen.getByRole('link', { name: '去别处' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText('离开设置页？')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings')
    await userEvent.click(within(dlg).getByRole('button', { name: '留在此页' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByLabelText('访问地址')).toHaveValue('https://a.example')
    await userEvent.click(screen.getByRole('link', { name: '去别处' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '放弃修改并离开' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/other'))
    expect(await screen.findByText('其他页面')).toBeInTheDocument()
  })

  it('保存后不再拦截', async () => {
    const { router } = await setup({ extra: (r) => (r.method === 'PUT' ? json(200, { ...baseSettings, access_url: 'https://a.example' }) : undefined) })
    await userEvent.type(screen.getByLabelText('访问地址'), 'https://a.example')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(saveBar()).not.toBeInTheDocument())
    await userEvent.click(screen.getByRole('link', { name: '去别处' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/other'))
  })

  it('关闭标签页：有修改时 beforeunload 被拦截，没有修改时不拦截', async () => {
    await setup()
    const probe = () => {
      const ev = new Event('beforeunload', { cancelable: true })
      act(() => void window.dispatchEvent(ev))
      return ev.defaultPrevented
    }
    expect(probe()).toBe(false)
    await userEvent.type(screen.getByLabelText('访问地址'), 'x')
    expect(probe()).toBe(true)
    await userEvent.click(screen.getByRole('button', { name: '放弃' }))
    expect(probe()).toBe(false)
  })
})

describe('修改密码对话框', () => {
  async function open() {
    const s = await setup({ extra: (r) => (r.method === 'PUT' && r.url === '/api/admin/password' ? json(200, { ok: true }) : undefined) })
    await userEvent.click(screen.getByRole('button', { name: /修改密码/ }))
    return { ...s, dlg: await screen.findByRole('dialog') }
  }

  it('本地校验：缺当前密码、新密码过短、两次不一致', async () => {
    const { api, dlg } = await open()
    await userEvent.click(within(dlg).getByRole('button', { name: '修改密码' }))
    expect(within(dlg).getByText('请输入当前密码')).toBeInTheDocument()
    expect(within(dlg).getByText('新密码至少 8 位')).toBeInTheDocument()
    await userEvent.type(within(dlg).getByLabelText('当前密码'), 'old-password')
    await userEvent.type(within(dlg).getByLabelText('新密码'), 'new-password-1')
    await userEvent.type(within(dlg).getByLabelText('确认新密码'), 'new-password-2')
    await userEvent.click(within(dlg).getByRole('button', { name: '修改密码' }))
    expect(within(dlg).getByText('两次输入的新密码不一致')).toBeInTheDocument()
    expect(api.calls.some((c) => c.url === '/api/admin/password')).toBe(false)
  })

  it('成功：提交当前与新密码并关闭对话框', async () => {
    const { api, dlg } = await open()
    await userEvent.type(within(dlg).getByLabelText('当前密码'), 'old-password')
    await userEvent.type(within(dlg).getByLabelText('新密码'), 'new-password-1')
    await userEvent.type(within(dlg).getByLabelText('确认新密码'), 'new-password-1')
    await userEvent.click(within(dlg).getByRole('button', { name: '修改密码' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(api.calls.find((c) => c.url === '/api/admin/password')!.body).toEqual({ current_password: 'old-password', new_password: 'new-password-1' })
    expect(await screen.findByText('密码已修改，其他会话已退出')).toBeInTheDocument()
  })

  it('当前密码错误：显示剩余次数', async () => {
    await setup({ extra: (r) => (r.url === '/api/admin/password' ? apiError(401, 'auth.invalid_password', { remaining: 7 }) : undefined) })
    await userEvent.click(screen.getByRole('button', { name: /修改密码/ }))
    const dlg = await screen.findByRole('dialog')
    await userEvent.type(within(dlg).getByLabelText('当前密码'), 'bad')
    await userEvent.type(within(dlg).getByLabelText('新密码'), 'new-password-1')
    await userEvent.type(within(dlg).getByLabelText('确认新密码'), 'new-password-1')
    await userEvent.click(within(dlg).getByRole('button', { name: '修改密码' }))
    expect(await within(dlg).findByText('当前密码错误，还可尝试 7 次')).toBeInTheDocument()
  })
})

describe('备份与其余分组', () => {
  it('下载最新备份前先提示含密钥文件，确认链接指向该备份', async () => {
    await setup()
    await userEvent.click(screen.getByRole('button', { name: /下载 pimon-backup-20260928/ }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText(/含密钥|密钥文件/)).toBeInTheDocument()
    const link = within(dlg).getByRole('link', { name: '我已了解，下载' })
    expect(link).toHaveAttribute('href', '/api/backups/pimon-backup-20260928-040000-daily.tar.gz')
  })

  it('保留份数步进到边界时禁用', async () => {
    await setup({ settings: { ...baseSettings, backup: { daily_at: '04:00', keep: 1 } } })
    expect(screen.getByRole('button', { name: '−' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: '+' }))
    expect(screen.getByRole('button', { name: '−' })).toBeEnabled()
  })

  it('当前值不在常用档位时，数据保留下拉仍显示真实值', async () => {
    await setup({ settings: { ...baseSettings, retention: { raw_hours: 36, five_min_days: 30, hour_days: 365 } } })
    expect(screen.getByLabelText('原始采样')).toHaveValue('36')
  })
})

const savedRemote: BackupRemote = {
  enabled: true,
  type: 'smb',
  smb_host: 'nas.local',
  smb_port: 445,
  smb_share: 'backup',
  smb_dir: 'pimon',
  smb_username: 'pi',
  smb_password: { set: true },
  smb_domain: '',
  webdav_url: '',
  webdav_dir: '',
  webdav_username: '',
  webdav_password: { set: false },
  passphrase: { set: true },
  keep: 30,
  passphrase_changed: false,
}

function nasExtra(opts: { remote?: BackupRemote | null; put?: (r: Req) => Response; test?: (r: Req) => Response } = {}) {
  const remote = opts.remote === undefined ? savedRemote : opts.remote
  return (r: Req) => {
    if (r.url === '/api/backups/remote' && r.method === 'GET') return remote ? json(200, remote) : apiError(404, 'not_found')
    if (r.url === '/api/backups/remote' && r.method === 'PUT') return opts.put ? opts.put(r) : json(200, savedRemote)
    if (r.url === '/api/backups/remote/test' && r.method === 'POST') return opts.test ? opts.test(r) : json(200, { success: true, target_type: 'smb', elapsed_ms: 120 })
    return undefined
  }
}

describe('NAS 备份', () => {
  it('不再灰显或标注 M2，字段可编辑', async () => {
    await setup({ extra: nasExtra() })
    expect(await screen.findByLabelText('主机')).toHaveValue('nas.local')
    expect(screen.queryByText('M2')).not.toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'SMB' })).toBeEnabled()
  })

  it('切换类型后只显示对应字段', async () => {
    await setup({ extra: nasExtra() })
    await screen.findByLabelText('主机')
    expect(screen.queryByLabelText('地址')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: 'WebDAV' }))
    expect(screen.getByLabelText('地址')).toBeInTheDocument()
    expect(screen.queryByLabelText('主机')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('共享名')).not.toBeInTheDocument()
    expect(saveBar()).toHaveTextContent('有 1 处未保存的修改')
  })

  it('密钥字段显示已设置，留空提交时为空串表示保留原值', async () => {
    const { api } = await setup({ extra: nasExtra() })
    const pass = await screen.findByLabelText('备份口令')
    expect(pass).toHaveAttribute('placeholder', '已设置，留空则保持不变')
    expect(pass).toHaveValue('')
    await userEvent.clear(screen.getByLabelText('主机'))
    await userEvent.type(screen.getByLabelText('主机'), 'nas2.local')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'PUT')).toBe(true))
    const put = api.calls.find((c) => c.method === 'PUT')!
    expect(put.url).toBe('/api/backups/remote')
    expect(put.body).toMatchObject({ smb_host: 'nas2.local', smb_password: '', passphrase: '', webdav_password: '', keep: 30 })
  })

  it('只改 NAS 时只调用 NAS 的 PUT', async () => {
    const { api } = await setup({ extra: nasExtra() })
    await userEvent.type(await screen.findByLabelText('域'), 'HOME')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(saveBar()).not.toBeInTheDocument())
    expect(api.calls.filter((c) => c.method === 'PUT').map((c) => c.url)).toEqual(['/api/backups/remote'])
  })

  it('只改通用设置时只调用 settings 的 PUT', async () => {
    const { api } = await setup({ extra: (r) => nasExtra()(r) ?? (r.url === '/api/settings' && r.method === 'PUT' ? json(200, baseSettings) : undefined) })
    await screen.findByLabelText('主机')
    await userEvent.type(screen.getByLabelText('访问地址'), 'https://x.example')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(api.calls.filter((c) => c.method === 'PUT').map((c) => c.url)).toEqual(['/api/settings'])
  })

  it('两边都改时各提交一次，脏计数合并', async () => {
    const { api } = await setup({ extra: (r) => nasExtra()(r) ?? (r.url === '/api/settings' && r.method === 'PUT' ? json(200, baseSettings) : undefined) })
    await userEvent.type(await screen.findByLabelText('域'), 'HOME')
    await userEvent.type(screen.getByLabelText('访问地址'), 'https://x.example')
    expect(saveBar()).toHaveTextContent('有 2 处未保存的修改')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.filter((c) => c.method === 'PUT')).toHaveLength(2))
  })

  it('NAS 保存失败时保留草稿并显示字段错误，不影响另一边', async () => {
    const { api } = await setup({
      extra: nasExtra({ put: () => apiError(400, 'validation.failed', { fields: { passphrase: 'out_of_range', smb_host: 'invalid' } }) }),
    })
    const pass = await screen.findByLabelText('备份口令')
    await userEvent.type(pass, 'short')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('备份口令至少 12 个字符')).toBeInTheDocument()
    expect(screen.getByText('主机不能为空，且不含空白或控制字符')).toBeInTheDocument()
    expect(pass).toHaveValue('short')
    expect(saveBar()).toHaveTextContent('有 1 处未保存的修改')
    expect(api.calls.filter((c) => c.method === 'PUT')).toHaveLength(1)
  })

  it('保存改了口令时弹出警告，且口令框下方始终有丢失提示', async () => {
    await setup({ extra: nasExtra({ put: () => json(200, { ...savedRemote, passphrase_changed: true }) }) })
    expect(await screen.findByText('口令丢失将无法恢复已加密的备份。')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('备份口令'), 'a-new-passphrase-1')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/备份口令已更改/)).toBeInTheDocument()
  })

  it('保留份数说明提示 NAS 空间', async () => {
    await setup({ extra: nasExtra() })
    expect(await screen.findByText(/份数 × 单份大小/)).toBeInTheDocument()
  })

  it('测试连接成功：用表单当前值调用并显示结果', async () => {
    const { api } = await setup({ extra: nasExtra() })
    await userEvent.type(await screen.findByLabelText('域'), 'HOME')
    await userEvent.click(screen.getByRole('button', { name: '测试连接' }))
    expect(await screen.findByText('连接成功（SMB，用时 120 毫秒）')).toBeInTheDocument()
    const call = api.calls.find((c) => c.url === '/api/backups/remote/test')!
    expect(call.body).toMatchObject({ smb_domain: 'HOME', smb_host: 'nas.local' })
  })

  it('测试连接失败：按 reason 翻译，未知 reason 回退通用文案，进行中按钮禁用', async () => {
    let release: (r: Response) => void = () => {}
    const pending = new Promise<Response>((res) => (release = res))
    await setup({
      extra: (r) => (r.url === '/api/backups/remote/test' ? (pending as unknown as Response) : nasExtra()(r)),
    })
    await screen.findByLabelText('主机')
    await userEvent.click(screen.getByRole('button', { name: '测试连接' }))
    expect(screen.getByRole('button', { name: '测试中…' })).toBeDisabled()
    release(apiError(502, 'backup.remote_failed', { reason: 'auth', message: 'x' }))
    expect(await screen.findByText('连接失败：用户名或密码错误')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '测试连接' })).toBeEnabled()
  })

  it('测试连接失败且 reason 未知时显示通用文案', async () => {
    await setup({ extra: nasExtra({ test: () => apiError(502, 'backup.remote_failed', { reason: 'weird' }) }) })
    await screen.findByLabelText('主机')
    await userEvent.click(screen.getByRole('button', { name: '测试连接' }))
    expect(await screen.findByText('连接失败：远端备份失败')).toBeInTheDocument()
  })

  it('读取配置失败（404）时降级显示默认表单并提示', async () => {
    await setup({ extra: nasExtra({ remote: null }) })
    expect(await screen.findByText(/无法读取 NAS 备份配置/)).toBeInTheDocument()
    expect(screen.getByLabelText('端口')).toHaveValue(445)
    expect(screen.getByLabelText('备份口令')).toHaveAttribute('placeholder', '至少 12 个字符')
    expect(saveBar()).not.toBeInTheDocument()
  })
})
