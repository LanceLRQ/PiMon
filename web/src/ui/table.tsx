import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

// 细线表格：1px 边框、表头为内嵌灰底的等宽小字，不用阴影；窄屏时容器横向滚动
export function Table({ className, ...props }: ComponentProps<'table'>) {
  return (
    <div className="w-full overflow-x-auto rounded-[2px] border border-line-strong bg-card">
      <table className={cn('w-full border-collapse text-[13px]', className)} {...props} />
    </div>
  )
}

export function TableHeader({ className, ...props }: ComponentProps<'thead'>) {
  return <thead className={cn('bg-panel-2', className)} {...props} />
}

export function TableBody({ className, ...props }: ComponentProps<'tbody'>) {
  return <tbody className={className} {...props} />
}

export function TableRow({ className, dimmed, ...props }: ComponentProps<'tr'> & { dimmed?: boolean }) {
  return (
    <tr
      data-dimmed={dimmed ? 'true' : undefined}
      className={cn('border-t border-border first:border-t-0 hover:bg-panel-2/60', dimmed && 'opacity-60', className)}
      {...props}
    />
  )
}

export function TableHead({ className, ...props }: ComponentProps<'th'>) {
  return (
    <th
      className={cn('h-[26px] px-3 text-left font-mono text-[10.5px] font-normal lowercase text-muted-foreground', className)}
      {...props}
    />
  )
}

export function TableCell({ className, mono, ...props }: ComponentProps<'td'> & { mono?: boolean }) {
  return <td className={cn('h-9 px-3', mono && 'font-mono tabular-nums', className)} {...props} />
}
