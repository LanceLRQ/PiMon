import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { NumberTag } from '@/ui/numbered-label'

interface PaneProps {
  no: string
  title: string
  meta?: ReactNode
  className?: string
  children: ReactNode
}

// 三栏布局里的一栏：编号 + 标题 + 右侧说明；宽屏下栏内独立滚动
export function Pane({ no, title, meta, className, children }: PaneProps) {
  return (
    <section className={cn('flex min-h-0 min-w-0 flex-col border-border bg-card', className)}>
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-4">
        <NumberTag no={no} />
        <h2 className="text-[13.5px] font-medium whitespace-nowrap">{title}</h2>
        <span className="flex-1" />
        {meta && <span className="min-w-0 truncate font-mono text-[11px] text-muted-foreground">{meta}</span>}
      </header>
      {children}
    </section>
  )
}
