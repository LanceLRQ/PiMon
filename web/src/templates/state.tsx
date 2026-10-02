import { useTranslation } from 'react-i18next'
import { findItem, levelOfValue, slotRef } from './data'
import { WidgetFrame } from './frame'
import { usePluginText } from './plugin-text'
import { layoutVariant } from './size'
import { StatusMarker, levelText } from './status'
import type { TemplateProps } from './types'

// state 模板：一个状态项（ok/warning/critical/unknown）。结构契约：
//   .tpl-state[data-value-level] > .tpl-state__marker（StatusMarker）+ .tpl-state__label（+ .tpl-state__summary）
// 槽：plugin 来源用 state 槽，generic 来源用 value 槽。
// 文字：state.text 先查 plugin.<id>.<key>，查不到原样显示（Ruling 34）；没有文字时显示级别名。
// 尺寸：1x1 标记 + 文字上下排；2x1 左右排并带实例摘要。数据项缺失按 unknown 显示「未知」。

export function StateTemplate({ widget, data }: TemplateProps) {
  const { t } = useTranslation()
  const pluginText = usePluginText(widget.plugin_id)
  const variant = layoutVariant(widget.size)
  const ref = slotRef(widget, 'state', 'value')
  const item = findItem(ref, data)
  const level = item?.type === 'state' || item?.state ? levelOfValue(item.state) : 'unknown'
  const label = item?.text ? pluginText(item.text) : t(`screenWidget.level.${level}`)
  const summary = variant !== 'compact' ? data[ref?.instance_id ?? '']?.summary : undefined
  const wide = variant !== 'compact'

  return (
    <WidgetFrame widget={widget} data={data}>
      <div
        className={`tpl-state flex h-full min-w-0 items-center justify-center gap-2 ${wide ? 'flex-row' : 'flex-col'} ${levelText[level]}`}
        data-variant={variant}
        data-value-level={level}
      >
        <StatusMarker level={level} size={wide ? 32 : 28} className="tpl-state__marker" />
        <div className={`flex min-w-0 flex-col ${wide ? 'items-start' : 'items-center'}`}>
          <span className="tpl-state__label max-w-full truncate text-[length:var(--size-value-sm)] font-semibold">{label}</span>
          {summary && <span className="tpl-state__summary text-s-muted-fg max-w-full truncate text-[length:var(--size-label)]">{summary}</span>}
        </div>
      </div>
    </WidgetFrame>
  )
}
