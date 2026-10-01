import { Undo2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import type { Change } from './changes'

/** 一条改动的文字：类别、小组件名、前后值 */
export function ChangeText({ change }: { change: Change }) {
  const { t } = useTranslation()
  return (
    <>
      <span className="shrink-0 rounded-[2px] border border-border px-1.5 text-[11.5px] leading-[18px] text-muted-foreground">{t(`layoutEd.chg.kind.${change.kind}`)}</span>
      <span className="min-w-0 flex-1 truncate">
        {change.label}
        {change.from && <span className="ml-2 font-mono text-[11.5px] text-muted-foreground line-through">{change.from}</span>}
        {change.from && change.to && <span className="mx-1 text-muted-foreground">→</span>}
        {change.to && <span className={`font-mono text-[11.5px] ${change.from ? '' : 'ml-2 '}text-signal-text`}>{change.to}</span>}
        {change.detail && <span className="ml-2 font-mono text-[11.5px] text-muted-foreground">{change.detail}</span>}
      </span>
    </>
  )
}

interface ChangeListProps {
  changes: Change[]
  /** 不传则只读（对方改动、版本差异） */
  onUndo?: (key: string) => void
  empty?: string
  testId?: string
}

export function ChangeList({ changes, onUndo, empty, testId }: ChangeListProps) {
  const { t } = useTranslation()
  if (!changes.length) return <p className="px-3.5 py-2.5 text-[12.5px] text-muted-foreground">{empty}</p>
  return (
    <ol data-testid={testId}>
      {changes.map((c, i) => (
        <li key={c.key} data-change-key={c.key} className="flex min-h-9 items-center gap-2.5 border-b border-border px-3.5 py-1 text-[12.5px] last:border-b-0">
          <span className="w-5 shrink-0 font-mono text-[11px] text-muted-foreground">{String(i + 1).padStart(2, '0')}</span>
          <ChangeText change={c} />
          {onUndo && (
            <Button variant="ghost" size="sm" className="shrink-0 rounded-[2px]" onClick={() => onUndo(c.key)}>
              <Undo2 size={13} />
              {t('layoutEd.chg.undoOne')}
            </Button>
          )}
        </li>
      ))}
    </ol>
  )
}
