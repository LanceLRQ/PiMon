import { useTranslation } from 'react-i18next'
import { formatDateTime, formatIn, formatMoney, formatNumber } from '@/lib/time'
import type { Item } from '@/types/generated'
import { cn } from '@/lib/utils'
import { StatusLabel, StatusShape } from './status-shape'

// 数据项按类型渲染（gauge / number / quota / money / state / text / table）。
// 缺失的值显示为「未知」，不当作 0；采集失败保留的旧值带「过期」标记。

interface ReportItemViewProps {
  item: Item
  // 插件声明的数据项标题；缺省时只显示 key
  title?: string
  // 相对时间用的「现在」（毫秒）
  now: number
  className?: string
}

function Unknown() {
  const { t } = useTranslation()
  return <span className="text-muted-foreground">{t('items.unknown')}</span>
}

function isNum(v: number | undefined | null): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

// 细线进度条：fraction 取 0 到 1，超出时夹住；值本身不被改写
function Meter({ fraction, label }: { fraction: number; label: string }) {
  const f = Math.min(1, Math.max(0, fraction))
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(f * 100)}
      className="mt-1.5 h-1.5 w-full overflow-hidden rounded-[1px] border border-line-strong bg-panel-2"
    >
      <div className="h-full bg-ink-2" style={{ width: `${f * 100}%` }} />
    </div>
  )
}

function Gauge({ item, locale }: { item: Item; locale: string }) {
  const { t } = useTranslation()
  if (!isNum(item.value)) return <Unknown />
  const hasRange = isNum(item.min) && isNum(item.max) && item.max > item.min
  return (
    <div>
      <div className="font-mono tabular-nums">
        {formatNumber(item.value, locale)}
        {item.unit && <span className="ml-1 text-muted-foreground">{item.unit}</span>}
      </div>
      {hasRange && <Meter fraction={(item.value - (item.min as number)) / ((item.max as number) - (item.min as number))} label={t('items.gauge')} />}
    </div>
  )
}

function NumberValue({ item, locale }: { item: Item; locale: string }) {
  if (!isNum(item.value)) return <Unknown />
  return (
    <span className="font-mono tabular-nums">
      {formatNumber(item.value, locale)}
      {item.unit && <span className="ml-1 text-muted-foreground">{item.unit}</span>}
    </span>
  )
}

function Quota({ item, now, locale }: { item: Item; now: number; locale: string }) {
  const { t } = useTranslation()
  const pct = isNum(item.remaining_pct) ? item.remaining_pct : null
  const unit = item.unit ? ` ${item.unit}` : ''
  const lines: string[] = []
  if (isNum(item.used) || isNum(item.total)) {
    lines.push(
      t('items.quotaUsed', {
        used: isNum(item.used) ? formatNumber(item.used, locale) + unit : t('items.unknown'),
        total: isNum(item.total) ? formatNumber(item.total, locale) + unit : t('items.unknown'),
      }),
    )
  }
  if (isNum(item.remaining)) lines.push(t('items.quotaRemaining', { value: formatNumber(item.remaining, locale) + unit }))
  if (isNum(item.resets_at)) {
    lines.push(t('items.resetsAt', { at: formatDateTime(item.resets_at, locale), in: formatIn(t, now, item.resets_at) }))
  }
  if (isNum(item.expires_at)) {
    lines.push(t('items.expiresAt', { at: formatDateTime(item.expires_at, locale), in: formatIn(t, now, item.expires_at) }))
  }
  if (pct === null && lines.length === 0) return <Unknown />
  return (
    <div>
      {pct !== null ? (
        <div className="font-mono tabular-nums">{t('items.quotaPct', { pct: formatNumber(pct, locale) })}</div>
      ) : (
        <Unknown />
      )}
      {pct !== null && <Meter fraction={pct / 100} label={t('items.quota')} />}
      {lines.length > 0 && (
        <ul className="mt-1 space-y-0.5 text-[12px] text-muted-foreground">
          {lines.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

function Money({ item, locale }: { item: Item; locale: string }) {
  const { t } = useTranslation()
  if (!isNum(item.amount)) return <Unknown />
  const extra: string[] = []
  if (isNum(item.used)) extra.push(t('items.moneyUsed', { value: formatMoney(item.used, item.currency, locale) }))
  if (isNum(item.total)) extra.push(t('items.moneyTotal', { value: formatMoney(item.total, item.currency, locale) }))
  return (
    <div>
      <div className="font-mono tabular-nums">{formatMoney(item.amount, item.currency, locale)}</div>
      {extra.length > 0 && <div className="mt-0.5 text-[12px] text-muted-foreground">{extra.join(' · ')}</div>}
    </div>
  )
}

function StateValue({ item }: { item: Item }) {
  const shown = item.state === 'ok' || item.state === 'warning' || item.state === 'critical' ? item.state : 'unknown'
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
      <StatusLabel state={shown} />
      {item.text && <span className="text-[12px] text-muted-foreground">{item.text}</span>}
    </div>
  )
}

function cell(v: unknown): string {
  if (v === null || v === undefined) return '—'
  return typeof v === 'object' ? JSON.stringify(v) : String(v)
}

function TableValue({ item }: { item: Item }) {
  const { t } = useTranslation()
  const columns = item.columns ?? []
  const rows = item.rows ?? []
  if (columns.length === 0 && rows.length === 0) return <Unknown />
  return (
    <div className="max-w-full overflow-x-auto rounded-[2px] border border-border">
      <table className="w-full border-collapse text-[12px]">
        {columns.length > 0 && (
          <thead className="bg-panel-2">
            <tr>
              {columns.map((c, i) => (
                <th key={i} className="px-2 py-1 text-left font-mono text-[10.5px] font-normal text-muted-foreground">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
        )}
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td className="px-2 py-1.5 text-muted-foreground" colSpan={Math.max(1, columns.length)}>
                {t('items.tableEmpty')}
              </td>
            </tr>
          )}
          {rows.map((r, ri) => (
            <tr key={ri} className="border-t border-border">
              {r.map((v, ci) => (
                <td key={ci} className="px-2 py-1 font-mono tabular-nums">
                  {cell(v)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function ReportItemView({ item, title, now, className }: ReportItemViewProps) {
  const { t, i18n } = useTranslation()
  const locale = i18n.language
  let body
  switch (item.type) {
    case 'gauge':
      body = <Gauge item={item} locale={locale} />
      break
    case 'number':
      body = <NumberValue item={item} locale={locale} />
      break
    case 'quota':
      body = <Quota item={item} now={now} locale={locale} />
      break
    case 'money':
      body = <Money item={item} locale={locale} />
      break
    case 'state':
      body = <StateValue item={item} />
      break
    case 'text':
      body = item.text ? <p className="break-words whitespace-pre-wrap">{item.text}</p> : <Unknown />
      break
    case 'table':
      body = <TableValue item={item} />
      break
    default:
      body = <span className="text-muted-foreground">{t('items.unsupported', { type: item.type })}</span>
  }
  const wide = item.type === 'table'
  return (
    <div
      data-item-type={item.type}
      data-stale={item.stale ? 'true' : undefined}
      className={cn('border-t border-border px-4 py-2.5 first:border-t-0', item.stale && 'opacity-80', className)}
    >
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span className="rounded-[2px] border border-border px-1 font-mono text-[10.5px] leading-[15px] text-muted-foreground">
          {item.type}
        </span>
        <span className="min-w-0 truncate text-[13px]">{title ?? item.key}</span>
        {title && <code className="min-w-0 truncate font-mono text-[11px] text-muted-foreground">{item.key}</code>}
        {item.stale && (
          <span className="inline-flex items-center gap-1 text-[11.5px] text-muted-foreground">
            <StatusShape state="stale" size={12} />
            {t('status.stale')}
          </span>
        )}
      </div>
      <div className={cn('mt-1 text-[13px]', !wide && 'sm:max-w-[90%]')}>{body}</div>
      {item.error && (
        <div role="note" className="mt-1.5 flex items-start gap-1.5 text-[12px] text-status-crit">
          <StatusShape state="error" size={12} className="mt-0.5" />
          <span className="min-w-0 break-words">{t('items.itemError', { message: item.error })}</span>
        </div>
      )}
    </div>
  )
}
