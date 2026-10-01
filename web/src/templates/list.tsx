import { useTranslation } from 'react-i18next'
import type { Item, ResolvedWidget, WidgetRef } from '@/types/generated'
import { findItem } from './data'
import { useScreenEnv } from './env'
import { WidgetFrame } from './frame'
import { fitList } from './fit'
import { formatNumber } from './format'
import { useItemDescriber, type ItemView } from './item-view'
import { layoutVariant } from './size'
import { StatusMarker, levelText } from './status'
import type { TemplateProps } from './types'
import { capacityOf, useBoxSize } from './use-box-size'

// list 模板：多个数据项各占一行。结构契约：
//   .tpl-list > ul.tpl-list__rows > li.tpl-list__row[data-value-level?]
//     > (.tpl-list__marker) .tpl-list__label[title] .tpl-list__value (> .tpl-list__unit?)
//   最后一行放不下的部分折成 li.tpl-list__more（「+N」）
// 槽：plugin 与 aggregate 来源取 items 槽的全部引用；generic 来源取 value 槽，若是 table 项则首列做标签、次列做读数。
// 尺寸：2x1、2x2、2x3、4x2、4x3，都是单列；行数按容器高度换算，量不到时按尺寸名义容量。
// 承载信息的小字（单位、标签）用 text-s-muted-fg（Ruling 42）。

const ROW_PX = 28

interface Row {
  key: string
  view: ItemView
}

function tableRows(item: Item, lang: 'zh' | 'en', pluginText: (s: string) => string): Row[] {
  return (item.rows ?? []).map((r, i) => {
    const name = r[0] == null ? '' : String(r[0])
    const raw = r[1]
    const text = raw == null || raw === '' ? null : typeof raw === 'number' ? formatNumber(raw, lang) : pluginText(String(raw))
    return { key: `r${i}`, view: { name, text, level: text === null ? 'unknown' : null, item } }
  })
}

function rowsOf(widget: ResolvedWidget, data: TemplateProps['data'], describe: (ref: WidgetRef) => ItemView, lang: 'zh' | 'en', pluginText: (s: string) => string): Row[] {
  const refs = widget.slots.items ?? []
  if (refs.length > 0) return refs.map((ref, i) => ({ key: `${ref.instance_id}/${ref.item}/${i}`, view: describe(ref) }))
  const single = widget.slots.value?.[0]
  if (!single) return []
  const item = findItem(single, data)
  if (item?.type === 'table') return tableRows(item, lang, pluginText)
  return [{ key: single.item, view: describe(single) }]
}

export function ListTemplate({ widget, data, defaultThreshold }: TemplateProps) {
  const { t } = useTranslation()
  const { lang } = useScreenEnv()
  const d = useItemDescriber(widget, defaultThreshold)
  const [boxRef, box] = useBoxSize<HTMLDivElement>()
  const rows = rowsOf(widget, data, (ref) => d.describe(ref, data), lang, d.pluginText)
  const nominal = layoutVariant(widget.size) === 'wide' ? 2 : widget.size.rows * 2
  const { shown, more } = fitList(rows.length, capacityOf(box, ROW_PX, nominal))

  return (
    <WidgetFrame widget={widget} data={data}>
      <div ref={boxRef} className="tpl-list h-full min-h-0 overflow-hidden">
        {rows.length === 0 ? (
          <span className="tpl-list__unknown text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.unknown')}</span>
        ) : (
          <ul className="tpl-list__rows m-0 flex list-none flex-col p-0">
            {rows.slice(0, shown).map(({ key, view }) => (
              <li
                key={key}
                className="tpl-list__row flex min-w-0 items-center gap-2"
                style={{ height: ROW_PX }}
                data-value-level={view.level ?? undefined}
              >
                {view.level && <StatusMarker level={view.level} size={12} className="tpl-list__marker" />}
                <span
                  className="tpl-list__label text-s-muted-fg min-w-0 flex-1 truncate text-[length:var(--size-label)]"
                  title={view.name}
                >
                  {view.name}
                </span>
                <span
                  className={`tpl-list__value max-w-[60%] shrink-0 truncate text-[length:var(--size-value-sm)] font-semibold tabular-nums font-[family-name:var(--font-numeric)] ${view.level ? levelText[view.level] : 'text-s-fg'}`}
                >
                  {view.text ?? t('screenWidget.unknown')}
                  {view.text !== null && view.unit && <span className="tpl-list__unit text-s-muted-fg ml-0.5 text-[length:var(--size-label)] font-normal">{view.unit}</span>}
                </span>
              </li>
            ))}
            {more > 0 && (
              <li className="tpl-list__more text-s-muted-fg flex items-center text-[length:var(--size-label)] tabular-nums" style={{ height: ROW_PX }}>
                {t('screenWidget.more', { n: more })}
              </li>
            )}
          </ul>
        )}
      </div>
    </WidgetFrame>
  )
}
