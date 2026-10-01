import { Info, Lock, Play, Plus, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { formatDateTime } from '@/lib/time'
import { useIsMobile } from '@/lib/use-mobile'
import { cn } from '@/lib/utils'
import type { Proxy, ProxyTestResult } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { PageHeader } from '@/ui/page-header'
import { Section } from '@/ui/section'
import { StatusShape } from '@/ui/status-shape'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/ui/table'
import { useToast } from '@/ui/toast'
import { DeleteProxyDialog } from './DeleteProxyDialog'
import { remoteDnsApplies, type Scheme } from './model'
import { ProxyDrawer } from './ProxyDrawer'

// 最近一次测试结果只保存在本页的内存里，不入库
interface LastTest {
  result: ProxyTestResult | null
  error?: string
  at: number
}

type TestState = LastTest | 'running'

const locationKeys = new Set(['hub', 'lan', 'any'])

export function ProxiesPage() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [proxies, setProxies] = useState<Proxy[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [target, setTarget] = useState<Proxy | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Proxy | null>(null)
  const [tests, setTests] = useState<Record<string, TestState>>({})
  const mobile = useIsMobile()

  const load = useCallback(async () => {
    try {
      setProxies(await http.get<Proxy[]>('/api/proxies'))
      setLoadError(null)
    } catch (e) {
      setLoadError(translateErrorValue(i18n, e))
    }
  }, [i18n])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次远端列表
    void load()
  }, [load])

  function recordTest(p: Proxy, result: ProxyTestResult) {
    setTests((m) => ({ ...m, [p.id]: { result, at: Date.now() } }))
  }

  async function runTest(p: Proxy) {
    if (tests[p.id] === 'running') return
    setTests((m) => ({ ...m, [p.id]: 'running' }))
    try {
      const result = await http.post<ProxyTestResult>(`/api/proxies/${p.id}/test`, undefined, { timeoutMs: 20_000 })
      recordTest(p, result)
      toast.show(
        result.ok
          ? t('proxies.test.toastOk', { name: p.name, status: result.status, ms: result.latency_ms })
          : t('proxies.test.toastFail', { name: p.name, error: result.error || t('proxies.test.failed') }),
        result.ok ? 'ok' : 'warn',
      )
    } catch (e) {
      const error = translateErrorValue(i18n, e)
      setTests((m) => ({ ...m, [p.id]: { result: null, error, at: Date.now() } }))
      toast.show(t('proxies.test.toastFail', { name: p.name, error }), 'warn')
    }
  }

  function onSaved(saved: Proxy, created: boolean) {
    setProxies((list) => {
      const rest = (list ?? []).filter((x) => x.id !== saved.id)
      return [...rest, saved].sort((a, b) => a.name.localeCompare(b.name))
    })
    setTarget(null)
    toast.show(t(created ? 'proxies.drawer.created' : 'proxies.drawer.saved', { name: saved.name }))
  }

  function onDeleted(p: Proxy) {
    setProxies((list) => (list ?? []).filter((x) => x.id !== p.id))
    setTests((m) => {
      const n = { ...m }
      delete n[p.id]
      return n
    })
    setDeleting(null)
    setTarget(null)
    toast.show(t('proxies.deleteDialog.deleted', { name: p.name }))
  }

  const count = proxies?.length ?? 0
  return (
    <>
      <PageHeader
        no="04"
        title={t('pages.proxies')}
        sub={t('proxies.sub')}
        actions={
          <Button size="sm" className="rounded-[2px]" onClick={() => setTarget('new')}>
            <Plus /> {t('proxies.add')}
          </Button>
        }
      />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        <Section no="04.1" title={t('proxies.list.title')} meta={proxies ? t('proxies.list.meta', { count }) : undefined}>
          <div className="p-3">
            {loadError ? (
              <Note tone="crit" role="alert">
                {t('common.withDetail', { summary: t('proxies.loadFailed'), detail: loadError })}{' '}
                <button type="button" className="underline underline-offset-2" onClick={() => void load()}>
                  {t('common.retry')}
                </button>
              </Note>
            ) : proxies === null ? (
              <div className="py-6 text-center text-[13px] text-muted-foreground">{t('common.loading')}</div>
            ) : proxies.length === 0 ? (
              <div className="py-8 text-center">
                <div className="text-[14px] font-medium">{t('proxies.list.empty')}</div>
                <div className="mx-auto mt-1 max-w-md text-[12.5px] text-muted-foreground">{t('proxies.list.emptyHint')}</div>
              </div>
            ) : mobile ? (
              <ul className="divide-y divide-border rounded-[2px] border border-line-strong bg-card">
                {proxies.map((p) => (
                  <ProxyCard
                    key={p.id}
                    p={p}
                    test={tests[p.id]}
                    locale={i18n.language}
                    onEdit={() => setTarget(p)}
                    onTest={() => void runTest(p)}
                    onDelete={() => setDeleting(p)}
                  />
                ))}
              </ul>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('proxies.cols.name')}</TableHead>
                    <TableHead>{t('proxies.cols.scheme')}</TableHead>
                    <TableHead>{t('proxies.cols.address')}</TableHead>
                    <TableHead>{t('proxies.cols.auth')}</TableHead>
                    <TableHead>{t('proxies.cols.remoteDns')}</TableHead>
                    <TableHead>{t('proxies.cols.location')}</TableHead>
                    <TableHead>{t('proxies.cols.refs')}</TableHead>
                    <TableHead>{t('proxies.cols.lastTest')}</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {proxies.map((p) => (
                    <ProxyRow
                      key={p.id}
                      p={p}
                      selected={target !== null && target !== 'new' && target.id === p.id}
                      test={tests[p.id]}
                      locale={i18n.language}
                      onEdit={() => setTarget(p)}
                      onTest={() => void runTest(p)}
                      onDelete={() => setDeleting(p)}
                    />
                  ))}
                </TableBody>
              </Table>
            )}
            <div className="mt-3 flex items-start gap-2 text-[12.5px] leading-[1.55] text-muted-foreground">
              <Info size={15} className="mt-0.5 shrink-0" />
              <span>{t('proxies.note')}</span>
            </div>
          </div>
        </Section>

        <Section no="04.2" title={t('proxies.legend.title')} meta={t('proxies.legend.meta')}>
          <div className="grid grid-cols-3 divide-x divide-border mobile:grid-cols-1 mobile:divide-x-0 mobile:divide-y">
            {(['hub', 'lan', 'any'] as const).map((k) => (
              <div key={k} className="px-4 py-3 text-[12.5px] leading-[1.55] text-ink-2">
                <b className="mb-0.5 block font-mono text-[12px] font-medium text-foreground">{t(`proxies.location.${k}`)}</b>
                {t(`proxies.legend.${k}`)}
              </div>
            ))}
          </div>
        </Section>
      </div>

      <ProxyDrawer
        target={target}
        onClose={() => setTarget(null)}
        onSaved={onSaved}
        onDelete={(p) => setDeleting(p)}
        onTested={recordTest}
      />
      <DeleteProxyDialog proxy={deleting} onClose={() => setDeleting(null)} onDeleted={onDeleted} />
    </>
  )
}

interface RowProps {
  p: Proxy
  selected: boolean
  test: TestState | undefined
  locale: string
  onEdit(): void
  onTest(): void
  onDelete(): void
}

function ProxyRow({ p, selected, test, locale, onEdit, onTest, onDelete }: RowProps) {
  const { t } = useTranslation()
  const dnsKey = !remoteDnsApplies(p.scheme as Scheme) ? 'na' : p.remote_dns ? 'yes' : 'no'
  const refs = p.referrers
  return (
    <TableRow className={cn(selected && 'bg-signal-soft')}>
      <TableCell className="h-[52px] font-medium">{p.name}</TableCell>
      <TableCell>
        <span className="rounded-[2px] border border-border px-1.5 py-0.5 font-mono text-[11px]">{p.scheme}</span>
      </TableCell>
      <TableCell mono className="text-[12.5px]">
        {p.address}
      </TableCell>
      <TableCell>
        {p.auth.set ? (
          <span className="inline-flex items-center gap-1">
            <Lock size={13} />
            {t('proxies.auth.has')}
          </span>
        ) : (
          <span className="text-muted-foreground">{t('proxies.auth.none')}</span>
        )}
      </TableCell>
      <TableCell>
        {dnsKey === 'na' ? (
          <span className="text-muted-foreground">{t('proxies.dns.na')}</span>
        ) : dnsKey === 'yes' ? (
          <span className="inline-flex items-center gap-1.5">
            <StatusShape state="ok" size={12} />
            {t('proxies.dns.yes')}
          </span>
        ) : (
          t('proxies.dns.no')
        )}
      </TableCell>
      <TableCell>{locationKeys.has(p.location) ? t(`proxies.location.${p.location}`) : p.location}</TableCell>
      <TableCell>
        {refs.length > 0 ? (
          <>
            {t('proxies.refs.count', { count: refs.length })}
            <span className="ml-1.5 text-[12px] text-muted-foreground">{refs.map((r) => r.name).join('、')}</span>
          </>
        ) : (
          <span className="text-muted-foreground">{t('proxies.refs.zero')}</span>
        )}
      </TableCell>
      <TableCell>
        <LastTestCell test={test} locale={locale} />
      </TableCell>
      <TableCell>
        <div className="flex items-center justify-end gap-1">
          <Button size="xs" variant="outline" className="rounded-[2px]" aria-label={t('proxies.edit', { name: p.name })} onClick={onEdit}>
            {t('common.edit')}
          </Button>
          <Button size="icon-xs" variant="ghost" aria-label={t('proxies.test.aria', { name: p.name })} title={t('common.test')} disabled={test === 'running'} onClick={onTest}>
            <Play />
          </Button>
          <Button size="icon-xs" variant="ghost" aria-label={t('proxies.delete', { name: p.name })} title={t('common.delete')} onClick={onDelete}>
            <Trash2 />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

// 手机宽度：每个代理一张卡片，操作按钮放在底部一行
function ProxyCard({ p, test, locale, onEdit, onTest, onDelete }: Omit<RowProps, 'selected'>) {
  const { t } = useTranslation()
  return (
    <li className="space-y-2 px-3 py-3">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-[14px] font-medium">{p.name}</span>
        <span className="rounded-[2px] border border-border px-1.5 py-0.5 font-mono text-[11px]">{p.scheme}</span>
      </div>
      <div className="font-mono text-[12.5px] break-all">{p.address}</div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted-foreground">
        <span>{p.referrers.length > 0 ? t('proxies.refs.count', { count: p.referrers.length }) : t('proxies.refs.zero')}</span>
        <span>{locationKeys.has(p.location) ? t(`proxies.location.${p.location}`) : p.location}</span>
        {p.auth.set && (
          <span className="inline-flex items-center gap-1">
            <Lock size={12} />
            {t('proxies.auth.has')}
          </span>
        )}
      </div>
      <div className="flex items-center gap-2 text-[12px]">
        <LastTestCell test={test} locale={locale} />
      </div>
      <div className="flex items-center gap-1.5 pt-0.5">
        <Button size="sm" variant="outline" className="rounded-[2px]" aria-label={t('proxies.edit', { name: p.name })} onClick={onEdit}>
          {t('common.edit')}
        </Button>
        <Button size="sm" variant="outline" className="rounded-[2px]" aria-label={t('proxies.test.aria', { name: p.name })} disabled={test === 'running'} onClick={onTest}>
          <Play /> {t('common.test')}
        </Button>
        <Button size="sm" variant="ghost" className="ml-auto" aria-label={t('proxies.delete', { name: p.name })} onClick={onDelete}>
          <Trash2 />
        </Button>
      </div>
    </li>
  )
}

function LastTestCell({ test, locale }: { test: TestState | undefined; locale: string }) {
  const { t } = useTranslation()
  if (test === undefined) return <span className="text-muted-foreground">{t('proxies.test.never')}</span>
  if (test === 'running') return <span className="text-muted-foreground">{t('proxies.test.running')}</span>
  const time = <span className="ml-1.5 font-mono text-[11px] text-muted-foreground">{formatDateTime(test.at, locale)}</span>
  if (test.result?.ok) {
    return (
      <span className="inline-flex items-center gap-1.5 font-mono text-[12px]">
        <StatusShape state="ok" size={13} />
        {t('proxies.test.ok', { status: test.result.status, ms: test.result.latency_ms })}
        {time}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[12px]" title={test.result?.error ?? test.error}>
      <StatusShape state="error" size={13} />
      {t('proxies.test.failed')}
      {time}
    </span>
  )
}
