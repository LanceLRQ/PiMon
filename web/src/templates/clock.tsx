import { useMemo } from 'react'
import { localeFor, safeTimeZone } from './format'
import { useScreenEnv, useScreenNow } from './env'
import { WidgetFrame } from './frame'
import { layoutVariant, type LayoutVariant } from './size'
import type { TemplateProps } from './types'

// clock 模板：服务器时间 + 全局时区（Ruling 33，不用浏览器本地时区）。结构契约：
//   .tpl-clock[data-variant] > .tpl-clock__time（等宽数字）> .tpl-clock__hour : .tpl-clock__minute [: .tpl-clock__second] [.tpl-clock__period]
//   .tpl-clock__date（wide/large 且 show_date 不为 false）
// 选项：format 24h|12h（默认 24h）、show_seconds（默认 false，只在 4x2 生效）、show_date（默认 true，1x1 忽略）。
// 尺寸：1x1 只有时分；2x1 时分 + 月日周；4x2 时分（可含秒）+ 年月日周。
// 冒号不闪烁（设计 10.4：去掉呼吸与闪烁动画）。statusMode=static，无状态标记与灰显。

const timeSize: Record<LayoutVariant, string> = {
  compact: '--size-value-md',
  wide: '--size-value-lg',
  large: '--size-value-xl',
}

export function ClockTemplate({ widget, data }: TemplateProps) {
  const { timezone, lang } = useScreenEnv()
  const nowMs = useScreenNow(1000)
  const variant = layoutVariant(widget.size)
  const opts = widget.options
  const h12 = opts.format === '12h'
  const seconds = variant === 'large' && opts.show_seconds === true
  const showDate = variant !== 'compact' && opts.show_date !== false
  const zone = safeTimeZone(timezone)
  const locale = localeFor(lang)

  const timeFmt = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        timeZone: zone,
        hour: h12 ? 'numeric' : '2-digit',
        minute: '2-digit',
        second: seconds ? '2-digit' : undefined,
        hourCycle: h12 ? 'h12' : 'h23',
      }),
    [locale, zone, h12, seconds],
  )
  const dateFmt = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        timeZone: zone,
        year: variant === 'large' ? 'numeric' : undefined,
        month: lang === 'zh' ? 'long' : 'short',
        day: 'numeric',
        weekday: 'short',
      }),
    [locale, zone, variant, lang],
  )
  const part = (type: Intl.DateTimeFormatPartTypes) => timeFmt.formatToParts(nowMs).find((p) => p.type === type)?.value ?? ''

  return (
    <WidgetFrame widget={widget} data={data} statusMode="static" hideHeader>
      <div className="tpl-clock flex h-full min-w-0 flex-col items-center justify-center" data-variant={variant}>
        <div
          className="tpl-clock__time flex items-baseline font-[family-name:var(--font-display)] leading-none font-semibold tabular-nums"
          style={{ fontSize: `var(${timeSize[variant]})` }}
        >
          <span className="tpl-clock__hour">{part('hour')}</span>
          <span className="tpl-clock__colon">:</span>
          <span className="tpl-clock__minute">{part('minute')}</span>
          {seconds && (
            <>
              <span className="tpl-clock__colon">:</span>
              <span className="tpl-clock__second">{part('second')}</span>
            </>
          )}
          {h12 && <span className="tpl-clock__period text-s-muted-fg ml-1.5 text-[length:var(--size-label)] font-normal">{part('dayPeriod')}</span>}
        </div>
        {showDate && (
          <div className="tpl-clock__date text-s-muted-fg mt-2 text-[length:var(--size-label)]">{dateFmt.format(nowMs)}</div>
        )}
      </div>
    </WidgetFrame>
  )
}
