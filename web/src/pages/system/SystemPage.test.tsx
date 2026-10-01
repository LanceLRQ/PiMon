import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, defaultPlugins, json, mockApi, renderWithApp, type Req } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { BackupInfo, LogEntry, LogList, PluginList, ScreenStatus, SystemInfo } from '@/types/generated'
import { SystemPage } from './SystemPage'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

function sysInfo(over: Partial<SystemInfo> = {}): SystemInfo {
  return {
    version: 'v0.1.0-test',
    go_version: 'go1.27.1',
    os: 'linux',
    arch: 'arm64',
    started_at: new Date(Date.now() - (3 * 86400 + 4 * 3600 + 30) * 1000).toISOString(),
    uptime_seconds: 3 * 86400 + 4 * 3600 + 30,
    memory: { sys_bytes: 46 * 1024 * 1024, heap_bytes: 12 * 1024 * 1024 },
    data_dir: { path: '/var/lib/pimon', used_bytes: 312 * 1024 * 1024 },
    disk_writes: { since_start_bytes: 100 * 1024 * 1024, last_24h_bytes: 38 * 1024 * 1024, last_24h_estimated: true },
    plugins: { total: 3, builtin: 2, exec: 1, errors: 0, conflicts: 0, plugin_dir: '/var/lib/pimon/plugins' },
    ...over,
  }
}

const log = (level: string, message: string, attrs = ''): LogEntry => ({ time: '2026-09-28T14:47:08.123Z', level, message, attrs })

const allLogs = [log('info', '服务已就绪', 'addr=:31415 mono=1.2s'), log('warn', '插件目录不可读'), log('error', '备份失败 boom')]

function logsFor(url: string): LogList {
  const level = new URL(url, 'http://x').searchParams.get('level')
  const rank: Record<string, number> = { debug: 0, info: 1, warn: 2, error: 3 }
  const min = level ? rank[level] : 0
  return { entries: allLogs.filter((e) => rank[e.level] >= min), capacity: 500 }
}

const execPlugins: PluginList = {
  ...defaultPlugins,
  plugins: [
    ...defaultPlugins.plugins,
    { ...defaultPlugins.plugins[0], id: 'smart-disk', name: '磁盘 SMART', version: '1.2.0', runtime: 'exec', origin: 'exec' },
  ],
  errors: [{ dir: 'broken-one', id: '', kind: 'invalid_manifest', message: 'plugin.yaml 第 3 行不合法' }],
  conflicts: [{ dir: 'dup', id: 'demo', kind: 'conflict', message: '与内置插件 demo 重名' }],
}

const backups: BackupInfo[] = [
  { name: 'pimon-backup-20260928-040000-daily.tar.gz', reason: 'daily', created_at: '2026-09-28T04:00:00Z', size: 4_200_000 },
  { name: 'pimon-backup-20260925-183100-pre-upgrade.tar.gz', reason: 'pre-upgrade', created_at: '2026-09-25T18:31:00Z', size: 3_900_000 },
]

interface Opts {
  info?: SystemInfo
  extra?: (req: Req) => Response | undefined | Promise<Response | undefined>
}

async function setup({ info = sysInfo(), extra }: Opts = {}) {
  const api = mockApi((req) => {
    const r = extra?.(req)
    if (r) return r
    if (req.method === 'GET' && req.url === '/api/system') return json(200, info)
    if (req.method === 'GET' && req.url.startsWith('/api/system/logs')) return json(200, logsFor(req.url))
    if (req.method === 'GET' && req.url.startsWith('/api/plugins')) return json(200, execPlugins)
    if (req.method === 'GET' && req.url === '/api/backups') return json(200, backups)
    return undefined
  })
  await renderWithApp(<SystemPage />)
  return api
}

describe('版本与资源', () => {
  it('显示版本、运行时长、内存、数据目录与日写入量', async () => {
    await setup()
    expect((await screen.findAllByText('v0.1.0-test')).length).toBeGreaterThan(0)
    expect(screen.getAllByText('3 天 4 小时').length).toBeGreaterThan(0)
    expect(screen.getByText('go1.27.1 linux/arm64')).toBeInTheDocument()
    expect(screen.getByText('46 MB（堆 12 MB）')).toBeInTheDocument()
    expect(screen.getByText('/var/lib/pimon')).toBeInTheDocument()
    expect(screen.getByText('312 MB')).toBeInTheDocument()
    expect(screen.getByText('38 MB')).toBeInTheDocument()
    expect(screen.getByText('按启动以来的平均速率折算')).toBeInTheDocument()
  })

  it('缺失不当作零：平台读不到写入量、目录用量未知时显示「未知」', async () => {
    await setup({ info: sysInfo({ disk_writes: null, data_dir: { path: '/d', used_bytes: null } }) })
    await screen.findByText('/d')
    const row = (label: string) => screen.getByText(label).closest('div')!
    expect(within(row('日写入量（近 24 小时）')).getByText('未知')).toBeInTheDocument()
    expect(within(row('自启动以来写入')).getByText('未知')).toBeInTheDocument()
    expect(within(row('数据目录占用')).getByText('未知')).toBeInTheDocument()
    expect(screen.queryByText('0 B')).not.toBeInTheDocument()
  })

  it('加载失败显示错误并可重试', async () => {
    let fail = true
    await setup({ extra: (r) => (r.url === '/api/system' ? (fail ? apiError(500, 'internal') : json(200, sysInfo())) : undefined) })
    expect(await screen.findByText(/无法加载系统信息/)).toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getAllByRole('button', { name: '重试' })[0])
    expect((await screen.findAllByText('v0.1.0-test')).length).toBeGreaterThan(0)
  })

  it('息屏检查是占位（M1e 提供）', async () => {
    await setup()
    expect(await screen.findByText(/M1e 的 kiosk 部署里提供/)).toBeInTheDocument()
  })
})

describe('屏幕区', () => {
  const screenStatus = (over: Partial<ScreenStatus> = {}): ScreenStatus => ({
    state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' },
    viewport: { w: 1024, h: 600, dpr: 1 },
    coarse_pointer: false,
    current_screen: 'index',
    online: true,
    ...over,
  })
  const withStatus = (st: ScreenStatus | null) => ({
    extra: (r: Req) => (r.method === 'GET' && r.url === '/api/screen/status' ? (st ? json(200, st) : apiError(500, 'internal')) : undefined),
  })
  const panel = () => screen.getByRole('region', { name: '屏幕' })

  it('在线时显示分辨率、输入方式（含检测结果）、界面缩放与当前 screen', async () => {
    liveStore.applySnapshot({
      type: 'snapshot', build: 'b', role: 'admin', topics: [], server_time: new Date().toISOString(), instances: [],
      settings: { backup: { daily_at: '04:00', keep: 7 }, screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1.25 } },
      layout: { version: 2, source: 'edit', created_at: '2026-10-01T00:00:00Z', layout: { grid: { cols: 8, rows: 5 }, screens: [] }, broken: [] },
    } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
    await setup(withStatus(screenStatus()))
    const p = panel()
    expect(await within(p).findByText('1024×600')).toBeInTheDocument()
    expect(within(p).getByText('在线')).toBeInTheDocument()
    expect(within(p).getByText('自动（检测：无触摸）')).toBeInTheDocument()
    expect(within(p).getByText('1.25')).toBeInTheDocument()
    expect(within(p).getByText('8×5')).toBeInTheDocument()
    expect(within(p).getByText('index')).toBeInTheDocument()
    expect(within(p).getByRole('link', { name: '在屏幕管理里调整' })).toHaveAttribute('href', '/screens')
  })

  it('离线时不显示当前 screen（离线后服务端不清空），显示最近在线；从未连接与读取失败都不当作零', async () => {
    await setup(withStatus(screenStatus({ online: false, last_seen: '2026-10-01T08:00:00Z' })))
    const p = panel()
    expect(await within(p).findByText('离线')).toBeInTheDocument()
    expect(within(p).queryByText('index')).toBeNull()
    expect(within(p).getByText('最近在线')).toBeInTheDocument()
  })

  it('从未连接', async () => {
    await setup(withStatus(screenStatus({ online: false, viewport: undefined, coarse_pointer: undefined, current_screen: undefined })))
    const p = panel()
    expect(await within(p).findByText('从未连接')).toBeInTheDocument()
    expect(within(p).queryByText('0×0')).toBeNull()
  })

  it('状态读取失败给出提示', async () => {
    await setup(withStatus(null))
    expect(await within(panel()).findByText('屏幕状态暂时无法读取。')).toBeInTheDocument()
  })
})

describe('hub 日志与级别筛选', () => {
  it('默认取全部级别，新的在上，属性随行显示', async () => {
    const api = await setup()
    const box = await screen.findByRole('log', { name: 'hub 日志' })
    await waitFor(() => expect(within(box).getByText('备份失败 boom')).toBeInTheDocument())
    const rows = within(box).getAllByText(/服务已就绪|插件目录不可读|备份失败/)
    expect(rows.map((r) => r.textContent?.slice(0, 4))).toEqual(['备份失败', '插件目录', '服务已就'])
    expect(within(box).getByText('addr=:31415 mono=1.2s')).toBeInTheDocument()
    const req = api.calls.find((c) => c.url.startsWith('/api/system/logs'))!
    expect(req.url).toBe('/api/system/logs?limit=500')
  })

  it('点击级别按钮向服务端请求对应级别并只显示符合的日志', async () => {
    const api = await setup()
    const box = await screen.findByRole('log', { name: 'hub 日志' })
    await waitFor(() => expect(within(box).getByText('服务已就绪')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('radio', { name: 'warn' }))
    await waitFor(() => expect(within(box).queryByText('服务已就绪')).not.toBeInTheDocument())
    expect(within(box).getByText('插件目录不可读')).toBeInTheDocument()
    expect(within(box).getByText('备份失败 boom')).toBeInTheDocument()
    expect(api.calls.some((c) => c.url === '/api/system/logs?level=warn&limit=500')).toBe(true)
    await userEvent.click(screen.getByRole('radio', { name: 'error' }))
    await waitFor(() => expect(within(box).queryByText('插件目录不可读')).not.toBeInTheDocument())
    expect(within(box).getByText('备份失败 boom')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: '全部' }))
    await waitFor(() => expect(within(box).getByText('服务已就绪')).toBeInTheDocument())
  })

  it('没有符合条件的日志时给出提示；加载失败显示错误', async () => {
    await setup({ extra: (r) => (r.url.startsWith('/api/system/logs') ? json(200, { entries: [], capacity: 500 }) : undefined) })
    expect(await screen.findByText('暂无日志')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: 'error' }))
    expect(await screen.findByText('没有符合条件的日志')).toBeInTheDocument()
  })

  it('日志接口失败显示错误', async () => {
    await setup({ extra: (r) => (r.url.startsWith('/api/system/logs') ? apiError(500, 'internal') : undefined) })
    expect(await screen.findByText(/无法加载日志/)).toBeInTheDocument()
  })

  it('手动刷新重新请求', async () => {
    const api = await setup()
    await screen.findByRole('log', { name: 'hub 日志' })
    const before = api.calls.filter((c) => c.url.startsWith('/api/system/logs')).length
    await userEvent.click(screen.getByRole('button', { name: '刷新日志' }))
    await waitFor(() => expect(api.calls.filter((c) => c.url.startsWith('/api/system/logs')).length).toBe(before + 1))
  })
})

describe('插件与备份', () => {
  it('列出 exec 插件、加载问题与 builtin 汇总，并提示目录', async () => {
    await setup()
    expect(await screen.findByText('smart-disk')).toBeInTheDocument()
    expect(screen.getByText('builtin × 1')).toBeInTheDocument()
    expect(screen.getByText('plugin.yaml 第 3 行不合法')).toBeInTheDocument()
    expect(screen.getByText('与内置插件 demo 重名')).toBeInTheDocument()
    expect(screen.getByText('/var/lib/pimon/plugins')).toBeInTheDocument()
  })

  it('重新扫描：POST 后提示插件数', async () => {
    const api = await setup({ extra: (r) => (r.method === 'POST' && r.url.startsWith('/api/plugins/rescan') ? json(200, execPlugins) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '重新扫描插件目录' }))
    expect(await screen.findByText('扫描完成 · 2 个插件')).toBeInTheDocument()
    expect(api.calls.some((c) => c.method === 'POST' && c.url.startsWith('/api/plugins/rescan'))).toBe(true)
  })

  it('备份列表：类型标签、大小，下载前提示含密钥文件', async () => {
    await setup()
    expect(await screen.findByText('pimon-backup-20260928-040000-daily.tar.gz')).toBeInTheDocument()
    expect(screen.getByText('升级前')).toBeInTheDocument()
    expect(screen.getByText('4 MB')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '下载 pimon-backup-20260928-040000-daily.tar.gz' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText(/密钥文件/)).toBeInTheDocument()
    expect(within(dlg).getByRole('link', { name: '我已了解，下载' })).toHaveAttribute('href', '/api/backups/pimon-backup-20260928-040000-daily.tar.gz')
  })

  it('立即备份：POST 后刷新列表', async () => {
    const created: BackupInfo = { name: 'pimon-backup-20260928-050000-manual.tar.gz', reason: 'manual', created_at: '2026-09-28T05:00:00Z', size: 1000 }
    const api = await setup({ extra: (r) => (r.method === 'POST' && r.url === '/api/backups' ? json(201, created) : undefined) })
    await userEvent.click(await screen.findByRole('button', { name: '立即备份' }))
    expect(await screen.findByText(/已生成备份/)).toBeInTheDocument()
    expect(api.calls.filter((c) => c.method === 'GET' && c.url === '/api/backups').length).toBeGreaterThanOrEqual(2)
  })
})
