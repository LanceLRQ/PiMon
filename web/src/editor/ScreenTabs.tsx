import { Pencil, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { LayoutScreen } from '@/types/generated'
import { MAX_SCREENS, MAX_SCREEN_NAME_RUNES, normalizeScreenName } from './screen-rules'

interface ScreenTabsProps {
  screens: LayoutScreen[]
  activeId: string
  canAdd: boolean
  /** 正在改名的 screen（新增后立刻进入改名） */
  editingId: string | null
  onEditingChange: (id: string | null) => void
  onSelect: (id: string) => void
  onAdd: () => void
  onRename: (id: string, name: string) => void
}

function NameInput({ screen, onDone, onRename }: { screen: LayoutScreen; onDone: () => void; onRename: (name: string) => void }) {
  const { t } = useTranslation()
  const [value, setValue] = useState(screen.name)
  const valid = normalizeScreenName(value) !== null
  const commit = () => {
    // 空名或超长不提交，保持原名
    const n = normalizeScreenName(value)
    if (n) onRename(n)
    onDone()
  }
  return (
    <input
      autoFocus
      aria-label={t('layoutEd.screenTab.nameLabel')}
      aria-invalid={!valid}
      value={value}
      maxLength={MAX_SCREEN_NAME_RUNES}
      onChange={(e) => setValue(e.target.value)}
      onFocus={(e) => e.currentTarget.select()}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') commit()
        else if (e.key === 'Escape') {
          e.stopPropagation()
          onDone()
        }
      }}
      className={cn('h-8 w-[130px] rounded-[2px] border bg-card px-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring', valid ? 'border-foreground' : 'border-status-crit')}
    />
  )
}

// screen 标签：切换、新增、重命名。停留秒数、轮播、删除与排序在 screens 页。
export function ScreenTabs({ screens, activeId, canAdd, editingId, onEditingChange, onSelect, onAdd, onRename }: ScreenTabsProps) {
  const { t } = useTranslation()
  return (
    <div role="tablist" aria-label={t('layoutEd.tabs')} className="flex flex-wrap items-center gap-1">
      {screens.map((s) =>
        editingId === s.id ? (
          <NameInput key={s.id} screen={s} onDone={() => onEditingChange(null)} onRename={(name) => onRename(s.id, name)} />
        ) : (
          <span key={s.id} className="inline-flex">
            <button
              type="button"
              role="tab"
              aria-selected={s.id === activeId}
              onClick={() => onSelect(s.id)}
              onDoubleClick={() => onEditingChange(s.id)}
              className={cn(
                'inline-flex h-8 items-center gap-1.5 rounded-[2px] border px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
                s.id === activeId ? 'border-foreground bg-inv-bg text-inv-ink' : 'border-border hover:bg-panel-2',
              )}
            >
              <span className="font-mono text-[11px] opacity-70">{s.id}</span>
              {s.name}
            </button>
            {s.id === activeId && (
              <button
                type="button"
                aria-label={t('layoutEd.screenTab.rename')}
                title={t('layoutEd.screenTab.rename')}
                onClick={() => onEditingChange(s.id)}
                className="grid size-8 place-items-center rounded-[2px] text-muted-foreground outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring"
              >
                <Pencil size={13} />
              </button>
            )}
          </span>
        ),
      )}
      <button
        type="button"
        aria-label={t('layoutEd.screenTab.add')}
        title={canAdd ? t('layoutEd.screenTab.add') : t('layoutEd.screenTab.full', { max: MAX_SCREENS })}
        disabled={!canAdd}
        onClick={onAdd}
        className="grid size-8 place-items-center rounded-[2px] border border-dashed border-border outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
      >
        <Plus size={14} />
      </button>
    </div>
  )
}
