import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import { formatBytes } from '@/lib/format'
import { formatDateTime, parseTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import type { BackupRemote, BackupUpload, BackupUploadList } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/ui/table'
import { useToast } from '@/ui/toast'

const uploadsLimit = 10

// 无业务错误码的网关类状态：反代超时或后端暂不可达时，上传可能仍在后台进行
const gatewayStatuses = new Set([408, 502, 503, 504])

type Tone = 'ok' | 'crit' | 'neutral'

// 目标的可读地址：只含类型与位置，不含任何密钥
function targetText(r: BackupRemote): string {
  const join = (base: string, dir: string) => [base.replace(/\/+$/, ''), dir.replace(/^\/+|\/+$/g, '')].filter(Boolean).join('/')
  if (r.type === 'webdav') return join(r.webdav_url, r.webdav_dir)
  return `smb://${join(`${r.smb_host}:${r.smb_port}`, join(r.smb_share, r.smb_dir))}`
}

// canceled 是 hub 重启或关停导致的中断，不算失败
function toneOf(u: BackupUpload): Tone {
  if (u.success) return 'ok'
  return u.reason_code === 'canceled' ? 'neutral' : 'crit'
}

const toneClass: Record<Tone, string> = {
  ok: 'text-foreground',
  crit: 'text-destructive',
  neutral: 'text-muted-foreground',
}

// 异地备份小节：目标、最近一次结果、立即上传与最近上传记录
export function RemoteBackupSection() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [remote, setRemote] = useState<BackupRemote | null>(null)
  const [uploads, setUploads] = useState<BackupUpload[]>([])
  // 后端按 hub-self 同口径给出（跳过取消记录），不依赖当前页记录
  const [last, setLast] = useState<BackupUpload | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // 卸载后不再刷新、不 setState、不弹提示
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const loadUploads = useCallback(async () => {
    try {
      const res = await http.get<BackupUploadList>('/api/backups/remote/uploads', { query: { limit: uploadsLimit } })
      if (mounted.current) {
        setUploads(res.uploads)
        setLast(res.last_result ?? null)
      }
    } catch (e) {
      if (mounted.current) toast.show(translateErrorValue(i18n, e), 'warn')
    }
  }, [i18n, toast])

  const load = useCallback(async () => {
    try {
      setRemote(await http.get<BackupRemote>('/api/backups/remote'))
      setError(null)
    } catch (e) {
      setError(translateErrorValue(i18n, e))
      return
    }
    await loadUploads()
  }, [i18n, loadUploads])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次异地备份状态
    void load()
  }, [load])

  const reasonText = useCallback(
    (u: BackupUpload): string => {
      const key = `backup.upload_reason.${u.reason_code}`
      if (u.reason_code && i18n.exists(key)) return t(key)
      return u.reason_detail || (u.success ? '' : t('system.backups.remote.unknownReason'))
    },
    [i18n, t],
  )

  async function uploadNow() {
    setBusy(true)
    try {
      const u = await http.post<BackupUpload>('/api/backups/remote/upload')
      if (mounted.current) toast.show(t('system.backups.remote.uploaded', { name: u.name }))
    } catch (e) {
      // 网络中断、超时或无业务错误码的网关错误时上传可能仍在后台进行，以上传记录为准
      const maybeRunning = isApiError(e) && (e.status === 0 || (gatewayStatuses.has(e.status) && e.code === `http.${e.status}`))
      if (mounted.current) toast.show(maybeRunning ? t('system.backups.remote.maybeRunning') : uploadErrorText(e), 'warn')
    } finally {
      if (mounted.current) {
        await loadUploads()
        if (mounted.current) setBusy(false)
      }
    }
  }

  function uploadErrorText(e: unknown): string {
    const base = translateErrorValue(i18n, e)
    const reason = isApiError(e) && e.code === 'backup.remote_failed' ? e.details.reason : undefined
    const key = typeof reason === 'string' ? `errors.backup.remote_reason.${reason}` : ''
    const text = key && i18n.exists(key) ? t('common.withDetail', { summary: base, detail: t(key) }) : base
    // 后端已脱敏的原因说明（如证书校验失败），作为分类文案的补充
    const message = isApiError(e) && typeof e.details.message === 'string' ? e.details.message : ''
    return message ? t('common.withDetail', { summary: text, detail: message }) : text
  }

  const resultLabel = (u: BackupUpload) => t(u.success ? 'system.backups.remote.ok' : toneOf(u) === 'neutral' ? 'system.backups.remote.canceled' : 'system.backups.remote.failed')
  const timeOf = (u: BackupUpload) => formatDateTime(parseTime(u.finished_at) ?? parseTime(u.started_at) ?? 0, i18n.language)

  return (
    <div className="mt-4 border-t border-border pt-3" data-testid="remote-backup">
      <div className="mb-2 text-[13px] font-medium">{t('system.backups.remote.title')}</div>
      {error ? (
        <Note tone="crit" role="alert">
          {t('common.withDetail', { summary: t('system.backups.remote.loadFailed'), detail: error })}{' '}
          <button type="button" className="underline underline-offset-2" onClick={() => void load()}>
            {t('common.retry')}
          </button>
        </Note>
      ) : remote === null ? (
        <div className="py-2 text-center text-[13px] text-muted-foreground">{t('common.loading')}</div>
      ) : (
        <>
          {remote.enabled ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[12.5px]">
              <dt className="text-muted-foreground">{t('system.backups.remote.target')}</dt>
              <dd className="font-mono break-all">
                <span className="mr-2 rounded-[2px] border border-border px-1.5 py-0.5 text-[11px] text-muted-foreground">{remote.type === 'webdav' ? 'WebDAV' : 'SMB'}</span>
                {targetText(remote)}
              </dd>
              <dt className="text-muted-foreground">{t('system.backups.remote.lastResult')}</dt>
              <dd data-testid="remote-last">
                {last ? (
                  <span className={cn(toneClass[toneOf(last)])}>
                    {resultLabel(last)} · {timeOf(last)}
                  </span>
                ) : (
                  <span className="text-muted-foreground">{t('system.backups.remote.never')}</span>
                )}
              </dd>
            </dl>
          ) : (
            <Note tone="info">
              {t('system.backups.remote.disabled')}{' '}
              <Link to="/settings#sec-backup" className="underline underline-offset-2">
                {t('system.backups.remote.goSettings')}
              </Link>
            </Note>
          )}
          <div className="mt-3 mb-1 text-[12px] text-muted-foreground">{t('system.backups.remote.uploadsTitle')}</div>
          <Table data-testid="remote-uploads">
            <TableHeader>
              <TableRow>
                <TableHead>{t('system.backups.remote.cols.time')}</TableHead>
                <TableHead className="mobile:hidden">{t('system.backups.remote.cols.file')}</TableHead>
                <TableHead>{t('system.backups.remote.cols.size')}</TableHead>
                <TableHead>{t('system.backups.remote.cols.result')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {uploads.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="py-3 text-center text-muted-foreground">
                    {t('system.backups.remote.noUploads')}
                  </TableCell>
                </TableRow>
              )}
              {uploads.map((u) => {
                const tone = toneOf(u)
                const reason = reasonText(u)
                const detail = u.reason_detail && u.reason_detail !== reason ? u.reason_detail : ''
                return (
                  <TableRow key={u.id} data-testid="remote-upload-row" data-tone={tone}>
                    <TableCell mono className="text-[12.5px]">{timeOf(u)}</TableCell>
                    <TableCell mono className="text-[12px] text-muted-foreground mobile:hidden">{u.name}</TableCell>
                    <TableCell mono className="text-[12.5px]">{formatBytes(u.size, i18n.language)}</TableCell>
                    <TableCell className={cn('text-[12.5px]', toneClass[tone])}>
                      <span>{resultLabel(u)}</span>
                      {reason && <span className="ml-2">{reason}</span>}
                      {detail && <span className="ml-2 font-mono text-[11px] text-muted-foreground">{detail}</span>}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </>
      )}
      <div className="mt-3 flex justify-end">
        <Button size="sm" variant="outline" className="rounded-[2px]" disabled={busy || !remote?.enabled} onClick={() => void uploadNow()}>
          {busy ? t('system.backups.remote.uploading') : t('system.backups.remote.uploadNow')}
        </Button>
      </div>
    </div>
  )
}
