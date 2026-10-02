import type { CSSProperties, ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { ResolvedWidget } from '@/types/generated'
import { parseThreshold, primaryData, widgetOwnLevel } from './data'
import { useScreenEnv, useScreenNow } from './env'
import { formatStamp } from './format'
import { WidgetIcon } from './icons'
import { layoutVariant, sizeKey } from './size'
import {
  DimIcon, LevelIcon, StatusIcon, StatusMarker, levelSurface, placeholderIcons, resolveStatus,
  type DisplayStatus,
} from './status'
import { useThemeRuntime } from './theme-context'
import type { InstanceDataMap } from './types'

interface FrameProps {
  widget: ResolvedWidget
  data: InstanceDataMap
  /**
   * full：按展示状态画标记、灰显与时间戳；
   * static：数据在前端合成的小组件（时钟、文本）只响应占位态，不随实例状态变化。
   */
  statusMode?: 'full' | 'static'
  hideHeader?: boolean
  className?: string
  children: ReactNode
}

function resolveFrameStatus(widget: ResolvedWidget, reportStatus: string | undefined, mode: 'full' | 'static'): DisplayStatus | null {
  const s = resolveStatus(widget.display_state, reportStatus)
  if (mode === 'static') return s.kind === 'placeholder' ? s : null
  return s
}

/** 灰显态的徽章：底层级别的标记 + 原因图标 + 最后成功时间 */
function DimBadge({ status, lastSuccessAt }: { status: Extract<DisplayStatus, { kind: 'dim' }>; lastSuccessAt: string | null | undefined }) {
  const { t } = useTranslation()
  const { timezone, lang, now } = useScreenEnv()
  useScreenNow(30_000)
  const ms = lastSuccessAt ? Date.parse(lastSuccessAt) : Number.NaN
  const stamp = Number.isNaN(ms) ? t('screenWidget.never') : formatStamp(ms, now(), timezone, lang)
  return (
    <span className="flex shrink-0 items-center gap-1" aria-label={t(`screenWidget.${status.reason}`)}>
      <StatusMarker level={status.level} size={12} />
      <DimIcon reason={status.reason} size={12} />
      <time dateTime={Number.isNaN(ms) ? undefined : new Date(ms).toISOString()} className="tabular-nums text-s-muted-fg text-[length:var(--size-label)]">
        {stamp}
      </time>
    </span>
  )
}

function LevelBadge({ level }: { level: Extract<DisplayStatus, { kind: 'level' }>['level'] }) {
  const { t } = useTranslation()
  return (
    <span className="flex shrink-0 items-center gap-1" aria-label={t(`screenWidget.level.${level}`)}>
      <StatusMarker level={level} size={12} />
      <LevelIcon level={level} size={12} />
    </span>
  )
}

/**
 * 小组件外框：标题、图标、状态徽章与内容槽。
 * 外框属性 data-* 是测试与 D9 端到端断言的稳定入口。
 */
export function WidgetFrame({ widget, data, statusMode = 'full', hideHeader = false, className = '', children }: FrameProps) {
  const { t } = useTranslation()
  const { status: channels } = useThemeRuntime()
  const inst = primaryData(widget, data)
  const status = resolveFrameStatus(widget, inst?.report_status, statusMode)
  const variant = layoutVariant(widget.size)
  // 标题栏状态标记取实例整体状态；外框的强调色与底色只取该小组件自身绑定数据项的最高级别（Ruling 47）
  const badgeLevel = status && status.kind !== 'placeholder' ? status.level : null
  const level =
    status && status.kind !== 'placeholder' && statusMode === 'full'
      ? widgetOwnLevel(widget, data, parseThreshold(widget.options?.threshold), undefined)
      : null
  const weight = level ? channels[level].weight : null
  const dimmed = status?.kind === 'dim'
  const placeholder = status?.kind === 'placeholder' ? status.reason : null

  // 边框只用 border 简写：占位态与普通态之间切换时，不混用简写与展开写法，避免 React 报样式冲突
  const accent = level && level !== 'ok' && weight !== 'normal' ? `var(--status-${level}-color)` : 'var(--border)'
  const borderWidth = weight === 'strong' && level !== 'ok' ? '2px' : 'var(--border-card-width)'
  const style: CSSProperties = {
    border: placeholder ? 'var(--state-placeholder-border)' : `${borderWidth} var(--border-card-style) ${accent}`,
    borderRadius: 'var(--radius-card)',
    padding: variant === 'compact' ? 'calc(var(--card-pad) * 0.6)' : 'var(--card-pad)',
    boxShadow: placeholder ? 'none' : 'var(--effect-card-shadow)',
    fontFamily: 'var(--font-body)',
  }
  const surface = level ? (levelSurface[level] ?? 'bg-s-card') : 'bg-s-card'

  return (
    <section
      data-widget-frame=""
      data-widget-id={widget.id}
      data-template={widget.template}
      data-size={sizeKey(widget.size)}
      data-display-state={widget.display_state}
      data-status-level={badgeLevel ?? 'none'}
      data-accent-level={level ?? 'none'}
      data-weight={weight ?? 'none'}
      data-dimmed={dimmed ? 'true' : 'false'}
      data-placeholder={placeholder ?? undefined}
      className={`${placeholder ? 'bg-s-card' : surface} text-s-card-fg flex h-full w-full min-w-0 flex-col overflow-hidden ${className}`}
      style={style}
    >
      {!hideHeader && (
        <header className="mb-1 flex min-w-0 items-center gap-1.5">
          <WidgetIcon name={widget.options?.icon} className="text-s-muted-fg shrink-0" />
          {widget.title && (
            <span
              data-widget-title=""
              title={widget.title}
              className="text-s-muted-fg min-w-0 flex-1 truncate text-[length:var(--size-label)] font-[family-name:var(--font-label)]"
            >
              {widget.title}
            </span>
          )}
          {!widget.title && <span className="flex-1" />}
          {status?.kind === 'level' && <LevelBadge level={status.level} />}
          {status?.kind === 'dim' && <DimBadge status={status} lastSuccessAt={inst?.last_success_at} />}
        </header>
      )}
      {placeholder ? (
        <div className="text-s-muted-fg flex min-h-0 flex-1 flex-col items-center justify-center gap-1 text-center text-[length:var(--size-label)]">
          <StatusIcon name={placeholderIcons[placeholder][0]} icon={placeholderIcons[placeholder][1]} size={variant === 'compact' ? 18 : 24} />
          <span>{t(`screenWidget.${placeholder}`)}</span>
        </div>
      ) : (
        <div data-widget-body="" className="min-h-0 flex-1" style={dimmed ? { opacity: 'var(--state-dim-opacity)' } : undefined}>
          {children}
        </div>
      )}
    </section>
  )
}
