import { useTranslation } from 'react-i18next'
import type { Item } from '@/types/generated'
import { findItem, parseThreshold, readNumber, resolveValueLevel, slotRef } from './data'
import { useScreenEnv } from './env'
import { WidgetFrame } from './frame'
import { formatMoney, formatNumber } from './format'
import { usePluginText } from './plugin-text'
import { layoutVariant, type LayoutVariant } from './size'
import { StatusMarker, levelText } from './status'
import type { Lang, TemplateProps } from './types'
import { fitStyle, useFitText } from './use-fit-text'

// value 模板：单个读数。结构契约：
//   section[data-widget-frame] > header + [data-widget-body] > .tpl-value
//   .tpl-value > .tpl-value__reading[data-value-level?] > (.tpl-value__marker?) .tpl-value__number .tpl-value__unit?
//   .tpl-value__summary（仅 wide/large 且实例有摘要）
// 尺寸：1x1 只有读数；2x1 读数更大并带摘要；2x2 读数最大并带摘要。
// 承载信息的小字（单位、摘要）一律用 text-s-muted-fg（Ruling 42），不用 faint 色。

export interface Reading {
  text: string
  unit?: string
}

/** 把数据项换成读数；缺失（含数值字段缺失）返回 null，由调用方显示「未知」 */
export function readItem(item: Item, field: string | undefined, lang: Lang, pluginText: (s: string) => string): Reading | null {
  if (item.type === 'text') return item.text ? { text: pluginText(item.text) } : null
  if (item.type === 'state') return item.text ? { text: pluginText(item.text) } : null
  if (item.type === 'money' && !field) {
    return typeof item.amount === 'number' && Number.isFinite(item.amount)
      ? { text: formatMoney(item.amount, item.currency, lang) }
      : null
  }
  const v = readNumber(item, field)
  if (v === null) return null
  const unit = item.unit ?? (item.type === 'quota' && !field ? '%' : undefined)
  return { text: formatNumber(v, lang), unit }
}

const numberSize: Record<LayoutVariant, string> = {
  compact: '--size-value-md',
  wide: '--size-value-lg',
  large: '--size-value-xl',
}
const fitCap: Record<LayoutVariant, number> = { compact: 28, wide: 48, large: 80 }

export function ValueTemplate({ widget, data, defaultThreshold }: TemplateProps) {
  const { t } = useTranslation()
  const { lang } = useScreenEnv()
  const pluginText = usePluginText(widget.plugin_id)
  const variant = layoutVariant(widget.size)
  const ref = slotRef(widget, 'value')
  const item = findItem(ref, data)
  const reading = item ? readItem(item, ref?.field, lang, pluginText) : null
  const level = item && reading ? resolveValueLevel(item, ref?.field, parseThreshold(widget.options.threshold), defaultThreshold) : null
  const summary = variant !== 'compact' ? data[ref?.instance_id ?? '']?.summary : undefined
  const [fitRef, fitPx] = useFitText<HTMLDivElement>(reading ? reading.text + (reading.unit ?? '') : '', {
    max: fitCap[variant],
    min: 12,
    heightRatio: variant === 'large' ? 0.6 : 1,
  })

  return (
    <WidgetFrame widget={widget} data={data}>
      <div ref={fitRef} className="tpl-value flex h-full min-w-0 flex-col justify-center" data-variant={variant}>
        {reading ? (
          <div
            className={`tpl-value__reading flex min-w-0 items-baseline gap-1.5 ${level ? levelText[level] : 'text-s-fg'}`}
            data-value-level={level ?? undefined}
          >
            {level && <StatusMarker level={level} size={variant === 'compact' ? 12 : 18} className="tpl-value__marker self-center" />}
            <span
              className="tpl-value__number truncate font-[family-name:var(--font-numeric)] leading-none font-semibold tabular-nums"
              style={{ fontSize: `var(${numberSize[variant]})`, ...fitStyle(numberSize[variant], fitPx) }}
            >
              {reading.text}
            </span>
            {reading.unit && (
              <span className="tpl-value__unit text-s-muted-fg shrink-0 text-[length:var(--size-label)]">{reading.unit}</span>
            )}
          </div>
        ) : (
          <span className="tpl-value__unknown text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.unknown')}</span>
        )}
        {summary && (
          <div className="tpl-value__summary text-s-muted-fg mt-1.5 truncate text-[length:var(--size-label)]">{summary}</div>
        )}
      </div>
    </WidgetFrame>
  )
}
