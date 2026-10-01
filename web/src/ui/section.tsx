import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { NumberTag } from './numbered-label'

interface SectionProps {
  no: string
  title: string
  // 标题栏右侧的补充信息
  meta?: ReactNode
  id?: string
  className?: string
  children: ReactNode
}

// 编号分区：细线描边的面板，标题栏是编号 + 标题 + 右侧补充信息
export function Section({ no, title, meta, id, className, children }: SectionProps) {
  return (
    <section id={id} aria-label={title} className={cn('min-w-0 rounded-[2px] border border-line-strong bg-card', className)}>
      <div className="flex min-h-10 items-center gap-2.5 border-b border-border px-4 py-1.5">
        <NumberTag no={no} />
        <h2 className="text-[14px] font-medium">{title}</h2>
        {meta && <span className="ml-auto min-w-0 truncate text-[12px] text-muted-foreground">{meta}</span>}
      </div>
      {children}
    </section>
  )
}

interface FormRowProps {
  label: ReactNode
  // 字段的机器名，等宽小字
  fieldKey?: string
  // 控件 id，用于 label 关联
  htmlFor?: string
  required?: boolean
  // 行右上的「已修改」标记
  changed?: boolean
  changedLabel?: string
  className?: string
  children: ReactNode
}

// 表单行：左侧标签 + 字段名，右侧控件；窄屏上下排列
export function FormRow({ label, fieldKey, htmlFor, required, changed, changedLabel, className, children }: FormRowProps) {
  return (
    <div
      data-changed={changed ? 'true' : undefined}
      className={cn('grid grid-cols-[180px_minmax(0,1fr)] gap-4 border-t border-border px-4 py-3 first:border-t-0 mobile:grid-cols-1 mobile:gap-1.5', className)}
    >
      <div className="text-[13px]">
        <label htmlFor={htmlFor} className="font-medium">
          {label}
          {required && <span className="ml-0.5 text-status-crit">*</span>}
        </label>
        {fieldKey && <div className="font-mono text-[10.5px] text-muted-foreground">{fieldKey}</div>}
        {changed && changedLabel && (
          <div className="mt-1.5 inline-flex items-center gap-1 font-mono text-[10.5px] text-signal-text">
            <span aria-hidden className="size-1.5 rounded-full bg-signal" />
            {changedLabel}
          </div>
        )}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

export function FieldHelp({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('mt-1.5 text-[11.5px] leading-[1.55] text-muted-foreground', className)}>{children}</div>
}

export function FieldError({ children, id }: { children: ReactNode; id?: string }) {
  return (
    <div id={id} role="alert" className="mt-1.5 text-[11.5px] text-status-crit">
      {children}
    </div>
  )
}
