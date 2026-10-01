import { ChevronDown } from 'lucide-react'
import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

export interface SelectProps extends ComponentProps<'select'> {
  invalid?: boolean
}

// 细线下拉框：用原生 select，保证键盘与移动端体验；右侧自绘箭头
export function Select({ className, invalid, children, ...props }: SelectProps) {
  return (
    <div className={cn('relative min-w-0', className)}>
      <select
        aria-invalid={invalid || undefined}
        className={cn(
          'h-8 w-full appearance-none rounded-[2px] border border-line-strong bg-card pr-8 pl-2.5 text-[13px] outline-none',
          'focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
          invalid && 'border-status-crit shadow-[inset_0_0_0_1px_var(--status-crit)]',
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown size={14} aria-hidden className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-muted-foreground" />
    </div>
  )
}
