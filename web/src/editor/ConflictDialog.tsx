import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'
import { ChangeList } from './ChangeList'
import type { Change } from './changes'

interface ConflictDialogProps {
  open: boolean
  baseVersion: number
  latestVersion: number
  /** 对方相对我的基线改了什么；null 表示还没去看 */
  theirs: Change[] | null
  busy: 'view' | 'overwrite' | null
  error: string | null
  onView: () => void
  onOverwrite: () => void
  onClose: () => void
}

// 保存时服务端版本已经变了：先看对方改了什么，再决定是否以我的为准重新提交
export function ConflictDialog({ open, baseVersion, latestVersion, theirs, busy, error, onView, onOverwrite, onClose }: ConflictDialogProps) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={(o) => !o && !busy && onClose()}>
      {open && (
        <DialogContent
          tag="!"
          tagTone="crit"
          title={t('layoutEd.conflict.title')}
          footer={
            <>
              <Button variant="outline" className="rounded-[2px]" disabled={busy !== null} onClick={onClose}>
                {t('layoutEd.conflict.close')}
              </Button>
              <Button variant="outline" className="rounded-[2px]" disabled={busy !== null} onClick={onView}>
                {t('layoutEd.conflict.view')}
              </Button>
              <Button variant="destructive" className="rounded-[2px]" disabled={busy !== null} onClick={onOverwrite}>
                {t('layoutEd.conflict.overwrite')}
              </Button>
            </>
          }
        >
          <p>{t('layoutEd.conflict.body', { base: baseVersion, latest: latestVersion })}</p>
          <p className="mt-1.5 text-[12.5px] text-muted-foreground">{t('layoutEd.conflict.overwriteHint')}</p>
          {error && (
            <Note tone="crit" role="alert" className="mt-2.5">
              {error}
            </Note>
          )}
          {theirs && (
            <div className="mt-3 border border-border" data-testid="conflict-theirs">
              <div className="border-b border-border bg-panel-2 px-3.5 py-1.5 text-[12px] font-medium">
                {t('layoutEd.conflict.theirsTitle', { base: baseVersion, latest: latestVersion })}
              </div>
              <ChangeList changes={theirs} empty={t('layoutEd.conflict.theirsNone')} />
            </div>
          )}
        </DialogContent>
      )}
    </Dialog>
  )
}
