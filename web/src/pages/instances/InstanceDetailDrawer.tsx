import { Copy, Pause, Play, RefreshCw, Settings2, Trash2 } from 'lucide-react'
import { lazy, Suspense, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { formatDateTime, formatAgo, parseTime } from '@/lib/time'
import type { Instance, PluginOutput } from '@/types/generated'
import { Button } from '@/ui/button'
import { NumberTag } from '@/ui/numbered-label'
import { Note } from '@/ui/note'
import { ReportItemView } from '@/ui/report-items'
import { Sheet, SheetContent } from '@/ui/sheet'
import { SpecList } from '@/ui/spec-list'
import { StatusLabel, StatusShape } from '@/ui/status-shape'
import { WordmarkBadge } from '@/ui/wordmark-badge'
import type { InstanceActions } from './actions'
import { historyItems } from './history-items'
import type { InstanceDetailView } from './types'

// 数据项标题：精确匹配插件声明的 key，动态集合（prefix[*]）按前缀匹配其成员
export function outputTitle(outputs: PluginOutput[] | undefined, key: string): string | undefined {
  if (!outputs) return undefined
  const exact = outputs.find((o) => o.key === key)
  if (exact) return exact.title
  const m = /^(.*)\[.*\]$/.exec(key)
  if (m) return outputs.find((o) => o.key === `${m[1]}[*]`)?.title
  return undefined
}

// 图表库体积较大，只在打开抽屉时按需加载
const HistoryChart = lazy(() => import('./HistoryChart'))

function Section({ no, title, children }: { no: string; title: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-border py-3 first:border-t-0">
      <div className="mb-2 flex items-center gap-2 px-4 text-[12.5px] text-ink-2">
        <NumberTag no={no} />
        <h3 className="font-medium">{title}</h3>
      </div>
      {children}
    </section>
  )
}

interface DrawerProps {
  // 要查看的实例 id；null 表示关闭
  id: string | null
  // 实时 store 里的该实例（随 patch 更新）；synced 且找不到说明已被删除
  instance: Instance | undefined
  synced: boolean
  now: number
  outputs: PluginOutput[] | undefined
  pluginName: string | undefined
  actions: InstanceActions
  onClose(): void
}

type Detail = { kind: 'loading' } | { kind: 'ready'; data: InstanceDetailView } | { kind: 'error'; error: unknown }

// 实例详情抽屉：全部数据项按类型渲染、24 小时历史曲线、最近错误；状态与读数随实时推送更新
export function InstanceDetailDrawer({ id, instance, synced, now, outputs, pluginName, actions, onClose }: DrawerProps) {
  const { t, i18n } = useTranslation()
  const [detail, setDetail] = useState<Detail>({ kind: 'loading' })

  const refreshKey = instance ? `${instance.last_success_at ?? ''}|${instance.failures}|${instance.display_state}|${instance.updated_at}` : ''

  useEffect(() => {
    if (!id) return
    const controller = new AbortController()
    http
      .get<InstanceDetailView>(`/api/instances/${id}`, { signal: controller.signal })
      .then((data) => setDetail({ kind: 'ready', data }))
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setDetail({ kind: 'error', error })
      })
    return () => controller.abort()
  }, [id, refreshKey])

  // 切换到另一个实例时先回到加载中
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 抽屉换了实例，旧详情不能继续显示
    setDetail({ kind: 'loading' })
  }, [id])

  // 实例已被删除（别处删除或本抽屉删除后）：关闭抽屉
  const gone = !!id && synced && !instance
  useEffect(() => {
    if (gone) onClose()
  }, [gone, onClose])

  const open = !!id && !!instance
  const inst = instance
  const data = detail.kind === 'ready' && detail.data.id === id ? detail.data : null
  const report = data?.report ?? null
  const items = report?.items ?? []
  const titleOf = (key: string) => outputTitle(outputs, key) ?? key
  const numeric = historyItems(items)
  const lastSuccess = parseTime(inst?.last_success_at)
  const running = inst ? actions.running.has(inst.id) : false

  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      {open && inst && (
        <SheetContent
          srTitle={t('detail.title', { name: inst.name })}
          header={
            <div className="flex items-center gap-2.5">
              <StatusShape state={inst.display_state} size={18} />
              <div className="min-w-0">
                <h2 className="truncate text-[15px] font-medium">{inst.name}</h2>
                <div className="flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
                  <WordmarkBadge name={inst.plugin_id} size={16} />
                  <span className="truncate">{inst.plugin_id}</span>
                </div>
              </div>
            </div>
          }
          footer={
            <>
              <Button variant="outline" size="sm" className="rounded-[2px]" disabled={running} onClick={() => void actions.run(inst)}>
                <RefreshCw className={running ? 'animate-spin' : undefined} /> {t('instances.menu.run')}
              </Button>
              <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => void actions.setPaused(inst, !inst.paused)}>
                {inst.paused ? <Play /> : <Pause />} {inst.paused ? t('instances.menu.resume') : t('instances.menu.pause')}
              </Button>
              <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => actions.edit(inst)}>
                <Settings2 /> {t('instances.menu.edit')}
              </Button>
              <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => void actions.copy(inst)}>
                <Copy /> {t('instances.menu.copy')}
              </Button>
              <Button variant="outline" size="sm" className="rounded-[2px] text-destructive" onClick={() => actions.requestDelete([inst])}>
                <Trash2 /> {t('instances.menu.delete')}
              </Button>
            </>
          }
        >
          <Section no="d1" title={t('detail.status')}>
            <div className="px-4">
              {inst.summary && <p className="mb-2 text-[13px]">{inst.summary}</p>}
              <SpecList
                rows={[
                  { label: t('detail.state'), value: <StatusLabel state={inst.display_state} /> },
                  { label: t('detail.plugin'), value: pluginName ? `${pluginName} · ${inst.plugin_id}` : inst.plugin_id },
                  { label: t('instances.cols.runs'), value: inst.runs_on === 'hub' ? t('instances.hubLocal') : inst.runs_on },
                  {
                    label: t('detail.interval'),
                    value: t('detail.intervalValue', { n: inst.effective_interval_seconds }) + (inst.interval_seconds > 0 ? t('detail.intervalOverride') : ''),
                  },
                  {
                    label: t('detail.lastSuccess'),
                    value: lastSuccess === null ? t('time.never') : `${formatAgo(t, now, lastSuccess)} · ${formatDateTime(lastSuccess, i18n.language)}`,
                  },
                  { label: t('detail.failures'), value: String(inst.failures), highlight: inst.failures > 0 },
                  ...(inst.paused ? [{ label: t('detail.paused'), value: t('detail.pausedYes'), highlight: true }] : []),
                ]}
              />
            </div>
          </Section>

          <Section no="d2" title={t('detail.items', { count: items.length })}>
            {detail.kind === 'loading' && !data && <p className="px-4 text-[13px] text-muted-foreground">{t('shell.loading')}</p>}
            {detail.kind === 'error' && (
              <p role="alert" className="px-4 text-[13px] text-status-crit">
                {translateErrorValue(i18n, detail.error)}
              </p>
            )}
            {data && !report && <p className="px-4 text-[13px] text-muted-foreground">{t('detail.noReport')}</p>}
            {report?.stale && (
              <div className="px-4 pb-2">
                <Note tone="warn" role="status">
                  {t('detail.staleReport')}
                </Note>
              </div>
            )}
            {items.length > 0 && (
              <div className="border-y border-border">
                {items.map((it) => (
                  <ReportItemView key={it.key} item={it} title={outputTitle(outputs, it.key)} now={now} />
                ))}
              </div>
            )}
            {data && report && items.length === 0 && <p className="px-4 text-[13px] text-muted-foreground">{t('detail.noItems')}</p>}
          </Section>

          <Section no="d3" title={t('detail.history')}>
            {data ? (
              <Suspense fallback={<p className="px-4 text-[13px] text-muted-foreground">{t('shell.loading')}</p>}>
                <HistoryChart instanceId={inst.id} items={numeric} titleOf={titleOf} refreshKey={refreshKey} />
              </Suspense>
            ) : (
              <p className="px-4 text-[13px] text-muted-foreground">{t('shell.loading')}</p>
            )}
          </Section>

          <Section no="d4" title={t('detail.errors')}>
            <div className="space-y-2 px-4">
              {inst.issue && (
                <Note tone="warn" role="status">
                  <div className="font-medium">{t('detail.issue')}</div>
                  <div className="break-words">{inst.issue}</div>
                </Note>
              )}
              {inst.last_error ? (
                <div>
                  <div className="mb-1 text-[12px] text-muted-foreground">{t('detail.lastError')}</div>
                  <pre className="max-h-48 overflow-auto rounded-[2px] border border-border bg-panel-2 p-2 font-mono text-[11.5px] leading-[1.5] break-words whitespace-pre-wrap">
                    {inst.last_error}
                  </pre>
                </div>
              ) : (
                !inst.issue && <p className="text-[13px] text-muted-foreground">{t('detail.noError')}</p>
              )}
              {data?.problems && Object.keys(data.problems).length > 0 && (
                <Note tone="crit" role="status">
                  <div className="font-medium">{t('detail.problems')}</div>
                  <ul className="mt-0.5 font-mono text-[12px]">
                    {Object.entries(data.problems).map(([field, code]) => (
                      <li key={field}>
                        {field}：{code}
                      </li>
                    ))}
                  </ul>
                </Note>
              )}
            </div>
          </Section>

          {inst.plugin_id === 'weather' && (
            <p className="border-t border-border px-4 py-3 text-[12px] text-muted-foreground">
              {t('detail.weatherCredit')}{' '}
              <a href="https://open-meteo.com/" target="_blank" rel="noreferrer noopener" className="underline underline-offset-2">
                Weather data by Open-Meteo.com
              </a>
            </p>
          )}
        </SheetContent>
      )}
    </Sheet>
  )
}
