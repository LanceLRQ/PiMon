import { useTranslation } from 'react-i18next'
import { expandRefs, hasWildcardWithData } from './data'
import { WidgetFrame } from './frame'
import { fitList } from './fit'
import { pairedLatency, useItemDescriber } from './item-view'
import type { StatusLevel } from '@/themes/types'
import type { ResolvedHost } from '@/types/generated'
import { dimStyle } from './frame'
import { DimIcon, StatusMarker, levelText, type DimReason } from './status'
import type { TemplateProps } from './types'
import { capacityOf, useBoxSize } from './use-box-size'

// status-grid 模板：多个数据项各占一格的状态灯阵。结构契约：
//   .tpl-grid（CSS grid，列数 = 小组件宽度格数）> .tpl-grid__cell[data-value-level]
//     > .tpl-grid__marker + (.tpl-grid__name[title] + .tpl-grid__sub)
//   放不下的部分折成最后一格 .tpl-grid__more（「+N」）
// 槽：aggregate 来源的 items 槽（全部引用，可跨实例）。
// 副文本：net-reach 的 target[X] 配对同实例的 latency[X] 显示延迟（Ruling 34），没有配对延迟时显示成功率；
//   state 项显示其文字；其余显示读数；缺失显示「未知」。
// 主机灯组（widget.hosts 非空，data_source 为 hosts）：每台机器一格，沿用同一结构，另加 data-host-state；
//   online 取 CPU 阈值级别（没有 CPU 实例按 ok），其余状态由主机状态决定级别与副文本；
//   offline 用 dim 弱化、dormant 更淡并去色，只弱化格子内容，不改变状态标记。
// 尺寸：2x2、4x2、4x3、6x2；格数按容器高度换算，量不到时按名义容量。

const CELL_PX = 46
// 行间距（Tailwind gap-y-1），算行数时要计入：n 行总高 = n × CELL_PX + (n − 1) × ROW_GAP_PX
const ROW_GAP_PX = 4

// 主机状态 → 级别、图标、副文本键（online 的副文本来自 CPU 读数）
const hostStates: Record<string, { level: StatusLevel; icon?: DimReason; text?: string }> = {
  disconnected: { level: 'warning', text: 'screenWidget.host.disconnected' },
  offline: { level: 'critical', icon: 'offline', text: 'screenWidget.offline' },
  dormant: { level: 'unknown', icon: 'dormant', text: 'screenWidget.dormant' },
  pending: { level: 'unknown', icon: 'stale', text: 'screenWidget.host.pending' },
  maintenance: { level: 'unknown', icon: 'maintenance', text: 'screenWidget.maintenance' },
  incompatible: { level: 'warning', text: 'screenWidget.host.incompatible' },
}

interface GridCell {
  key: string
  name: string
  level: StatusLevel
  sub: string
  hostState?: string
  icon?: DimReason
}

export function StatusGridTemplate({ widget, data, defaultThreshold }: TemplateProps) {
  const { t } = useTranslation()
  const d = useItemDescriber(widget, defaultThreshold)
  const [boxRef, box] = useBoxSize<HTMLDivElement>()
  const rawRefs = widget.slots.items ?? []
  const hosts = widget.hosts
  const refs = hosts ? [] : expandRefs(rawRefs, data)
  const columns = Math.max(1, widget.size.cols)
  const cellRows = box ? Math.floor((box.height + ROW_GAP_PX) / (CELL_PX + ROW_GAP_PX)) : widget.size.rows * 2 - 1

  const cells: GridCell[] = hosts
    ? hosts.map((h) => hostCell(h))
    : refs.map((ref, i) => {
        const view = d.describe(ref, data)
        const latency = d.format(pairedLatency(ref, data))
        const sub = latency ?? (view.text === null ? t('screenWidget.unknown') : `${view.text}${view.unit ?? ''}`)
        return { key: `${ref.instance_id}/${ref.item}/${i}`, name: view.name, level: view.level ?? 'unknown', sub }
      })
  function hostCell(h: ResolvedHost): GridCell {
    const name = h.name || t('screenWidget.host.hub')
    const known = hostStates[h.state]
    if (known) return { key: h.id, name, level: known.level, sub: known.text ? t(known.text) : '', hostState: h.state, icon: known.icon }
    // online：有 CPU 实例按其阈值，缺失显示未知（不当作零）；没有实例不显示数值
    const ref = h.instance_id ? rawRefs.find((r) => r.instance_id === h.instance_id) : undefined
    if (!ref) return { key: h.id, name, level: 'ok', sub: '', hostState: h.state }
    const view = d.describe(ref, data)
    const sub = view.text === null ? t('screenWidget.unknown') : `${view.text}${view.unit ?? ''}`
    return { key: h.id, name, level: view.item ? (view.level ?? 'ok') : 'unknown', sub, hostState: h.state }
  }
  const { shown, more } = fitList(cells.length, capacityOf(null, CELL_PX, columns * cellRows))

  return (
    <WidgetFrame widget={widget} data={data}>
      <div ref={boxRef} className="tpl-grid-box h-full min-h-0 overflow-hidden">
        {cells.length === 0 && hasWildcardWithData(rawRefs, data) ? (
          <span className="tpl-grid__empty text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.noItems')}</span>
        ) : cells.length === 0 ? (
          <span className="tpl-grid__unknown text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.unknown')}</span>
        ) : (
          <div className="tpl-grid grid gap-x-3 gap-y-1" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
            {cells.slice(0, shown).map((c) => (
              <div
                key={c.key}
                className="tpl-grid__cell flex min-w-0 items-center gap-2"
                style={{ height: CELL_PX, ...(c.hostState === 'offline' || c.hostState === 'dormant' ? dimStyle(c.hostState === 'dormant') : undefined) }}
                data-value-level={c.level}
                data-host-state={c.hostState}
              >
                <StatusMarker level={c.level} size={16} className="tpl-grid__marker" />
                <div className="flex min-w-0 flex-col">
                  <span className={`tpl-grid__name truncate text-[length:var(--size-value-sm)] leading-tight font-semibold ${levelText[c.level]}`} title={c.name}>
                    {c.name}
                  </span>
                  <span className="tpl-grid__sub text-s-muted-fg flex min-w-0 items-center gap-1 text-[length:var(--size-label)] tabular-nums">
                    {c.icon && <DimIcon reason={c.icon} size={11} />}
                    <span className="truncate">{c.sub}</span>
                  </span>
                </div>
              </div>
            ))}
            {more > 0 && (
              <div className="tpl-grid__more text-s-muted-fg flex items-center text-[length:var(--size-label)] tabular-nums" style={{ height: CELL_PX }}>
                {t('screenWidget.more', { n: more })}
              </div>
            )}
          </div>
        )}
      </div>
    </WidgetFrame>
  )
}
