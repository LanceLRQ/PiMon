import { useTranslation } from 'react-i18next'
import { expandRefs, hasWildcardWithData } from './data'
import { WidgetFrame } from './frame'
import { fitList } from './fit'
import { pairedLatency, useItemDescriber } from './item-view'
import { StatusMarker, levelText } from './status'
import type { TemplateProps } from './types'
import { capacityOf, useBoxSize } from './use-box-size'

// status-grid 模板：多个数据项各占一格的状态灯阵。结构契约：
//   .tpl-grid（CSS grid，列数 = 小组件宽度格数）> .tpl-grid__cell[data-value-level]
//     > .tpl-grid__marker + (.tpl-grid__name[title] + .tpl-grid__sub)
//   放不下的部分折成最后一格 .tpl-grid__more（「+N」）
// 槽：aggregate 来源的 items 槽（全部引用，可跨实例）。
// 副文本：net-reach 的 target[X] 配对同实例的 latency[X] 显示延迟（Ruling 34），没有配对延迟时显示成功率；
//   state 项显示其文字；其余显示读数；缺失显示「未知」。
// 尺寸：2x2、4x2、4x3、6x2；格数按容器高度换算，量不到时按名义容量。

const CELL_PX = 46

export function StatusGridTemplate({ widget, data, defaultThreshold }: TemplateProps) {
  const { t } = useTranslation()
  const d = useItemDescriber(widget, defaultThreshold)
  const [boxRef, box] = useBoxSize<HTMLDivElement>()
  const rawRefs = widget.slots.items ?? []
  const refs = expandRefs(rawRefs, data)
  const columns = Math.max(1, widget.size.cols)
  const cellRows = box ? Math.floor(box.height / CELL_PX) : widget.size.rows * 2 - 1
  const { shown, more } = fitList(refs.length, capacityOf(null, CELL_PX, columns * cellRows))

  return (
    <WidgetFrame widget={widget} data={data}>
      <div ref={boxRef} className="tpl-grid-box h-full min-h-0 overflow-hidden">
        {refs.length === 0 && hasWildcardWithData(rawRefs, data) ? (
          <span className="tpl-grid__empty text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.noItems')}</span>
        ) : refs.length === 0 ? (
          <span className="tpl-grid__unknown text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.unknown')}</span>
        ) : (
          <div className="tpl-grid grid gap-x-3 gap-y-1" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
            {refs.slice(0, shown).map((ref, i) => {
              const view = d.describe(ref, data)
              const latency = d.format(pairedLatency(ref, data))
              const sub = latency ?? (view.text === null ? t('screenWidget.unknown') : `${view.text}${view.unit ?? ''}`)
              const level = view.level ?? 'unknown'
              return (
                <div
                  key={`${ref.instance_id}/${ref.item}/${i}`}
                  className="tpl-grid__cell flex min-w-0 items-center gap-2"
                  style={{ height: CELL_PX }}
                  data-value-level={level}
                >
                  <StatusMarker level={level} size={16} className="tpl-grid__marker" />
                  <div className="flex min-w-0 flex-col">
                    <span className={`tpl-grid__name truncate text-[length:var(--size-value-sm)] leading-tight font-semibold ${levelText[level]}`} title={view.name}>
                      {view.name}
                    </span>
                    <span className="tpl-grid__sub text-s-muted-fg truncate text-[length:var(--size-label)] tabular-nums">{sub}</span>
                  </div>
                </div>
              )
            })}
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
