import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import { useTranslation } from 'react-i18next'
import type { Item } from '@/types/generated'
import { findItem, slotRef } from './data'
import { useScreenEnv } from './env'
import { WidgetFrame } from './frame'
import { formatStamp } from './format'
import { parseRange, useHistorySeries } from './history'
import { useItemDescriber } from './item-view'
import { layoutVariant } from './size'
import { StatusMarker, levelText } from './status'
import type { TemplateProps } from './types'
import { useBoxSize } from './use-box-size'

// chart 模板：一个数值数据项的迷你趋势。结构契约：
//   .tpl-chart[data-chart-state] > .tpl-chart__reading（当前读数）+ .tpl-chart__plot（svg 趋势 或 .tpl-chart__message）
//   large 尺寸另有 .tpl-chart__range（区间最小、最大值，4x2 再带起止时刻）
// 槽：generic 来源的 value 槽。取数经 HistoryContext 注入的提供者，挂载取一次、之后每 5 分钟刷新；模板不自己发请求。
// 状态 data-chart-state：unavailable（无提供者）、loading、ready、empty（点数不足）、error（取数失败且无旧曲线）、
//   unsupported（文本、状态、表格项与 wind_direction 没有历史曲线）。降级态显示「暂无历史数据」，不报 console.error。
// 配色只用 L2 图表 token（--chart-1 线条与面积、--chart-grid 网格）。
// 尺寸：2x1 读数在右、趋势在左；2x2 读数在上、趋势居中、区间在下；4x2 同 2x2 并带网格与起止时刻。

const FALLBACK_PLOT = { width: 160, height: 56 }

function chartable(item: Item | undefined): boolean {
  if (!item) return true
  if (item.key === 'wind_direction') return false
  return item.type === 'number' || item.type === 'gauge' || item.type === 'quota' || item.type === 'money'
}

export function ChartTemplate({ widget, data, defaultThreshold }: TemplateProps) {
  const { t } = useTranslation()
  const { lang, timezone, now } = useScreenEnv()
  const d = useItemDescriber(widget, defaultThreshold)
  const ref = slotRef(widget, 'value')
  const item = findItem(ref, data)
  const supported = chartable(item)
  const range = parseRange(widget.options.range)
  const series = useHistorySeries(ref, range, supported)
  const state = supported ? series.state : 'unsupported'
  const [plotRef, box] = useBoxSize<HTMLDivElement>()
  const variant = layoutVariant(widget.size)
  const wide = variant === 'wide'
  const big = widget.size.cols >= 4

  const view = ref ? d.describe(ref, data) : { name: '', text: null, level: null, unit: undefined, item: undefined }
  const points = series.points.map((p) => ({ t: p.t, v: p.avg }))
  const plot = box ?? FALLBACK_PLOT
  const values = points.map((p) => p.v)
  const lo = values.length ? Math.min(...values) : 0
  const hi = values.length ? Math.max(...values) : 0
  const fmt = (v: number) => new Intl.NumberFormat(lang === 'zh' ? 'zh-CN' : 'en-US', { maximumFractionDigits: 1 }).format(v)

  const reading = (
    <div
      className={`tpl-chart__reading flex items-baseline gap-1.5 font-[family-name:var(--font-numeric)] leading-none font-semibold tabular-nums whitespace-nowrap ${view.level ? levelText[view.level] : 'text-s-fg'}`}
      style={{ fontSize: `var(${wide ? '--size-value-md' : '--size-value-lg'})` }}
      data-value-level={view.level ?? undefined}
    >
      {view.level && view.text !== null && <StatusMarker level={view.level} size={wide ? 12 : 16} className="self-center" />}
      {view.text === null ? (
        <span className="text-s-muted-fg text-[length:var(--size-value-sm)] font-normal">{t('screenWidget.unknown')}</span>
      ) : (
        <>
          <span>{view.text}</span>
          {view.unit && <span className="text-s-muted-fg text-[length:var(--size-label)] font-normal">{view.unit}</span>}
        </>
      )}
    </div>
  )

  const plotBody =
    state === 'ready' ? (
      <AreaChart width={plot.width} height={plot.height} data={points} margin={{ top: 4, right: 2, bottom: 2, left: 2 }}>
        {big && <CartesianGrid vertical={false} stroke="var(--chart-grid)" strokeDasharray="3 3" />}
        <XAxis dataKey="t" hide type="number" domain={['dataMin', 'dataMax']} />
        <YAxis hide domain={['dataMin', 'dataMax']} />
        <Area type="monotone" dataKey="v" stroke="var(--chart-1)" fill="var(--chart-1)" fillOpacity={0.18} strokeWidth={2} dot={false} isAnimationActive={false} />
      </AreaChart>
    ) : (
      <span className="tpl-chart__message text-s-muted-fg flex h-full items-center text-[length:var(--size-label)]">
        {t(state === 'loading' ? 'screenWidget.chart.loading' : state === 'unsupported' ? 'screenWidget.chart.unsupported' : 'screenWidget.chart.empty')}
      </span>
    )

  const plotBox = (
    <div ref={plotRef} className="tpl-chart__plot min-h-0 min-w-0 flex-1 overflow-hidden">
      {plotBody}
    </div>
  )

  return (
    <WidgetFrame widget={widget} data={data}>
      <div className={`tpl-chart flex h-full min-w-0 ${wide ? 'flex-row items-center gap-3' : 'flex-col gap-1'}`} data-chart-state={state} data-variant={variant}>
        {wide ? (
          <>
            {plotBox}
            <div className="shrink-0">{reading}</div>
          </>
        ) : (
          <>
            {reading}
            {plotBox}
            {state === 'ready' && (
              <div className="tpl-chart__range text-s-muted-fg flex justify-between gap-2 text-[length:var(--size-label)] tabular-nums">
                {big && <span>{formatStamp(points[0].t, now(), timezone, lang)}</span>}
                <span>{`${fmt(lo)} – ${fmt(hi)}`}</span>
                {big && <span>{formatStamp(points[points.length - 1].t, now(), timezone, lang)}</span>}
              </div>
            )}
          </>
        )}
      </div>
    </WidgetFrame>
  )
}
