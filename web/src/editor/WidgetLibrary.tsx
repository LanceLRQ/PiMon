import { GripVertical, Plus, Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { NumberTag } from '@/ui/numbered-label'
import { countByGroup, filterLibrary, type LibraryEntry, type LibraryGroup } from './library'

interface WidgetLibraryProps {
  entries: readonly LibraryEntry[]
  onAdd: (entry: LibraryEntry) => void
  onDragStart: (entry: LibraryEntry) => void
  onDragEnd: () => void
}

const groups: LibraryGroup[] = ['plugin', 'generic', 'aggregate']

/** 左栏：小组件库，分插件声明 / 通用 / 聚合三个 Tab，可搜索；条目可拖到画布，也可点「添加」放到第一个空位 */
export function WidgetLibrary({ entries, onAdd, onDragStart, onDragEnd }: WidgetLibraryProps) {
  const { t } = useTranslation()
  const [group, setGroup] = useState<LibraryGroup>('plugin')
  const [query, setQuery] = useState('')
  const counts = countByGroup(entries)
  const shown = filterLibrary(entries, group, query)

  return (
    <aside aria-label={t('layoutEd.lib.title')} className="flex min-h-0 flex-col border-r border-border bg-card">
      <div className="flex h-10 items-center gap-2.5 border-b border-border px-3.5">
        <NumberTag no="02.1" />
        <h2 className="text-[14px] font-medium">{t('layoutEd.lib.title')}</h2>
        <span className="ml-auto font-mono text-[12px] text-muted-foreground">{entries.length}</span>
      </div>
      <div className="relative border-b border-border p-2.5">
        <Search size={14} aria-hidden className="pointer-events-none absolute top-1/2 left-5 -translate-y-1/2 text-muted-foreground" />
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('layoutEd.lib.search')}
          aria-label={t('layoutEd.lib.search')}
          className="h-8 w-full rounded-[2px] border border-line-strong bg-card pr-2 pl-8 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
      </div>
      <div role="tablist" aria-label={t('layoutEd.lib.title')} className="flex border-b border-border">
        {groups.map((g) => (
          <button
            key={g}
            type="button"
            role="tab"
            id={`lib-tab-${g}`}
            aria-selected={group === g}
            aria-controls="lib-panel"
            onClick={() => setGroup(g)}
            className={cn(
              'flex-1 border-b-2 px-1 py-2 text-[12.5px] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset',
              group === g ? 'border-foreground font-medium' : 'border-transparent text-muted-foreground hover:bg-panel-2',
            )}
          >
            {t(`layoutEd.lib.tab.${g}`)} <span className="font-mono text-[11px]">{counts[g]}</span>
          </button>
        ))}
      </div>
      <div role="tabpanel" id="lib-panel" aria-labelledby={`lib-tab-${group}`} className="min-h-0 flex-1 overflow-y-auto">
        {shown.length === 0 ? (
          <p className="px-3.5 py-6 text-center text-[12.5px] text-muted-foreground">{t('layoutEd.lib.none')}</p>
        ) : (
          <ul>
            {shown.map((e) => (
              <li
                key={e.key}
                draggable
                data-testid={`lib-item-${e.key}`}
                onDragStart={(ev) => {
                  ev.dataTransfer.setData('text/plain', e.key)
                  ev.dataTransfer.effectAllowed = 'copy'
                  onDragStart(e)
                }}
                onDragEnd={onDragEnd}
                className="flex cursor-grab items-center gap-2.5 border-b border-border px-3.5 py-2.5 hover:bg-panel-2"
              >
                <GripVertical size={14} aria-hidden className="shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px] font-medium">{e.title}</div>
                  <div className="flex flex-wrap items-center gap-1 text-[11px] text-muted-foreground">
                    <span className="font-mono">{e.subtitle}</span>
                    {e.sizes.map((s) => (
                      <span key={`${s.cols}x${s.rows}`} className="rounded-[2px] border border-border px-1 font-mono">
                        {s.cols}×{s.rows}
                      </span>
                    ))}
                  </div>
                </div>
                <button
                  type="button"
                  aria-label={t('layoutEd.lib.add', { name: e.title })}
                  title={t('layoutEd.lib.add', { name: e.title })}
                  onClick={() => onAdd(e)}
                  className="grid size-7 shrink-0 place-items-center rounded-[2px] border border-line-strong outline-none hover:bg-card focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <Plus size={14} />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="border-t border-border px-3.5 py-2 text-[11.5px] text-muted-foreground">{t('layoutEd.lib.hint')}</div>
    </aside>
  )
}
