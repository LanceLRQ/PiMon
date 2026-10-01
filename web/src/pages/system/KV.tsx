import type { ReactNode } from 'react'

// 键值行：左侧名称，右侧等宽取值
export function KV({ rows }: { rows: { k: string; v: ReactNode }[] }) {
  return (
    <dl>
      {rows.map((r) => (
        <div key={r.k} className="flex items-start justify-between gap-3 border-t border-border px-4 py-2 text-[13px] first:border-t-0">
          <dt className="shrink-0 text-muted-foreground">{r.k}</dt>
          <dd className="min-w-0 text-right font-mono text-[12.5px] break-words">{r.v}</dd>
        </div>
      ))}
    </dl>
  )
}
