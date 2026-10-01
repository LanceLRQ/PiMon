import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Item, ResolvedRef, ResolvedWidget } from '@/types/generated'
import {
  OpenMeteoAttribution,
  StatusMarker,
  WidgetView,
  formatStamp,
  levelText,
  resolveStatus,
  useItemDescriber,
  useScreenEnv,
  type InstanceDataMap,
} from '@/templates'

export interface DetailLayerProps {
  widget: ResolvedWidget
  data: InstanceDataMap
  /** `${实例 id}/${数据项键}` → 服务端解析的数据项标题；没有标题的项回落到 label 或键 */
  titles: ReadonlyMap<string, string>
  onClose: () => void
  /** 滚动等操作：续期详情层的空闲计时 */
  onActivity: () => void
}

export const refTitleKey = (instanceId: string, item: string) => `${instanceId}/${item}`

function instanceIdsOf(widget: ResolvedWidget): string[] {
  const ids = new Set<string>()
  if (widget.instance_id) ids.add(widget.instance_id)
  for (const refs of Object.values(widget.slots)) for (const r of refs) ids.add(r.instance_id)
  return [...ids]
}

function isChartable(item: Item | undefined): boolean {
  if (!item || item.key === 'wind_direction') return false
  return item.type === 'number' || item.type === 'gauge' || item.type === 'quota' || item.type === 'money'
}

/** 历史曲线用的数据项：小组件引用里第一个数值项 */
function chartRef(widget: ResolvedWidget, data: InstanceDataMap): ResolvedRef | undefined {
  for (const refs of Object.values(widget.slots)) {
    for (const r of refs) {
      if (isChartable(data[r.instance_id]?.items.find((i) => i.key === r.item))) return r
    }
  }
  return undefined
}

function TableItem({ item }: { item: Item }) {
  return (
    <table className="mt-1 w-full text-left text-[length:var(--size-label)] text-s-muted-fg">
      {item.columns && (
        <thead>
          <tr>
            {item.columns.map((c, i) => (
              <th key={i} className="pr-3 font-normal">
                {c}
              </th>
            ))}
          </tr>
        </thead>
      )}
      <tbody>
        {(item.rows ?? []).map((row, ri) => (
          <tr key={ri}>
            {row.map((cell, ci) => (
              <td key={ci} className="pr-3 tabular-nums">
                {cell == null ? '—' : String(cell)}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

/**
 * 详情层（设计 5.8）：全屏显示被点开小组件所属实例的全部数据项与历史曲线。
 * 内容较长时允许手指竖向滑动但不显示滚动条；无操作 60 秒自动关闭由状态机负责。
 */
export function DetailLayer({ widget, data, titles, onClose, onActivity }: DetailLayerProps) {
  const { t } = useTranslation()
  const { timezone, lang, now } = useScreenEnv()
  const d = useItemDescriber(widget)
  const ids = instanceIdsOf(widget)
  const primary = data[ids[0] ?? '']
  const status = resolveStatus(widget.display_state, primary?.report_status)
  const level = status.kind === 'placeholder' ? null : status.level
  const rows = ids.flatMap((id) =>
    (data[id]?.items ?? []).map((item) => ({
      key: `${id}/${item.key}`,
      item,
      view: d.describe({ instance_id: id, item: item.key, title: titles.get(refTitleKey(id, item.key)) }, data),
    })),
  )
  const chart = chartRef(widget, data)
  const isWeather = widget.template === 'weather' || widget.plugin_id === 'weather'
  const updatedMs = primary?.last_success_at ? Date.parse(primary.last_success_at) : Number.NaN

  return (
    <div
      data-detail-layer
      className="absolute inset-0 z-40 flex flex-col bg-s-bg text-s-fg"
      style={{ padding: 'var(--card-pad, 16px)' }}
    >
      <div className="mb-3 flex shrink-0 items-center gap-3">
        {level && <StatusMarker level={level} size={18} />}
        <h2 className="m-0 min-w-0 flex-1 truncate text-[length:var(--size-value-md)] font-semibold font-[family-name:var(--font-display)]">
          {widget.title}
        </h2>
        {!Number.isNaN(updatedMs) && (
          <span className="text-s-muted-fg text-[length:var(--size-label)] tabular-nums">
            {t('screenApp.detail.updated')} {formatStamp(updatedMs, now(), timezone, lang)}
          </span>
        )}
        <button
          type="button"
          aria-label={t('screenApp.detail.close')}
          onClick={onClose}
          className="grid size-12 shrink-0 place-items-center rounded-[var(--radius-card)] border border-s-border bg-s-card text-s-fg"
        >
          <X size={24} aria-hidden />
        </button>
      </div>
      <div
        data-detail-scroll
        className="screen-no-scrollbar min-h-0 flex-1 overflow-y-auto overscroll-contain"
        style={{ touchAction: 'pan-y' }}
        onScroll={onActivity}
      >
        {primary?.summary && <p className="mt-0 mb-3 text-s-muted-fg text-[length:var(--size-value-sm)]">{primary.summary}</p>}
        {chart && (
          <section className="mb-4" aria-label={t('screenApp.detail.history')}>
            <div className="h-[180px]">
              <WidgetView
                widget={{
                  ...widget,
                  id: `${widget.id}-history`,
                  source: 'generic',
                  template: 'chart',
                  size: { cols: 4, rows: 2 },
                  slots: { value: [chart] },
                  options: { ...widget.options, range: '24h' },
                }}
                data={data}
              />
            </div>
          </section>
        )}
        <section aria-label={t('screenApp.detail.items')}>
          {rows.length === 0 ? (
            <p className="text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenApp.detail.noItems')}</p>
          ) : (
            <ul className="m-0 list-none p-0">
              {rows.map(({ key, item, view }) => (
                <li key={key} data-detail-item={item.key} className="border-b border-s-border py-2">
                  <div className="flex min-w-0 items-center gap-3">
                    {view.level && <StatusMarker level={view.level} size={14} />}
                    <span className="min-w-0 flex-1 truncate text-s-muted-fg text-[length:var(--size-value-sm)]" title={view.name}>
                      {view.name}
                    </span>
                    {item.type !== 'table' && (
                      <span
                        className={`shrink-0 font-semibold tabular-nums font-[family-name:var(--font-numeric)] text-[length:var(--size-value-sm)] ${view.level ? levelText[view.level] : 'text-s-fg'}`}
                      >
                        {view.text ?? t('screenWidget.unknown')}
                        {view.unit && <span className="ml-1 text-s-muted-fg font-normal">{view.unit}</span>}
                      </span>
                    )}
                  </div>
                  {item.type === 'table' && <TableItem item={item} />}
                </li>
              ))}
            </ul>
          )}
        </section>
        {isWeather && (
          <div className="mt-3">
            <OpenMeteoAttribution />
          </div>
        )}
      </div>
    </div>
  )
}
