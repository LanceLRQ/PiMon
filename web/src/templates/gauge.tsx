import { useTranslation } from 'react-i18next'
import { findItem, parseThreshold, readNumber, resolveValueLevel, slotRef } from './data'
import { useScreenEnv } from './env'
import { WidgetFrame } from './frame'
import { fitFontSize } from './fit'
import { formatNumber } from './format'
import { layoutVariant } from './size'
import { StatusMarker, levelBg, levelStroke, levelText } from './status'
import type { TemplateProps } from './types'
import { useBoxSize } from './use-box-size'

// gauge 模板：带量程的读数 + 进度。结构契约：
//   .tpl-gauge[data-variant] > [role=meter][aria-valuenow=百分比 0-100] > (.tpl-gauge__ring svg | .tpl-gauge__bar) 含 .tpl-gauge__fill
//   .tpl-gauge__reading[data-value-level?] > (.tpl-gauge__marker?) 数字 + .tpl-gauge__unit?
//   .tpl-gauge__summary（仅 large 且实例有摘要）
// 尺寸：1x1 环形 + 居中读数；2x1 横条在左、读数在右；2x2 大环形 + 摘要。
// 量程取数据项的 min/max，缺省 0–100；进度夹在 0–100%，读数保持真实值。
// 进度颜色按 Ruling 32，无阈值时用 primary（中性）。承载信息的小字用 muted-fg（Ruling 42）。

const RING_R = 42
const RING_STROKE = 9
// 圆环内缘直径占整环的比例；读数（含标记与单位）只用其中一条弦的宽度，留出余量避免压到线条
const RING_INNER = (RING_R * 2 - RING_STROKE) / 100
const READING_WIDTH_RATIO = RING_INNER * 0.82
const READING_MIN_PX = 8

export function GaugeTemplate({ widget, data, defaultThreshold }: TemplateProps) {
  const { t } = useTranslation()
  const { lang } = useScreenEnv()
  const variant = layoutVariant(widget.size)
  const ref = slotRef(widget, 'value')
  const item = findItem(ref, data)
  const value = item ? readNumber(item, ref?.field) : null
  const min = item?.min ?? 0
  const max = item?.max !== undefined && item.max > min ? item.max : min + 100
  const fraction = value === null ? null : Math.min(1, Math.max(0, (value - min) / (max - min)))
  const level = item && value !== null ? resolveValueLevel(item, ref?.field, parseThreshold(widget.options.threshold), defaultThreshold) : null
  const text = value === null ? '' : formatNumber(value, lang)
  const summary = variant === 'large' ? data[ref?.instance_id ?? '']?.summary : undefined
  const readingSize = variant === 'large' ? '--size-value-lg' : variant === 'wide' ? '--size-value-lg' : '--size-value-sm'
  // 圆环内的读数按环的实际大小缩小字号：1x1 的环只有五十来像素，固定字号会让读数压到线条上或被裁切
  const [ringRef, ringBox] = useBoxSize<HTMLDivElement>()
  const ringPx = ringBox ? Math.min(ringBox.width, ringBox.height) : 0
  const markerSize = variant === 'compact' ? 10 : 16
  let fitPx: number | undefined
  let fitMarker = markerSize
  if (variant !== 'wide' && ringPx > 0 && value !== null) {
    const budget = ringPx * READING_WIDTH_RATIO - (level ? markerSize + 4 : 0)
    fitPx = fitFontSize(`${text}${item?.unit ?? ''}`, { width: Math.max(1, budget), height: ringPx * RING_INNER * 0.5, max: 64, min: READING_MIN_PX })
    fitMarker = Math.min(markerSize, Math.max(8, Math.round(fitPx * 0.7)))
  }

  const reading =
    value === null ? (
      <span className="tpl-gauge__unknown text-s-muted-fg text-[length:var(--size-value-sm)]">{t('screenWidget.unknown')}</span>
    ) : (
      <div
        className={`tpl-gauge__reading flex items-baseline justify-center gap-1 ${level ? levelText[level] : 'text-s-fg'} font-[family-name:var(--font-numeric)] leading-none font-semibold tabular-nums`}
        data-value-level={level ?? undefined}
        style={{ fontSize: fitPx === undefined ? `var(${readingSize})` : `min(var(${readingSize}), ${fitPx}px)` }}
      >
        {level && <StatusMarker level={level} size={fitMarker} className="tpl-gauge__marker self-center" />}
        <span>{text}</span>
        {item?.unit && <span className="tpl-gauge__unit text-s-muted-fg text-[length:var(--size-label)] font-normal">{item.unit}</span>}
      </div>
    )

  const meterProps =
    fraction === null
      ? {}
      : {
          role: 'meter',
          'aria-label': widget.title,
          'aria-valuemin': 0,
          'aria-valuemax': 100,
          'aria-valuenow': Math.round(fraction * 100),
          'aria-valuetext': `${text}${item?.unit ?? ''}`,
        }

  return (
    <WidgetFrame widget={widget} data={data}>
      <div className="tpl-gauge flex h-full min-w-0 items-center justify-center gap-3" data-variant={variant}>
        {variant === 'wide' ? (
          <>
            <div className="flex min-w-0 flex-1 items-center" {...meterProps}>
              {fraction !== null && (
                <div className="tpl-gauge__bar bg-s-muted h-2 w-full overflow-hidden rounded-full">
                  <div
                    className={`tpl-gauge__fill h-full ${level ? levelBg[level] : 'bg-s-primary'}`}
                    style={{ width: `${fraction * 100}%` }}
                  />
                </div>
              )}
            </div>
            <div className="shrink-0">{reading}</div>
          </>
        ) : (
          <div ref={ringRef} className="relative flex h-full max-h-full min-h-0 min-w-0 flex-col items-center justify-center" style={{ aspectRatio: '1 / 1' }}>
            {fraction !== null && (
              <svg viewBox="0 0 100 100" className="tpl-gauge__ring absolute inset-0 h-full w-full" {...meterProps}>
                <circle cx="50" cy="50" r={RING_R} fill="none" strokeWidth={RING_STROKE} className="stroke-s-chart-grid" />
                <circle
                  cx="50" cy="50" r={RING_R} fill="none" strokeWidth={RING_STROKE} strokeLinecap="round" pathLength="100"
                  strokeDasharray={`${fraction * 100} 100`}
                  transform="rotate(-90 50 50)"
                  className={`tpl-gauge__fill ${level ? levelStroke[level] : 'stroke-s-primary'}`}
                />
              </svg>
            )}
            <div className="relative z-10 flex w-[68%] flex-col items-center">
              {reading}
              {summary && <div className="tpl-gauge__summary text-s-muted-fg mt-1 max-w-full truncate text-[length:var(--size-label)]">{summary}</div>}
            </div>
          </div>
        )}
      </div>
    </WidgetFrame>
  )
}
