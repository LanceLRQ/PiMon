import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { BackupInfo } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'

interface Props {
  backup: BackupInfo | null
  onClose(): void
}

// 下载备份前的提示：备份包内含密钥文件，需要用户确认后才发起下载
export function BackupDownloadDialog({ backup, onClose }: Props) {
  const { t } = useTranslation()
  return (
    <Dialog open={backup !== null} onOpenChange={(o) => !o && onClose()}>
      {backup && (
        <DialogContent
          tag="!"
          tagTone="crit"
          title={t('settings.downloadWarn.title')}
          footer={
            <>
              <Button variant="outline" className="rounded-[2px]" onClick={onClose}>
                {t('common.cancel')}
              </Button>
              <Button asChild className="rounded-[2px]">
                <a href={`/api/backups/${encodeURIComponent(backup.name)}`} download={backup.name} onClick={onClose}>
                  {t('settings.downloadWarn.confirm')}
                </a>
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <Note tone="warn" icon={<AlertTriangle size={15} />}>
              {t('settings.downloadWarn.body')}
            </Note>
            <div className="font-mono text-[12px] text-muted-foreground">{t('settings.downloadWarn.file', { name: backup.name })}</div>
          </div>
        </DialogContent>
      )}
    </Dialog>
  )
}
