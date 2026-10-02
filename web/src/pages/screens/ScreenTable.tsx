import { GripVertical, LayoutGrid, Trash2 } from 'lucide-react'
import { useState, type DragEvent, type KeyboardEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { MAX_DWELL_SECONDS, MAX_SCREEN_NAME_RUNES, MIN_DWELL_SECONDS, normalizeScreenName } from '@/editor/screen-rules'
import { cn } from '@/lib/utils'
import type { LayoutScreen } from '@/types/generated'
import { Input } from '@/ui/input'
import { Switch } from '@/ui/switch'
import { Button } from '@/ui/button'

export const INDEX_ID = 'index'

/** 停留秒数输入：留空或 0 用全局默认值；有效值（3–3600 的整数）才提交，无效值只标红，失焦后还原 */
function DwellInput({ id, value, fallback, onCommit }: { id: string; value: number; fallback: number; onCommit: (seconds: number) => void }) {
  const { t } = useTranslation()
  const fmt = (n: number) => (n === 0 ? '' : String(n))
  const [text, setText] = useState(fmt(value))
  // 草稿里的值从外部变了（放弃改动、重新加载）：输入框跟着重置
  const [seen, setSeen] = useState(value)
  if (seen !== value) {
    setSeen(value)
    setText(fmt(value))
  }
  const parse = (s: string): number | null => {
    if (s.trim() === '') return 0
    const n = Number(s)
    return Number.isInteger(n) && n >= MIN_DWELL_SECONDS && n <= MAX_DWELL_SECONDS ? n : null
  }
  const parsed = parse(text)
  return (
    <div className="flex items-center gap-1.5">
      <Input
        type="number"
        inputMode="numeric"
        min={MIN_DWELL_SECONDS}
        max={MAX_DWELL_SECONDS}
        aria-label={t('screens.list.dwellLabel', { id })}
        title={parsed === null ? t('screens.list.dwellInvalid', { min: MIN_DWELL_SECONDS, max: MAX_DWELL_SECONDS }) : undefined}
        placeholder={t('screens.list.dwellPlaceholder', { n: fallback })}
        invalid={parsed === null}
        value={text}
        className="h-7 w-[112px] font-mono"
        onChange={(e) => {
          setText(e.target.value)
          const n = parse(e.target.value)
          if (n !== null) onCommit(n)
        }}
        onBlur={() => parsed === null && setText(fmt(value))}
      />
      <span className="text-[12px] text-muted-foreground">{t('screens.rotation.secondsUnit')}</span>
    </div>
  )
}

/** 名称：点击改名；回车或失焦提交，Esc 取消。校验沿用编辑器顶栏的规则（1–64 字符） */
function NameCell({ screen, onRename }: { screen: LayoutScreen; onRename: (name: string) => void }) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState(screen.name)
  const commit = () => {
    setEditing(false)
    const name = normalizeScreenName(text)
    if (name && name !== screen.name) onRename(name)
  }
  if (editing) {
    return (
      <Input
        autoFocus
        aria-label={t('screens.list.renameLabel', { id: screen.id })}
        className="h-7 w-[140px]"
        maxLength={MAX_SCREEN_NAME_RUNES}
        invalid={normalizeScreenName(text) === null}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') commit()
          if (e.key === 'Escape') setEditing(false)
        }}
      />
    )
  }
  return (
    <button
      type="button"
      title={t('screens.list.rename')}
      aria-label={t('screens.list.renameLabel', { id: screen.id })}
      className="rounded-[2px] px-1 text-left outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring"
      onClick={() => {
        setText(screen.name)
        setEditing(true)
      }}
    >
      {screen.name}
    </button>
  )
}

interface ScreenTableProps {
  screens: readonly LayoutScreen[]
  defaultDwell: number
  /** screen id → 上次修改的文字（「v23 · 10月1日 21:58」）；没有记录时缺省 */
  modified: Record<string, string>
  /** 未保存的新增 screen 没有版本记录 */
  unsavedIds: ReadonlySet<string>
  thumbs: Record<string, ReactNode>
  onDwell: (id: string, seconds: number) => void
  onRotation: (id: string, on: boolean) => void
  onRename: (id: string, name: string) => void
  onDelete: (id: string) => void
  onMove: (id: string, to: number) => void
  disabled?: boolean
}

export function ScreenTable({ screens, defaultDwell, modified, unsavedIds, thumbs, onDwell, onRotation, onRename, onDelete, onMove, disabled }: ScreenTableProps) {
  const { t } = useTranslation()
  const [dragId, setDragId] = useState<string | null>(null)
  const [overId, setOverId] = useState<string | null>(null)

  const onDragStart = (e: DragEvent<HTMLElement>, id: string) => {
    setDragId(id)
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', id)
  }
  const onDragOver = (e: DragEvent<HTMLElement>, id: string) => {
    if (!dragId) return
    e.preventDefault()
    setOverId(id)
  }
  const onDrop = (e: DragEvent<HTMLElement>, id: string) => {
    e.preventDefault()
    const to = screens.findIndex((s) => s.id === id)
    if (dragId && dragId !== id && to >= 0) onMove(dragId, to)
    setDragId(null)
    setOverId(null)
  }
  const onGripKey = (e: KeyboardEvent<HTMLButtonElement>, id: string, idx: number) => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
    e.preventDefault()
    onMove(id, idx + (e.key === 'ArrowUp' ? -1 : 1))
  }

  const th = 'px-3 py-2 text-left text-[11.5px] font-medium text-muted-foreground'
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[980px] whitespace-nowrap border-collapse text-[13px]" data-testid="screen-table">
        <thead>
          <tr className="border-b border-border">
            <th className="w-10" />
            <th className={cn(th, 'w-[196px] mobile:hidden')}>{t('screens.list.col.thumb')}</th>
            <th className={th}>{t('screens.list.col.screen')}</th>
            <th className={cn(th, 'w-[90px]')}>{t('screens.list.col.widgets')}</th>
            <th className={cn(th, 'w-[170px]')}>{t('screens.list.col.dwell')}</th>
            <th className={cn(th, 'w-[100px]')}>{t('screens.list.col.rotation')}</th>
            <th className={cn(th, 'w-[170px]')}>{t('screens.list.col.modified')}</th>
            <th className="w-[150px]" />
          </tr>
        </thead>
        <tbody>
          {screens.map((s, idx) => {
            const home = s.id === INDEX_ID
            return (
              <tr
                key={s.id}
                data-testid={`screen-row-${s.id}`}
                data-dragging={dragId === s.id ? 'true' : undefined}
                data-over={overId === s.id && dragId !== s.id ? 'true' : undefined}
                draggable={!home && !disabled}
                onDragStart={(e) => !home && onDragStart(e, s.id)}
                onDragOver={(e) => onDragOver(e, s.id)}
                onDrop={(e) => onDrop(e, s.id)}
                onDragEnd={() => {
                  setDragId(null)
                  setOverId(null)
                }}
                className={cn('border-b border-border last:border-b-0', !s.in_rotation && 'text-muted-foreground', overId === s.id && dragId !== s.id && 'bg-panel-2', dragId === s.id && 'opacity-50')}
              >
                <td className="px-2">
                  <button
                    type="button"
                    disabled={home || disabled}
                    aria-label={t('screens.list.grip', { id: s.id })}
                    title={home ? t('screens.list.homePinned') : t('screens.list.dragHint')}
                    onKeyDown={(e) => onGripKey(e, s.id, idx)}
                    className={cn('grid size-7 place-items-center rounded-[2px] outline-none focus-visible:ring-2 focus-visible:ring-ring', home ? 'cursor-not-allowed opacity-30' : 'cursor-grab text-muted-foreground hover:bg-panel-2')}
                  >
                    <GripVertical size={16} />
                  </button>
                </td>
                <td className="px-3 py-2 mobile:hidden">{thumbs[s.id]}</td>
                <td className="px-3 py-2">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-[12px]">{s.id}</span>
                    <NameCell screen={s} onRename={(name) => onRename(s.id, name)} />
                    {home && <span className="rounded-[2px] border border-signal px-1.5 text-[11px] leading-[18px] text-signal-text">{t('screens.list.homeTag')}</span>}
                  </div>
                </td>
                <td className="px-3 py-2 font-mono text-[12px]">{t('screens.list.widgets', { n: s.widgets.length })}</td>
                <td className="px-3 py-2">
                  <DwellInput id={s.id} value={s.dwell_seconds} fallback={defaultDwell} onCommit={(n) => onDwell(s.id, n)} />
                </td>
                <td className="px-3 py-2">
                  <Switch
                    checked={s.in_rotation}
                    disabled={home || disabled}
                    ariaLabel={t('screens.list.rotationLabel', { id: s.id })}
                    onChange={(on) => onRotation(s.id, on)}
                  />
                </td>
                <td className="px-3 py-2 font-mono text-[12px] text-muted-foreground">
                  {unsavedIds.has(s.id) ? t('screens.list.unsaved') : (modified[s.id] ?? t('screens.list.modifiedNone'))}
                </td>
                <td className="px-3 py-2">
                  <div className="flex items-center justify-end gap-1.5">
                    <Button asChild variant="outline" size="sm" className="rounded-[2px]">
                      <Link to={`/screens/editor?screen=${encodeURIComponent(s.id)}`}>
                        <LayoutGrid size={13} />
                        {t('screens.list.editLayout')}
                      </Link>
                    </Button>
                    {!home && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="rounded-[2px]"
                        disabled={disabled}
                        title={t('screens.list.delete', { id: s.id })}
                        aria-label={t('screens.list.delete', { id: s.id })}
                        onClick={() => onDelete(s.id)}
                      >
                        <Trash2 size={14} />
                      </Button>
                    )}
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
