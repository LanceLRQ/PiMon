import { Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { PluginInfo } from '@/types/generated'
import { Input } from '@/ui/input'
import { categoryOrder, pluginCategory } from './catalog'
import { PluginIcon } from './PluginIcon'

interface PluginCatalogProps {
  plugins: PluginInfo[]
  selectedId: string | null
  onSelect: (plugin: PluginInfo) => void
}

// 插件目录：按类别分组、可搜索，点选一个插件
export function PluginCatalog({ plugins, selectedId, onSelect }: PluginCatalogProps) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const groups = useMemo(() => {
    const q = query.trim().toLowerCase()
    const hit = plugins.filter((p) => q === '' || `${p.id} ${p.name}`.toLowerCase().includes(q))
    return categoryOrder
      .map((key) => ({ key, items: hit.filter((p) => pluginCategory(p) === key) }))
      .filter((g) => g.items.length > 0)
  }, [plugins, query])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="relative border-b border-border p-2.5">
        <Search size={14} aria-hidden className="pointer-events-none absolute top-1/2 left-5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('editor.catalog.search')}
          aria-label={t('editor.catalog.search')}
          className="pl-8"
        />
      </div>
      <div className="max-h-80 min-h-0 flex-1 overflow-y-auto min-[1100px]:max-h-none">
        {groups.length === 0 && <p className="p-4 text-[13px] text-muted-foreground">{t('editor.catalog.none')}</p>}
        {groups.map((g) => (
          <div key={g.key}>
            <div className="flex justify-between border-t border-border px-3 pt-2.5 pb-1.5 font-mono text-[11px] text-muted-foreground first:border-t-0">
              <span>{t(`editor.catalog.cat.${g.key}`)}</span>
              <span>{g.items.length}</span>
            </div>
            {g.items.map((p) => {
              const on = p.id === selectedId
              return (
                <button
                  key={p.id}
                  type="button"
                  aria-pressed={on}
                  onClick={() => onSelect(p)}
                  className={cn(
                    'grid w-full grid-cols-[32px_minmax(0,1fr)] items-center gap-2.5 border-t border-border px-3 py-2 text-left outline-none first:border-t-0 hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset',
                    on && 'bg-panel-2 shadow-[inset_3px_0_0_var(--signal)]',
                  )}
                >
                  <span className="grid size-8 place-items-center rounded-[3px] border border-line-strong bg-panel-2 text-ink-2">
                    <PluginIcon plugin={p} size={16} />
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate text-[13.5px] leading-tight">{p.name}</span>
                    <span className="mt-0.5 flex min-w-0 items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
                      <span className="truncate">{p.id}</span>
                      <Tag>{p.runs_on.join(' · ')}</Tag>
                      <Tag accent={p.origin === 'exec'}>{p.origin}</Tag>
                    </span>
                  </span>
                </button>
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}

function Tag({ children, accent }: { children: string; accent?: boolean }) {
  return (
    <span
      className={cn(
        'shrink-0 rounded-[2px] border px-1 text-[10px] leading-[15px]',
        accent ? 'border-signal text-signal-text' : 'border-border text-muted-foreground',
      )}
    >
      {children}
    </span>
  )
}
