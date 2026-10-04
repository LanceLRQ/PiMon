import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, json, mockApi, renderWithApp, type Req } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { BackupRemote, BackupUpload } from '@/types/generated'
import { BackupsPanel } from './BackupsPanel'

beforeEach(() => liveStore.reset())
afterEach(() => vi.unstubAllGlobals())

const unset = { set: false }
function remote(over: Partial<BackupRemote> = {}): BackupRemote {
  return {
    enabled: true,
    type: 'smb',
    smb_host: 'nas.lan',
    smb_port: 445,
    smb_share: 'backup',
    smb_dir: 'pimon',
    smb_username: 'u',
    smb_password: { set: true },
    smb_domain: '',
    webdav_url: '',
    webdav_dir: '',
    webdav_username: '',
    webdav_password: unset,
    passphrase: { set: true },
    keep: 30,
    passphrase_changed: false,
    ...over,
  }
}

function upload(over: Partial<BackupUpload> = {}): BackupUpload {
  return {
    id: 1,
    name: 'pimon-backup-20260928-040000-daily.tar.gz.age',
    target_type: 'smb',
    started_at: '2026-09-28T04:00:10Z',
    finished_at: '2026-09-28T04:01:00Z',
    size: 4_200_000,
    success: true,
    reason: '',
    reason_code: '',
    reason_detail: '',
    ...over,
  }
}

interface Opts {
  remote?: BackupRemote | Response
  uploads?: BackupUpload[]
  // 后端给出的最近一次结果；缺省时按后端口径从 uploads 推算（跳过取消记录）
  lastResult?: BackupUpload | null
  extra?: (req: Req) => Response | undefined | Promise<Response | undefined>
}

const lastOf = (list: BackupUpload[]) => list.find((u) => !(u.reason_code === 'canceled' && !u.success)) ?? null

async function setup({ remote: r = remote(), uploads = [], lastResult, extra }: Opts = {}) {
  let current = uploads
  const api = mockApi((req) => {
    const x = extra?.(req)
    if (x) return x
    if (req.method === 'GET' && req.url === '/api/backups') return json(200, [])
    if (req.method === 'GET' && req.url === '/api/backups/remote') return r instanceof Response ? r : json(200, r)
    if (req.method === 'GET' && req.url.startsWith('/api/backups/remote/uploads')) return json(200, { uploads: current, last_result: lastResult === undefined ? lastOf(current) : lastResult })
    return undefined
  })
  await renderWithApp(<BackupsPanel />)
  return { api, setUploads: (u: BackupUpload[]) => (current = u) }
}

describe('异地备份小节', () => {
  it('最近结果取自响应的 last_result，而不是在当前页记录里推导', async () => {
    const canceled = (id: number) => upload({ id, success: false, reason: 'canceled:', reason_code: 'canceled' })
    await setup({ uploads: Array.from({ length: 10 }, (_, i) => canceled(20 - i)), lastResult: upload({ id: 5 }) })
    expect(within(await screen.findByTestId('remote-last')).getByText(/^成功 · /)).toBeInTheDocument()
  })

  it('最近结果：last_result 为空时显示尚无上传记录', async () => {
    await setup({ uploads: [upload({ id: 2, success: false, reason: 'canceled:', reason_code: 'canceled' })], lastResult: null })
    expect(await screen.findByText('尚无上传记录')).toBeInTheDocument()
  })

  it('最近结果跳过取消记录；全是取消记录时显示尚无结果', async () => {
    await setup({ uploads: [upload({ id: 2, success: false, reason: 'canceled:', reason_code: 'canceled' }), upload({ id: 1 })] })
    expect(within(await screen.findByTestId('remote-last')).getByText(/^成功 · /)).toBeInTheDocument()
    expect(within(screen.getByTestId('remote-uploads')).getAllByTestId('remote-upload-row')).toHaveLength(2)
    cleanup()
    await setup({ uploads: [upload({ id: 2, success: false, reason: 'canceled:', reason_code: 'canceled' })] })
    expect(await screen.findByText('尚无上传记录')).toBeInTheDocument()
  })

  it('无业务码的 500 按普通错误提示，不提示可能仍在后台', async () => {
    await setup({ extra: (r) => (r.method === 'POST' && r.url === '/api/backups/remote/upload' ? new Response('boom', { status: 500 }) : undefined) })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '立即上传' })).toBeEnabled())
    expect(screen.queryByText(/可能仍在后台进行/)).not.toBeInTheDocument()
  })

  it('无业务码的 502 提示可能仍在后台', async () => {
    await setup({ extra: (r) => (r.method === 'POST' && r.url === '/api/backups/remote/upload' ? new Response('bad gateway', { status: 502 }) : undefined) })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText(/可能仍在后台进行/)).toBeInTheDocument()
  })

  it('上传未完成时卸载：之后完成也不再刷新记录', async () => {
    let release!: (r: Response) => void
    const pending = new Promise<Response>((res) => (release = res))
    const ctx = await setup({ extra: (r) => (r.method === 'POST' && r.url === '/api/backups/remote/upload' ? pending : undefined) })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    const before = ctx.api.calls.filter((c) => c.url.startsWith('/api/backups/remote/uploads')).length
    cleanup()
    release(json(200, upload({ id: 9 })))
    await new Promise((r) => setTimeout(r, 30))
    expect(ctx.api.calls.filter((c) => c.url.startsWith('/api/backups/remote/uploads')).length).toBe(before)
  })

  it('已启用：显示目标与最近一次结果', async () => {
    await setup({ uploads: [upload()] })
    expect(await screen.findByText('smb://nas.lan:445/backup/pimon')).toBeInTheDocument()
    const last = screen.getByTestId('remote-last')
    expect(within(last).getByText(/^成功 · /)).toBeInTheDocument()
  })

  it('WebDAV 目标显示地址与目录', async () => {
    await setup({ remote: remote({ type: 'webdav', webdav_url: 'https://dav.lan/dav', webdav_dir: 'pimon' }) })
    expect(await screen.findByText('https://dav.lan/dav/pimon')).toBeInTheDocument()
  })

  it('上传记录：成功、按机器码翻译失败、取消用中性样式、未知码回退说明', async () => {
    await setup({
      uploads: [
        upload({ id: 4, success: false, reason: 'auth:bad', reason_code: 'auth', reason_detail: 'bad' }),
        upload({ id: 3, success: false, reason: 'canceled:', reason_code: 'canceled' }),
        upload({ id: 2, success: false, reason: 'weird:磁盘炸了', reason_code: 'weird', reason_detail: '磁盘炸了' }),
        upload({ id: 1 }),
      ],
    })
    const list = await screen.findByTestId('remote-uploads')
    expect(within(list).getByText('用户名或密码错误')).toBeInTheDocument()
    expect(within(list).getByText('中枢关停，上传已取消')).toBeInTheDocument()
    expect(within(list).getByText(/磁盘炸了/)).toBeInTheDocument()
    const rows = within(list).getAllByTestId('remote-upload-row')
    expect(rows[0]).toHaveAttribute('data-tone', 'crit')
    expect(rows[1]).toHaveAttribute('data-tone', 'neutral')
    expect(rows[3]).toHaveAttribute('data-tone', 'ok')
    expect(within(rows[1]).getByText('已取消')).toBeInTheDocument()
  })

  it('本地读取出错：记录与上传失败提示都给出本地原因，不说远端', async () => {
    await setup({
      uploads: [upload({ id: 1, success: false, reason: 'local_read:读取本地备份: boom', reason_code: 'local_read', reason_detail: '读取本地备份: boom' })],
      extra: (r) => (r.method === 'POST' && r.url === '/api/backups/remote/upload' ? apiError(502, 'backup.remote_failed', { reason: 'local_read', message: 'boom' }) : undefined),
    })
    expect(await within(await screen.findByTestId('remote-uploads')).findByText('读取本地备份文件时出错')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText(/远端备份失败.*读取本地备份文件时出错/)).toBeInTheDocument()
  })

  it('立即上传成功后刷新记录', async () => {
    const ctx = await setup({ uploads: [], extra: (r) => (r.method === 'POST' && r.url === '/api/backups/remote/upload' ? json(200, upload({ id: 9 })) : undefined) })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    ctx.setUploads([upload({ id: 9 })])
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText(/已上传/)).toBeInTheDocument()
    await waitFor(() => expect(within(screen.getByTestId('remote-uploads')).getAllByTestId('remote-upload-row')).toHaveLength(1))
  })

  it('立即上传：409 无本地备份与 502 remote_failed 按错误码翻译', async () => {
    let n = 0
    await setup({
      extra: (r) => {
        if (r.method === 'POST' && r.url === '/api/backups/remote/upload') {
          n++
          return n === 1 ? apiError(409, 'backup.no_local') : apiError(502, 'backup.remote_failed', { reason: 'auth', message: 'x' })
        }
        return undefined
      },
    })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText('还没有可上传的本地备份，请先创建一份备份')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText(/远端备份失败.*用户名或密码错误/)).toBeInTheDocument()
  })

  it('立即上传失败时在分类文案后附上已脱敏的 details.message', async () => {
    await setup({
      extra: (r) => (r.method === 'POST' && r.url === '/api/backups/remote/upload' ? apiError(502, 'backup.remote_failed', { reason: 'connect', message: 'TLS 证书校验失败' }) : undefined),
    })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText(/TLS 证书校验失败/)).toBeInTheDocument()
  })

  it('网络错误：提示可能仍在后台进行并刷新记录', async () => {
    const ctx = await setup({
      extra: (r) => {
        if (r.method === 'POST' && r.url === '/api/backups/remote/upload') throw new TypeError('Failed to fetch')
        return undefined
      },
    })
    await screen.findByText('smb://nas.lan:445/backup/pimon')
    await userEvent.click(screen.getByRole('button', { name: '立即上传' }))
    expect(await screen.findByText(/可能仍在后台进行/)).toBeInTheDocument()
    expect(ctx.api.calls.filter((c) => c.url.startsWith('/api/backups/remote/uploads')).length).toBeGreaterThanOrEqual(2)
  })

  it('未启用：按钮禁用并给出设置页链接', async () => {
    await setup({ remote: remote({ enabled: false }) })
    expect(await screen.findByText('异地备份未启用')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '立即上传' })).toBeDisabled()
    expect(screen.getByRole('link', { name: '前往设置启用' })).toHaveAttribute('href', '/settings#sec-backup')
  })

  it('读取失败：降级显示错误提示，不影响本地备份列表', async () => {
    await setup({ remote: apiError(404, 'not_found') })
    expect(await screen.findByText(/无法加载异地备份状态/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '立即备份' })).toBeInTheDocument()
  })
})
