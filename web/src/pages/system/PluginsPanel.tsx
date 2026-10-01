import { Folder, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { translateErrorValue } from '@/i18n/errors'
import { usePlugins } from '@/pages/instances/use-plugins'
import { Note } from '@/ui/note'
import { Button } from '@/ui/button'
import { Section } from '@/ui/section'
import { StatusShape } from '@/ui/status-shape'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/ui/table'
import { useToast } from '@/ui/toast'

const tag = 'rounded-[2px] border px-1.5 py-0.5 font-mono text-[11px]'

export function PluginsPanel() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const { list, error, reload, rescan } = usePlugins()
  const [scanning, setScanning] = useState(false)

  async function scan() {
    setScanning(true)
    try {
      const next = await rescan()
      toast.show(t('system.plugins.rescanned', { total: next.plugins.length }))
    } catch (e) {
      toast.show(t('common.withDetail', { summary: t('system.plugins.scanFailed'), detail: translateErrorValue(i18n, e) }), 'warn')
    } finally {
      setScanning(false)
    }
  }

  const plugins = list?.plugins ?? []
  const exec = plugins.filter((p) => p.origin === 'exec')
  const builtin = plugins.length - exec.length
  const issues = [...(list?.errors ?? []), ...(list?.conflicts ?? [])]
  const conflictCount = list?.conflicts.length ?? 0
  const errorCount = list?.errors.length ?? 0

  return (
    <Section
      no="06.5"
      title={t('system.plugins.title')}
      meta={list ? t('system.plugins.meta', { total: plugins.length, builtin, exec: exec.length }) : undefined}
    >
      {error && !list ? (
        <div className="p-3">
          <Note tone="crit" role="alert">
            {t('common.withDetail', { summary: t('system.plugins.loadFailed'), detail: translateErrorValue(i18n, error) })}{' '}
            <button type="button" className="underline underline-offset-2" onClick={() => void reload()}>
              {t('common.retry')}
            </button>
          </Note>
        </div>
      ) : !list ? (
        <div className="px-4 py-6 text-center text-[13px] text-muted-foreground">{t('common.loading')}</div>
      ) : (
        <div className="p-3">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('system.plugins.cols.plugin')}</TableHead>
                <TableHead>{t('system.plugins.cols.kind')}</TableHead>
                <TableHead>{t('system.plugins.cols.source')}</TableHead>
                <TableHead>{t('system.plugins.cols.status')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {exec.map((p) => (
                <TableRow key={p.id}>
                  <TableCell mono className="text-[12.5px]">{p.id}</TableCell>
                  <TableCell>
                    <span className={`${tag} border-signal text-signal-text`}>exec</span>
                  </TableCell>
                  <TableCell mono className="text-[12px] text-muted-foreground">{`${p.name} · v${p.version}`}</TableCell>
                  <TableCell>
                    <span className="inline-flex items-center gap-1.5">
                      <StatusShape state="ok" size={12} />
                      {t('system.plugins.loaded')}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
              {issues.map((is, i) => (
                <TableRow key={`${is.dir}-${i}`}>
                  <TableCell mono className="text-[12.5px]">{is.id || is.dir}</TableCell>
                  <TableCell>
                    <span className={`${tag} border-signal text-signal-text`}>exec</span>
                  </TableCell>
                  <TableCell className="max-w-[260px] truncate text-[12px] text-muted-foreground" title={is.message}>
                    {is.message}
                  </TableCell>
                  <TableCell>
                    <span className="inline-flex items-center gap-1.5">
                      <StatusShape state={is.kind === 'conflict' ? 'warning' : 'error'} size={12} />
                      {is.kind === 'conflict' ? t('system.plugins.conflict') : t('system.plugins.error')}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
              <TableRow>
                <TableCell mono className="text-[12.5px] text-muted-foreground">{t('system.plugins.builtinRow', { count: builtin })}</TableCell>
                <TableCell>
                  <span className={`${tag} border-border text-muted-foreground`}>builtin</span>
                </TableCell>
                <TableCell className="text-[12px] text-muted-foreground">{t('system.plugins.builtinSource')}</TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5">
                    <StatusShape state="ok" size={12} />
                    {t('system.plugins.loaded')}
                  </span>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
          {(errorCount > 0 || conflictCount > 0) && (
            <div className="mt-2 text-[12px] text-status-warn">{t('system.plugins.issues', { errors: errorCount, conflicts: conflictCount })}</div>
          )}
          <div className="mt-3 flex flex-wrap items-center gap-2 text-[12.5px] text-muted-foreground">
            <Folder size={15} className="shrink-0" />
            <span className="min-w-0 break-all">
              {t('system.plugins.dir')}：<code className="font-mono text-[12px] text-foreground">{list.plugin_dir}</code>
            </span>
            <Button size="xs" variant="outline" className="ml-auto rounded-[2px]" disabled={scanning} onClick={() => void scan()}>
              <RefreshCw /> {scanning ? t('system.plugins.rescanning') : t('system.plugins.rescan')}
            </Button>
          </div>
        </div>
      )}
    </Section>
  )
}
