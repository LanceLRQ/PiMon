import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface NumberTagProps {
  // 编号，如 01、01.1、p3、k2
  no: string
  highlight?: boolean
  className?: string
}

// 编号标签：等宽小字加细边框；highlight 时用琥珀填充（选中、联动高亮）
export function NumberTag({ no, highlight, className }: NumberTagProps) {
  return (
    <span
      data-highlight={highlight ? 'true' : undefined}
      className={cn(
        'inline-block rounded-[2px] border px-1 font-mono text-[10.5px] leading-[15px]',
        highlight ? 'border-signal bg-signal text-primary-foreground' : 'border-border text-muted-foreground',
        className,
      )}
    >
      {no}
    </span>
  )
}

interface SectionLabelProps {
  no: string
  children: ReactNode
  className?: string
}

// 编号分区标签：编号 + 说明文字，分区靠编号而不是卡片
export function SectionLabel({ no, children, className }: SectionLabelProps) {
  return (
    <div className={cn('mb-2 flex items-center gap-2 text-[12.5px] text-ink-2', className)}>
      <NumberTag no={no} />
      <span>{children}</span>
    </div>
  )
}
