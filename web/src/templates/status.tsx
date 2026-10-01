import type { ReactNode } from 'react'
import { CircleHelp, CircleX, Clock, OctagonAlert, TriangleAlert, Unlink, CirclePlus, Wrench, WifiOff, type LucideIcon } from 'lucide-react'
import { statusLevels, type StatusLevel } from '@/themes/types'
import { useThemeRuntime } from './theme-context'

// 屏幕端主题化状态组件（Ruling 15、设计 10.3）：
//   ok/warning/critical/unknown/error —— 主题的 marker（形）+ weight（强调）+ 色；
//   stale/offline/maintenance —— 灰显叠加（--state-dim-opacity）+ 时间戳 + 图标，保留底层 report_status 的 marker；
//   unconfigured/broken —— 占位卡（--state-placeholder-border）。

export type DimReason = 'stale' | 'offline' | 'maintenance'
export type PlaceholderReason = 'unconfigured' | 'broken'

export type DisplayStatus =
  | { kind: 'level'; level: StatusLevel }
  | { kind: 'dim'; level: StatusLevel; reason: DimReason }
  | { kind: 'placeholder'; reason: PlaceholderReason }

export function resolveStatus(displayState: string, reportStatus: string | undefined): DisplayStatus {
  if ((statusLevels as readonly string[]).includes(displayState)) {
    return { kind: 'level', level: displayState as StatusLevel }
  }
  if (displayState === 'stale' || displayState === 'offline' || displayState === 'maintenance') {
    const level = reportStatus === 'ok' || reportStatus === 'warning' || reportStatus === 'critical' ? reportStatus : 'unknown'
    return { kind: 'dim', level, reason: displayState }
  }
  if (displayState === 'unconfigured' || displayState === 'broken') {
    return { kind: 'placeholder', reason: displayState }
  }
  return { kind: 'level', level: 'unknown' }
}

// 类名必须整串写出，Tailwind 才能扫描到
export const levelText: Record<StatusLevel, string> = {
  ok: 'text-s-ok',
  warning: 'text-s-warning',
  critical: 'text-s-critical',
  unknown: 'text-s-unknown',
  error: 'text-s-error',
}
export const levelBg: Record<StatusLevel, string> = {
  ok: 'bg-s-ok',
  warning: 'bg-s-warning',
  critical: 'bg-s-critical',
  unknown: 'bg-s-unknown',
  error: 'bg-s-error',
}
export const levelFg: Record<StatusLevel, string> = {
  ok: 'text-s-ok-fg',
  warning: 'text-s-warning-fg',
  critical: 'text-s-critical-fg',
  unknown: 'text-s-unknown-fg',
  error: 'text-s-error-fg',
}
export const levelStroke: Record<StatusLevel, string> = {
  ok: 'stroke-s-ok',
  warning: 'stroke-s-warning',
  critical: 'stroke-s-critical',
  unknown: 'stroke-s-unknown',
  error: 'stroke-s-error',
}
export const levelBorder: Record<StatusLevel, string> = {
  ok: 'border-s-ok',
  warning: 'border-s-warning',
  critical: 'border-s-critical',
  unknown: 'border-s-unknown',
  error: 'border-s-error',
}
export const levelSurface: Partial<Record<StatusLevel, string>> = {
  warning: 'bg-s-warning-bg',
  critical: 'bg-s-critical-bg',
}

const levelIcons: Record<Exclude<StatusLevel, 'ok'>, [string, LucideIcon]> = {
  warning: ['triangle-alert', TriangleAlert],
  critical: ['octagon-alert', OctagonAlert],
  unknown: ['circle-help', CircleHelp],
  error: ['circle-x', CircleX],
}
const dimIcons: Record<DimReason, [string, LucideIcon]> = {
  stale: ['clock', Clock],
  offline: ['wifi-off', WifiOff],
  maintenance: ['wrench', Wrench],
}
export const placeholderIcons: Record<PlaceholderReason, [string, LucideIcon]> = {
  unconfigured: ['circle-plus', CirclePlus],
  broken: ['unlink', Unlink],
}

export function StatusIcon({ name, icon: Icon, size = 14, className }: { name: string; icon: LucideIcon; size?: number; className?: string }) {
  return (
    <span data-status-icon={name} aria-hidden="true" className={className ?? 'inline-flex shrink-0'}>
      <Icon size={size} strokeWidth={2.25} />
    </span>
  )
}

/** 级别的辅助图标；ok 不画图标，保持安静 */
export function LevelIcon({ level, size }: { level: StatusLevel; size?: number }): ReactNode {
  if (level === 'ok') return null
  const [name, icon] = levelIcons[level]
  return <StatusIcon name={name} icon={icon} size={size} />
}

export function DimIcon({ reason, size }: { reason: DimReason; size?: number }) {
  const [name, icon] = dimIcons[reason]
  return <StatusIcon name={name} icon={icon} size={size} />
}

interface MarkerProps {
  level: StatusLevel
  size?: number
  className?: string
}

/**
 * 主题状态标记：形（marker）与强调（weight）读自主题运行时，色取该级别的屏幕颜色类。
 * strong 加粗描边；invert 反白成色块。
 */
export function StatusMarker({ level, size = 14, className = '' }: MarkerProps) {
  const { status } = useThemeRuntime()
  const { marker, weight } = status[level]
  const invert = weight === 'invert'
  const stroke = weight === 'strong' ? 2 : 0
  const common = { fill: marker === 'ring' ? 'none' : 'currentColor', stroke: 'currentColor', strokeWidth: marker === 'ring' ? 2 : stroke }
  let shape: ReactNode
  switch (marker) {
    case 'ring':
      shape = <circle cx="8" cy="8" r="5" {...common} />
      break
    case 'square':
      shape = <rect x="3" y="3" width="10" height="10" {...common} />
      break
    case 'triangle':
      shape = <polygon points="8,2 14.5,13.5 1.5,13.5" strokeLinejoin="round" {...common} />
      break
    case 'diamond':
      shape = <polygon points="8,1 15,8 8,15 1,8" strokeLinejoin="round" {...common} />
      break
    default:
      shape = <circle cx="8" cy="8" r="5" {...common} />
  }
  const color = invert ? `${levelBg[level]} ${levelFg[level]} rounded-[2px] p-[2px]` : levelText[level]
  return (
    <span
      data-marker={marker}
      data-level={level}
      data-weight={weight}
      aria-hidden="true"
      className={`inline-flex shrink-0 items-center justify-center ${color} ${className}`}
    >
      <svg width={size} height={size} viewBox="0 0 16 16">
        {shape}
      </svg>
    </span>
  )
}
