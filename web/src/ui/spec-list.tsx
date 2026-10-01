import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export interface SpecRow {
  label: ReactNode
  value: ReactNode
  // 强调该行取值（琥珀文字），如未保存的改动
  highlight?: boolean
}

interface SpecListProps {
  rows: SpecRow[]
  className?: string
}

// 细线参数表：标签靠左、取值靠右且等宽，行间用 1px 点线分隔
export function SpecList({ rows, className }: SpecListProps) {
  return (
    <dl className={cn('flex flex-col', className)}>
      {rows.map((row, i) => (
        <div
          key={i}
          className="flex h-[21px] items-center justify-between gap-2 border-b border-dotted border-border text-[12.5px] last:border-b-0"
        >
          <dt className="text-muted-foreground">{row.label}</dt>
          <dd className={cn('font-mono text-xs tabular-nums', row.highlight && 'text-signal-text')}>{row.value}</dd>
        </div>
      ))}
    </dl>
  )
}
