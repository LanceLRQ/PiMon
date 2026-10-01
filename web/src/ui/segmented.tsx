import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export interface SegmentedOption<V extends string> {
  value: V
  // 按钮内容（文字或图标）
  label: ReactNode
  // 可访问名称与悬浮提示
  title: string
}

interface SegmentedProps<V extends string> {
  options: SegmentedOption<V>[]
  value: V
  onChange: (value: V) => void
  ariaLabel: string
  className?: string
}

// 分段选择：细线描边，选中项反色；按单选组暴露给辅助技术
export function Segmented<V extends string>({ options, value, onChange, ariaLabel, className }: SegmentedProps<V>) {
  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      className={cn('inline-flex overflow-hidden rounded-[2px] border border-line-strong bg-card', className)}
    >
      {options.map((o, i) => {
        const on = o.value === value
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={o.title}
            title={o.title}
            onClick={() => onChange(o.value)}
            className={cn(
              'flex h-7 flex-1 items-center justify-center gap-1 px-2.5 text-xs whitespace-nowrap outline-none focus-visible:ring-2 focus-visible:ring-ring',
              i > 0 && 'border-l border-border',
              on ? 'bg-inv-bg text-inv-ink' : 'text-ink-2 hover:bg-panel-2',
            )}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
