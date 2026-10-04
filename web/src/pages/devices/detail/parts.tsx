import type { ReactNode } from 'react'
import { NumberTag } from '@/ui/numbered-label'

// 抽屉分区：编号 + 标题 + 右侧补充信息，分区之间只用细线分隔
export function DrawerSection({ no, title, meta, children }: { no: string; title: string; meta?: ReactNode; children: ReactNode }) {
  return (
    <section aria-label={title} className="border-t border-border first:border-t-0">
      <div className="flex min-h-9 items-center gap-2 px-4 pt-3 pb-1 text-[12.5px] text-ink-2">
        <NumberTag no={no} />
        <h3 className="font-medium">{title}</h3>
        {meta && <span className="ml-auto min-w-0 truncate text-[12px] text-muted-foreground">{meta}</span>}
      </div>
      {children}
    </section>
  )
}

export interface KvRow {
  label: string
  value: ReactNode
}

// 键值表：标签靠左，取值靠右，长取值（地址、指纹）换行显示
export function Kv({ rows }: { rows: KvRow[] }) {
  return (
    <dl className="px-4 pb-3 text-[12.5px]">
      {rows.map((r) => (
        <div key={r.label} className="flex min-h-[24px] items-start justify-between gap-3 border-b border-dotted border-border py-0.5 last:border-b-0">
          <dt className="shrink-0 text-muted-foreground">{r.label}</dt>
          <dd className="m-0 min-w-0 text-right font-mono text-xs break-all">{r.value}</dd>
        </div>
      ))}
    </dl>
  )
}
