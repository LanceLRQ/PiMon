import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { formatBytes, formatUptime } from '@/lib/format'
import { formatDateTime, parseTime, useNow } from '@/lib/time'
import type { SystemInfo } from '@/types/generated'
import { Note } from '@/ui/note'
import { PageHeader } from '@/ui/page-header'
import { Section } from '@/ui/section'
import { BackupsPanel } from './BackupsPanel'
import { LogPanel } from './LogPanel'
import { PluginsPanel } from './PluginsPanel'
import { useSystemInfo } from './use-system'

function KV({ rows }: { rows: { k: string; v: ReactNode }[] }) {
  return (
    <dl>
      {rows.map((r) => (
        <div key={r.k} className="flex items-start justify-between gap-3 border-t border-border px-4 py-2 text-[13px] first:border-t-0">
          <dt className="shrink-0 text-muted-foreground">{r.k}</dt>
          <dd className="min-w-0 text-right font-mono text-[12.5px] break-words">{r.v}</dd>
        </div>
      ))}
    </dl>
  )
}

function Placeholder({ text }: { text: string }) {
  return <div className="px-4 py-5 text-[12.5px] leading-[1.6] text-muted-foreground">{text}</div>
}

export function SystemPage() {
  const { t, i18n } = useTranslation()
  const { info, error, reload } = useSystemInfo()
  const now = useNow()
  const unknown = <span className="font-sans text-muted-foreground">{t('items.unknown')}</span>

  const startedMs = info ? parseTime(info.started_at) : null
  const uptime = startedMs !== null ? formatUptime(t, (now - startedMs) / 1000) : null

  const readout = info ? (
    <div className="flex items-center gap-4 font-mono text-[12px] text-muted-foreground mobile:hidden">
      <span>{t('system.readout.hub', { version: info.version })}</span>
      <span>{t('system.readout.uptime', { uptime: uptime ?? '' })}</span>
    </div>
  ) : undefined

  return (
    <>
      <PageHeader no="06" title={t('pages.system')} sub={t('system.sub')} actions={readout} />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        {error && (
          <Note tone="crit" role="alert">
            {t('common.withDetail', { summary: t('system.loadFailed'), detail: error })}{' '}
            <button type="button" className="underline underline-offset-2" onClick={() => void reload()}>
              {t('common.retry')}
            </button>
          </Note>
        )}

        <div className="grid grid-cols-3 gap-px overflow-hidden rounded-[2px] border border-line-strong bg-line-strong mobile:grid-cols-1 [&>section]:rounded-none [&>section]:border-0">
          <Section no="06.1" title={t('system.version.title')} meta={t('system.version.meta')}>
            {info ? (
              <>
                <div className="flex items-baseline gap-2.5 border-b border-border px-4 pt-3.5 pb-3">
                  <span className="font-mono text-[28px] leading-none break-all">{info.version}</span>
                </div>
                <KV
                  rows={[
                    { k: t('system.version.uptime'), v: uptime },
                    { k: t('system.version.go'), v: `${info.go_version} ${info.os}/${info.arch}` },
                    { k: t('system.version.startedAt'), v: startedMs !== null ? formatDateTime(startedMs, i18n.language) : unknown },
                  ]}
                />
              </>
            ) : (
              <Placeholder text={t('common.loading')} />
            )}
          </Section>

          <Section no="06.2" title={t('system.resources.title')} meta={t('system.resources.meta')}>
            {info ? <Resources info={info} unknown={unknown} /> : <Placeholder text={t('common.loading')} />}
          </Section>

          <Section no="06.3" title={t('system.screen.title')} meta={t('system.screen.meta')}>
            <Placeholder text={t('system.screen.placeholder')} />
          </Section>
        </div>

        <div className="grid grid-cols-2 gap-4 mobile:grid-cols-1">
          <Section no="06.4" title={t('system.idle.title')} meta={t('system.idle.meta')}>
            <Placeholder text={t('system.idle.placeholder')} />
          </Section>
          <PluginsPanel />
        </div>

        <LogPanel />
        <BackupsPanel />
      </div>
    </>
  )
}

function Resources({ info, unknown }: { info: SystemInfo; unknown: ReactNode }) {
  const { t, i18n } = useTranslation()
  const fmt = (n: number) => formatBytes(n, i18n.language)
  const writes = info.disk_writes
  return (
    <>
      <KV
        rows={[
          { k: t('system.resources.memory'), v: t('system.resources.memoryValue', { sys: fmt(info.memory.sys_bytes), heap: fmt(info.memory.heap_bytes) }) },
          { k: t('system.resources.dataDir'), v: info.data_dir.path },
          { k: t('system.resources.dataUsed'), v: info.data_dir.used_bytes === null ? unknown : fmt(info.data_dir.used_bytes) },
          {
            k: t('system.resources.writes24h'),
            v: writes === null ? <span title={t('system.resources.unsupported')}>{unknown}</span> : fmt(writes.last_24h_bytes),
          },
          { k: t('system.resources.writesSince'), v: writes === null ? unknown : fmt(writes.since_start_bytes) },
        ]}
      />
      {writes?.last_24h_estimated && <div className="border-t border-border px-4 py-1.5 text-[11.5px] text-muted-foreground">{t('system.resources.writesEstimated')}</div>}
    </>
  )
}
