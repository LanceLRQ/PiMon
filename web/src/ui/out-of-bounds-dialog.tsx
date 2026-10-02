import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'

/** 越界的一个小组件：与编辑器内部状态无关，调用方自己拼好显示文字 */
export interface OutOfBoundsItem {
  id: string
  /** 所在 screen，如「index 首页」 */
  screen: string
  /** 小组件名 */
  label: string
  /** 位置与尺寸，如「c7 r5 · 2×1」 */
  place: string
}

interface OutOfBoundsDialogProps {
  open: boolean
  /** 要缩小到的网格 */
  grid: { cols: number; rows: number }
  items: OutOfBoundsItem[]
  /** 用户确认：由调用方移除这些小组件并应用新网格 */
  onConfirm: () => void
  /** 用户取消：保持现有网格 */
  onCancel: () => void
}

// 缩小网格会让部分小组件放不下时的处理对话框；布局编辑器与 screens 页共用。
export function OutOfBoundsDialog({ open, grid, items, onConfirm, onCancel }: OutOfBoundsDialogProps) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      {open && (
        <DialogContent
          tag="!"
          tagTone="crit"
          title={t('outOfBounds.title', { cols: grid.cols, rows: grid.rows, n: items.length })}
          footer={
            <>
              <Button variant="outline" className="rounded-[2px]" onClick={onCancel}>
                {t('outOfBounds.cancel')}
              </Button>
              <Button variant="destructive" className="rounded-[2px]" onClick={onConfirm}>
                {t('outOfBounds.confirm', { n: items.length })}
              </Button>
            </>
          }
        >
          <p>{t('outOfBounds.body')}</p>
          <ul className="mt-2.5 max-h-[240px] overflow-y-auto border border-border" data-testid="oob-list">
            {items.map((it) => (
              <li key={`${it.screen}/${it.id}`} className="flex items-baseline gap-2 border-b border-border px-2.5 py-1.5 last:border-b-0">
                <span className="font-mono text-[11px] text-muted-foreground">{it.screen}</span>
                <span className="min-w-0 flex-1 truncate">{it.label}</span>
                <span className="font-mono text-[11.5px] text-muted-foreground">{it.place}</span>
              </li>
            ))}
          </ul>
        </DialogContent>
      )}
    </Dialog>
  )
}
