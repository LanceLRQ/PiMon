import { Download } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { formatBytes } from '@/lib/format'
import { formatDateTime, parseTime } from '@/lib/time'
import { BackupDownloadDialog } from '@/pages/settings/BackupDownloadDialog'
import { selectSettings, useLiveStore } from '@/store/live-store'
import type { BackupInfo } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { Section } from '@/ui/section'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/ui/table'
import { useToast } from '@/ui/toast'

export function BackupsPanel() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const settings = useLiveStore(selectSettings)
  const [list, setList] = useState<BackupInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [downloading, setDownloading] = useState<BackupInfo | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setList(await http.get<BackupInfo[]>('/api/backups'))
      setError(null)
    } catch (e) {
      setError(translateErrorValue(i18n, e))
    }
  }, [i18n])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次备份列表
    void load()
  }, [load])

  const sorted = useMemo(() => (list ? [...list].sort((a, b) => (parseTime(b.created_at) ?? 0) - (parseTime(a.created_at) ?? 0)) : null), [list])

  async function backupNow() {
    setBusy(true)
    try {
      const info = await http.post<BackupInfo>('/api/backups')
      toast.show(t('system.backups.created', { name: info.name }))
      await load()
    } catch (e) {
      toast.show(translateErrorValue(i18n, e), 'warn')
    } finally {
      setBusy(false)
    }
  }

  const meta = (
    <span>
      {sorted ? t('system.backups.meta', { count: sorted.length, time: settings?.backup.daily_at ?? '—', keep: settings?.backup.keep ?? '—' }) : null}
      {' · '}
      <Link to="/settings#sec-backup" className="underline underline-offset-2">
        {t('system.backups.settings')}
      </Link>
    </span>
  )

  return (
    <Section no="06.7" title={t('system.backups.title')} meta={meta}>
      <div className="p-3">
        {error ? (
          <Note tone="crit" role="alert">
            {t('common.withDetail', { summary: t('system.backups.loadFailed'), detail: error })}{' '}
            <button type="button" className="underline underline-offset-2" onClick={() => void load()}>
              {t('common.retry')}
            </button>
          </Note>
        ) : sorted === null ? (
          <div className="py-4 text-center text-[13px] text-muted-foreground">{t('common.loading')}</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('system.backups.cols.time')}</TableHead>
                <TableHead className="mobile:hidden">{t('system.backups.cols.file')}</TableHead>
                <TableHead>{t('system.backups.cols.size')}</TableHead>
                <TableHead>{t('system.backups.cols.type')}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="py-4 text-center text-muted-foreground">
                    {t('system.backups.empty')}
                  </TableCell>
                </TableRow>
              )}
              {sorted.map((b) => (
                <TableRow key={b.name}>
                  <TableCell mono className="text-[12.5px]">{formatDateTime(parseTime(b.created_at) ?? 0, i18n.language)}</TableCell>
                  <TableCell mono className="text-[12px] text-muted-foreground mobile:hidden">{b.name}</TableCell>
                  <TableCell mono className="text-[12.5px]">{formatBytes(b.size, i18n.language)}</TableCell>
                  <TableCell>
                    <span className="rounded-[2px] border border-border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                      {t(`settings.reason.${b.reason}`, { defaultValue: b.reason })}
                    </span>
                  </TableCell>
                  <TableCell className="text-right">
                    <Button size="xs" variant="outline" className="rounded-[2px]" aria-label={`${t('common.download')} ${b.name}`} onClick={() => setDownloading(b)}>
                      <Download /> {t('common.download')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        <div className="mt-3 flex justify-end">
          <Button size="sm" variant="outline" className="rounded-[2px]" disabled={busy} onClick={() => void backupNow()}>
            {busy ? t('system.backups.backingUp') : t('system.backups.backupNow')}
          </Button>
        </div>
      </div>
      <BackupDownloadDialog backup={downloading} onClose={() => setDownloading(null)} />
    </Section>
  )
}
