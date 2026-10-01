import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import { formatNumber } from '@/lib/time'
import type { HistoryResult, Item } from '@/types/generated'
import { Segmented } from '@/ui/segmented'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; result: HistoryResult }
  | { kind: 'error'; error: unknown }

interface HistoryChartProps {
  instanceId: string
  items: Item[]
  titleOf(key: string): string
  // 实例有新数据时变化，触发重新取历史
  refreshKey?: string
}

function formatClock(ms: number, locale: string): string {
  return new Date(ms).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false })
}

// 最近 24 小时历史曲线：先选数据项（quota 另可选字段），数据来自 history API
export default function HistoryChart({ instanceId, items, titleOf, refreshKey }: HistoryChartProps) {
  const { t, i18n } = useTranslation()
  const locale = i18n.language
  const [key, setKey] = useState(items[0]?.key ?? '')
  const [field, setField] = useState<'remaining_pct' | 'used'>('remaining_pct')
  const [state, setState] = useState<LoadState>({ kind: 'loading' })

  const current = items.find((i) => i.key === key) ?? items[0]
  const effectiveKey = current?.key ?? ''
  const isQuota = current?.type === 'quota'

  useEffect(() => {
    if (!effectiveKey) return
    const controller = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换数据项时重置为加载中
    setState((s) => (s.kind === 'ready' && s.result.item === effectiveKey && (!isQuota || s.result.field === field) ? s : { kind: 'loading' }))
    http
      .get<HistoryResult>(`/api/instances/${instanceId}/history`, {
        query: { item: effectiveKey, field: isQuota ? field : undefined, range: '24h' },
        signal: controller.signal,
      })
      .then((result) => setState({ kind: 'ready', result }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return
        setState({ kind: 'error', error })
      })
    return () => controller.abort()
  }, [instanceId, effectiveKey, isQuota, field, refreshKey])

  const result = state.kind === 'ready' ? state.result : null
  const data = useMemo(() => (result ? result.points.map((p) => ({ t: p.t, v: p.avg })) : []), [result])

  if (!current) return <p className="px-4 text-[13px] text-muted-foreground">{t('detail.historyNoNumeric')}</p>

  let body
  if (state.kind === 'loading') {
    body = <p className="py-6 text-center text-[13px] text-muted-foreground">{t('shell.loading')}</p>
  } else if (state.kind === 'error') {
    body = (
      <p role="alert" className="py-6 text-center text-[13px] text-status-crit">
        {isApiError(state.error) && state.error.status === 404 ? t('detail.historyNone') : translateErrorValue(i18n, state.error)}
      </p>
    )
  } else if (data.length === 0) {
    body = <p className="py-6 text-center text-[13px] text-muted-foreground">{t('detail.historyEmpty')}</p>
  } else {
    body = (
      <>
        <div className="h-40 w-full" data-testid="history-chart">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={data} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
              <CartesianGrid stroke="var(--border)" strokeDasharray="2 3" vertical={false} />
              <XAxis
                dataKey="t"
                type="number"
                scale="time"
                domain={[result!.from, result!.to]}
                tickFormatter={(v: number) => formatClock(v, locale)}
                tick={{ fontSize: 10, fill: 'var(--muted-foreground)' }}
                stroke="var(--line-strong)"
                minTickGap={36}
              />
              <YAxis
                width={44}
                tick={{ fontSize: 10, fill: 'var(--muted-foreground)' }}
                stroke="var(--line-strong)"
                tickFormatter={(v: number) => formatNumber(v, locale)}
                domain={['auto', 'auto']}
              />
              <Tooltip
                labelFormatter={(v) => formatClock(Number(v), locale)}
                formatter={(v) => [formatNumber(Number(v), locale), titleOf(effectiveKey)]}
                contentStyle={{ background: 'var(--card)', border: '1px solid var(--line-strong)', borderRadius: 2, fontSize: 12 }}
              />
              <Area type="monotone" dataKey="v" stroke="var(--ink-2)" fill="var(--signal)" fillOpacity={0.25} strokeWidth={1.5} dot={false} isAnimationActive={false} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
        <p className="mt-1 font-mono text-[11px] text-muted-foreground">
          {t('detail.historyMeta', { points: data.length, tier: result!.tier })}
        </p>
      </>
    )
  }

  return (
    <div className="px-4">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <select
          aria-label={t('detail.historyItem')}
          value={effectiveKey}
          onChange={(e) => setKey(e.target.value)}
          className="h-8 min-w-0 max-w-full flex-1 rounded-[2px] border border-line-strong bg-card px-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {items.map((i) => (
            <option key={i.key} value={i.key}>
              {titleOf(i.key)}
            </option>
          ))}
        </select>
        {isQuota && (
          <Segmented
            ariaLabel={t('detail.historyField')}
            value={field}
            onChange={setField}
            options={[
              { value: 'remaining_pct', label: t('detail.fieldRemainingPct'), title: t('detail.fieldRemainingPct') },
              { value: 'used', label: t('detail.fieldUsed'), title: t('detail.fieldUsed') },
            ]}
          />
        )}
      </div>
      {body}
    </div>
  )
}
