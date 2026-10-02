import { useTranslation } from 'react-i18next'
import { expandRefs, findItem, hasWildcardWithData, slotRef } from './data'
import { useScreenEnv } from './env'
import { WidgetFrame } from './frame'
import { fitList } from './fit'
import { formatNumber } from './format'
import { usePluginText } from './plugin-text'
import type { TemplateProps } from './types'
import { capacityOf, useBoxSize } from './use-box-size'

// table 模板：一个 table 数据项（columns + rows）。结构契约：
//   .tpl-table > .tpl-table__header > .tpl-table__head × 列数；
//   .tpl-table__row > .tpl-table__cell × 列数；最后放不下的部分折成 .tpl-table__more（「+N」）
// 槽：plugin 来源用 table 槽，generic 来源用 value 槽。
// 尺寸：2x2、4x2、4x3，以及插件声明的 6x1；列数不超过小组件的宽度格数（至少 2 列），多出的列被丢弃。
// 行数按容器高度扣掉表头换算，量不到时按名义容量；空单元格显示「—」，table 项缺失或没有行显示「未知」。
// 表头与单元格都是承载信息的小字，用 text-s-muted-fg 与 text-s-fg（Ruling 42）。

const ROW_PX = 26

export function TableTemplate({ widget, data }: TemplateProps) {
  const { t } = useTranslation()
  const { lang } = useScreenEnv()
  const pluginText = usePluginText(widget.plugin_id)
  const [boxRef, box] = useBoxSize<HTMLDivElement>()
  const ref = slotRef(widget, 'table', 'value')
  // 通配引用取展开后的第一个 table 成员
  const members = ref ? expandRefs([ref], data) : []
  const item = findItem(members[0], data)
  const table = item?.type === 'table' ? item : undefined
  const empty = ref !== undefined && members.length === 0 && hasWildcardWithData([ref], data)
  const rows = table?.rows ?? []
  const widthColumns = Math.max(2, widget.size.cols)
  const natural = table?.columns?.length ?? rows.reduce((m, r) => Math.max(m, r.length), 0)
  const columnCount = Math.min(widthColumns, natural)
  const columns = (table?.columns ?? []).slice(0, columnCount)
  const hasHeader = (table?.columns?.length ?? 0) > 0
  const headerRows = hasHeader ? 1 : 0
  const { shown, more } = fitList(rows.length, capacityOf(box, ROW_PX, widget.size.rows * 2) - headerRows)
  const grid = { gridTemplateColumns: `repeat(${Math.max(1, columnCount)}, minmax(0, 1fr))`, height: ROW_PX }

  const cellText = (v: unknown): string => {
    if (v === null || v === undefined || v === '') return '—'
    return typeof v === 'number' ? formatNumber(v, lang) : pluginText(String(v))
  }

  return (
    <WidgetFrame widget={widget} data={data}>
      <div ref={boxRef} className="tpl-table h-full min-h-0 overflow-hidden">
        {empty ? (
          <span className="tpl-table__empty text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.noItems')}</span>
        ) : !table || rows.length === 0 ? (
          <span className="tpl-table__unknown text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.unknown')}</span>
        ) : (
          <>
            {hasHeader && (
              <div className="tpl-table__header border-s-border grid items-center gap-2 border-b" style={grid}>
                {columns.map((c, i) => (
                  <span key={i} className="tpl-table__head text-s-muted-fg truncate text-[length:var(--size-label)] font-[family-name:var(--font-label)]" title={c}>
                    {c}
                  </span>
                ))}
              </div>
            )}
            {rows.slice(0, shown).map((row, ri) => (
              <div key={ri} className="tpl-table__row grid items-center gap-2" style={grid}>
                {Array.from({ length: columnCount }, (_, ci) => {
                  const text = cellText(row[ci])
                  return (
                    <span key={ci} className="tpl-table__cell text-s-fg truncate text-[length:var(--size-label)] tabular-nums" title={text}>
                      {text}
                    </span>
                  )
                })}
              </div>
            ))}
            {more > 0 && (
              <div className="tpl-table__more text-s-muted-fg flex items-center text-[length:var(--size-label)] tabular-nums" style={{ height: ROW_PX }}>
                {t('screenWidget.more', { n: more })}
              </div>
            )}
          </>
        )}
      </div>
    </WidgetFrame>
  )
}
