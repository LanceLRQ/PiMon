import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

// 9 种状态形状（设计 10.3，互不相同）。键与后端 display_state 取值一致。
export const shapeStates = [
  'ok',
  'warning',
  'critical',
  'unknown',
  'error',
  'stale',
  'offline',
  'unconfigured',
  'broken',
] as const

export type ShapeState = (typeof shapeStates)[number]

// 24×24 画布。实心形状（ok/warning/critical）靠填充，其余为描边线框。
export const shapeGeometry: Record<ShapeState, ReactNode> = {
  ok: <circle cx="12" cy="12" r="6" fill="currentColor" stroke="none" />,
  warning: <path d="M12 3.5 22 20.5H2Z" fill="currentColor" stroke="none" />,
  critical: <path d="M12 2.2 21.8 12 12 21.8 2.2 12Z" fill="currentColor" stroke="none" />,
  unknown: <circle cx="12" cy="12" r="7" />,
  error: (
    <>
      <circle cx="12" cy="12" r="9.5" />
      <path d="m15.5 8.5-7 7M8.5 8.5l7 7" />
    </>
  ),
  stale: (
    <>
      <circle cx="12" cy="12" r="9.5" />
      <path d="M12 7v5l3.5 2" />
    </>
  ),
  offline: (
    <>
      <path d="m19 5 3-3M2 22l3-3" />
      <path d="M6.3 20.3a2.4 2.4 0 0 0 3.4 0L12 18l-6-6-2.3 2.3a2.4 2.4 0 0 0 0 3.4Z" />
      <path d="M7.5 13.5 10 11M10.5 16.5 13 14" />
      <path d="m12 6 6 6 2.3-2.3a2.4 2.4 0 0 0 0-3.4l-2.6-2.6a2.4 2.4 0 0 0-3.4 0Z" />
    </>
  ),
  unconfigured: <rect x="4" y="4" width="16" height="16" rx="1" strokeDasharray="3.2 2.6" />,
  broken: <path d="M9 17H7A5 5 0 0 1 7 7M15 7h2a5 5 0 0 1 4 8M8 12h4M2 2l20 20" />,
}

const colorClass: Record<ShapeState, string> = {
  ok: 'text-status-ok',
  warning: 'text-status-warn',
  critical: 'text-status-crit',
  unknown: 'text-status-unknown',
  error: 'text-status-crit',
  stale: 'text-status-unknown',
  offline: 'text-status-unknown',
  unconfigured: 'text-status-unknown',
  broken: 'text-status-unknown',
}

// 把后端 display_state 归到某个形状：维护中沿用离线的形状（本期不会出现），未知取值回落「未知」
export function resolveShape(state: string): ShapeState {
  if (state === 'maintenance') return 'offline'
  return (shapeStates as readonly string[]).includes(state) ? (state as ShapeState) : 'unknown'
}

// 过期、离线等弱化态：整行灰显
export function isDimmedState(state: string): boolean {
  const s = resolveShape(state)
  return s === 'stale' || s === 'offline'
}

interface StatusShapeProps {
  state: string
  size?: number
  className?: string
}

// 色（语义色）+ 形（SVG 形状）+ 强调（严重加粗描边），不只靠颜色区分
export function StatusShape({ state, size = 14, className }: StatusShapeProps) {
  const { t } = useTranslation()
  const shape = resolveShape(state)
  const strong = shape === 'critical'
  return (
    <svg
      role="img"
      aria-label={t(`status.${state === 'maintenance' ? 'maintenance' : shape}`)}
      data-shape={shape}
      data-weight={strong ? 'strong' : 'normal'}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strong ? 3 : 2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn('shrink-0', colorClass[shape], isDimmedState(state) && 'opacity-70', className)}
    >
      {shapeGeometry[shape]}
    </svg>
  )
}

// 形状 + 文字标签；严重状态的文字加粗
export function StatusLabel({ state, className }: { state: string; className?: string }) {
  const { t } = useTranslation()
  const shape = resolveShape(state)
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-[13px]', shape === 'critical' && 'font-semibold', className)}>
      <StatusShape state={state} />
      <span aria-hidden="true">{t(`status.${state === 'maintenance' ? 'maintenance' : shape}`)}</span>
    </span>
  )
}
