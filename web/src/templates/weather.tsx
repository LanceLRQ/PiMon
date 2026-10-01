import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  ArrowUp, CircleHelp, Cloud, CloudDrizzle, CloudFog, CloudLightning, CloudMoon, CloudRain, CloudSnow, CloudSun,
  MapPin, Moon, Sun, type LucideIcon,
} from 'lucide-react'
import type { Item } from '@/types/generated'
import { findItem, primaryData, readNumber, slotRef } from './data'
import { useScreenEnv } from './env'
import { WidgetFrame } from './frame'
import { formatNumber } from './format'
import { usePluginText } from './plugin-text'
import type { Lang, TemplateProps } from './types'

// weather 模板：当前天气 + 湿度、风、明日预报（取决于尺寸）。结构契约：
//   .tpl-weather[data-variant] > .tpl-weather__icon[data-weather-icon] + .tpl-weather__temp + .tpl-weather__condition + .tpl-weather__location[title]
//   2x2、4x2 另有 .tpl-weather__humidity、.tpl-weather__wind（含 [data-wind-degrees]）、.tpl-weather__tomorrow
//   .tpl-weather__attribution 在角落（≥2x1 都显示，Ruling 27）
//   未选城市：.tpl-weather__setup 显示「请选择城市」占位（取实例数据里的 setup 项，不是 widget 槽）
// 槽（manifest 的 weather 小组件）：location、temperature、condition、icon、humidity、wind_speed、wind_direction、
//   tomorrow_high、tomorrow_low、tomorrow_icon、attribution。
// 尺寸：2x1 只有当前天气；2x2 竖排加湿度、风、明日；4x2 左右分栏，左当前右详情。
// condition 文本形如「局部多云 / Partly cloudy」，按界面语言取一半。
// 署名用 text-s-muted-fg 小字（Ruling 27、42）。风向不画历史图（Ruling 17）。

const icons: Record<string, LucideIcon> = {
  clear: Sun,
  'clear-night': Moon,
  'partly-cloudy': CloudSun,
  'partly-cloudy-night': CloudMoon,
  cloudy: Cloud,
  fog: CloudFog,
  drizzle: CloudDrizzle,
  rain: CloudRain,
  showers: CloudRain,
  snow: CloudSnow,
  thunderstorm: CloudLightning,
  unknown: CircleHelp,
}

function WeatherIcon({ name, size, className = '' }: { name: string | undefined; size: number; className?: string }) {
  const key = name && Object.hasOwn(icons, name) ? name : 'unknown'
  const Icon = icons[key]
  return (
    <span data-weather-icon={key} className={`tpl-weather__icon text-s-fg inline-flex shrink-0 ${className}`}>
      <Icon size={size} aria-hidden="true" />
    </span>
  )
}

/** Open-Meteo 要求的署名（CC BY 4.0）。小组件角落与详情层共用，只用 muted 色 */
export function OpenMeteoAttribution({ title, className = '' }: { title?: string; className?: string }) {
  const { t } = useTranslation()
  return (
    <span
      title={title}
      className={`tpl-weather__attribution text-s-muted-fg truncate text-[length:var(--size-label)] ${className}`}
    >
      {t('screenWidget.weather.attribution')}
    </span>
  )
}

function pickLang(text: string | undefined, lang: Lang): string | undefined {
  if (!text) return undefined
  const [zh, en] = text.split(' / ')
  return lang === 'en' ? (en ?? zh) : zh
}

function numberText(item: Item | undefined, lang: Lang, digits = 1): string | null {
  if (!item) return null
  const v = readNumber(item)
  return v === null ? null : `${formatNumber(v, lang, digits)}${item.unit ?? ''}`
}

export function WeatherTemplate({ widget, data }: TemplateProps) {
  const { t } = useTranslation()
  const { lang } = useScreenEnv()
  const pluginText = usePluginText(widget.plugin_id)
  const key = `${widget.size.cols}x${widget.size.rows}`
  const variant = widget.size.cols >= 4 ? 'xl' : widget.size.rows >= 2 ? 'large' : 'wide'
  const get = (slot: string) => findItem(slotRef(widget, slot), data)

  const temperature = get('temperature')
  const setup = primaryData(widget, data)?.items.find((i) => i.key === 'setup')
  if (!temperature && setup && setup.state !== 'ok') {
    return (
      <WidgetFrame widget={widget} data={data}>
        <div className="tpl-weather__setup text-s-muted-fg flex h-full flex-col items-center justify-center gap-1 text-center text-[length:var(--size-value-sm)]" data-variant={variant}>
          <MapPin size={variant === 'wide' ? 20 : 28} aria-hidden="true" />
          <span>{setup.text ? pluginText(setup.text) : t('screenWidget.unknown')}</span>
        </div>
      </WidgetFrame>
    )
  }

  const location = get('location')?.text
  const condition = pickLang(get('condition')?.text, lang)
  const iconName = get('icon')?.text
  const temp = numberText(temperature, lang)
  const humidity = numberText(get('humidity'), lang, 0)
  const windItem = get('wind_speed')
  const wind = numberText(windItem, lang)
  const windDeg = get('wind_direction') ? readNumber(get('wind_direction')!) : null
  const high = numberText(get('tomorrow_high'), lang, 0)
  const low = numberText(get('tomorrow_low'), lang, 0)
  const attributionTitle = get('attribution')?.text
  const unknown = t('screenWidget.unknown')

  const iconSize = variant === 'wide' ? 36 : variant === 'large' ? 40 : 56
  const tempEl = (
    <span
      className="tpl-weather__temp text-s-fg font-[family-name:var(--font-numeric)] leading-none font-semibold tabular-nums whitespace-nowrap"
      style={{ fontSize: `var(${variant === 'wide' ? '--size-value-lg' : '--size-value-xl'})` }}
    >
      {temp ?? <span className="text-s-muted-fg text-[length:var(--size-value-sm)] font-normal">{unknown}</span>}
    </span>
  )
  const textEl = (
    <div className="flex min-w-0 flex-col">
      {condition && <span className="tpl-weather__condition text-s-fg truncate text-[length:var(--size-value-sm)]">{condition}</span>}
      {location && (
        <span className="tpl-weather__location text-s-muted-fg truncate text-[length:var(--size-label)]" title={location}>
          {location}
        </span>
      )}
    </div>
  )

  const detail = (label: string, body: ReactNode, cls: string) => (
    <div className={`${cls} flex min-w-0 items-baseline gap-1.5 text-[length:var(--size-label)]`}>
      <span className="text-s-muted-fg shrink-0">{label}</span>
      <span className="text-s-fg truncate font-semibold tabular-nums">{body}</span>
    </div>
  )
  const details = variant !== 'wide' && (
    <div className="flex min-w-0 flex-col gap-1">
      {detail(t('screenWidget.weather.humidity'), humidity ?? unknown, 'tpl-weather__humidity')}
      {detail(
        t('screenWidget.weather.wind'),
        <span className="inline-flex items-baseline gap-1">
          {windDeg !== null && (
            <span data-wind-degrees={windDeg} className="inline-flex self-center" style={{ transform: `rotate(${(windDeg + 180) % 360}deg)` }}>
              <ArrowUp size={12} aria-hidden="true" />
            </span>
          )}
          {wind ?? unknown}
        </span>,
        'tpl-weather__wind',
      )}
      <div className="tpl-weather__tomorrow flex min-w-0 items-center gap-1.5 text-[length:var(--size-label)]">
        <span className="text-s-muted-fg shrink-0">{t('screenWidget.weather.tomorrow')}</span>
        <WeatherIcon name={get('tomorrow_icon')?.text} size={16} />
        <span className="text-s-fg truncate font-semibold tabular-nums">{high && low ? `${low} ~ ${high}` : (high ?? low ?? unknown)}</span>
      </div>
    </div>
  )

  const current = (
    <div className="flex min-w-0 items-center gap-3">
      <WeatherIcon name={iconName} size={iconSize} />
      {tempEl}
      {variant === 'wide' && textEl}
    </div>
  )

  return (
    <WidgetFrame widget={widget} data={data}>
      <div className="tpl-weather flex h-full min-h-0 min-w-0 flex-col" data-variant={variant} data-size={key}>
        <div className={`flex min-h-0 flex-1 ${variant === 'xl' ? 'flex-row items-center justify-between gap-6' : 'flex-col justify-center gap-2'}`}>
          {current}
          {variant !== 'wide' && textEl}
          {details}
        </div>
        <OpenMeteoAttribution title={attributionTitle} className="self-end" />
      </div>
    </WidgetFrame>
  )
}
