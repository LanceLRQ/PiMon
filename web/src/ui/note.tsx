import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export type NoteTone = 'info' | 'warn' | 'crit'

interface NoteProps {
  tone?: NoteTone
  icon?: ReactNode
  children: ReactNode
  className?: string
  role?: 'alert' | 'status'
}

const toneClass: Record<NoteTone, string> = {
  info: 'border-border bg-card',
  warn: 'border-status-warn bg-card',
  crit: 'border-status-crit bg-card',
}

const iconClass: Record<NoteTone, string> = {
  info: 'text-muted-foreground',
  warn: 'text-status-warn',
  crit: 'text-status-crit',
}

// 提示条：细线描边，左侧图标按语气着色
export function Note({ tone = 'info', icon, children, className, role }: NoteProps) {
  return (
    <div
      role={role}
      data-tone={tone}
      className={cn('flex items-start gap-2.5 rounded-[2px] border px-3.5 py-2.5 text-[13px] leading-[1.55]', toneClass[tone], className)}
    >
      {icon && <span className={cn('mt-0.5 flex-none', iconClass[tone])}>{icon}</span>}
      <div className="min-w-0">{children}</div>
    </div>
  )
}
