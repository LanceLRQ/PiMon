import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'

interface Props {
  open: boolean
  busy: boolean
  // 上一次重置失败的原因（已翻译）；对话框保持打开以便重试
  error: string | null
  onCancel(): void
  onConfirm(): void
}

// 重置 NAS 配置前的二次确认：清空密文是不可逆的
export function ResetNasDialog({ open, busy, error, onCancel, onConfirm }: Props) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={(o) => !o && !busy && onCancel()}>
      <DialogContent
        tag="!"
        tagTone="crit"
        title={t('settings.backup.remote.resetTitle')}
        footer={
          <>
            <Button variant="outline" className="rounded-[2px]" disabled={busy} onClick={onCancel}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" className="rounded-[2px]" disabled={busy} onClick={onConfirm}>
              {busy ? t('settings.backup.remote.resetting') : t('settings.backup.remote.resetConfirm')}
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <Note tone="warn" icon={<AlertTriangle size={15} />}>
            {t('settings.backup.remote.resetBody')}
          </Note>
          {error && (
            <Note tone="crit" role="alert">
              {t('settings.backup.remote.resetFailed', { reason: error })}
            </Note>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
