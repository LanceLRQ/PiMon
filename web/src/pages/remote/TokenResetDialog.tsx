import { KeyRound, TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'
import { useToast } from '@/ui/toast'

interface Props {
  open: boolean
  onClose: () => void
  /** 令牌重置成功后回调（刷新操作记录与状态） */
  onReset: () => void
  /** 对外访问地址（设置里的 access_url）；为空时用当前页面的来源 */
  baseUrl: string
}

/**
 * 重置屏幕令牌的二次确认：先说明后果，确认后才调用接口；
 * 成功后说明新令牌的获取方式（接口不回传令牌，令牌写在中枢数据目录的 screen.token 文件里）。
 */
export function TokenResetDialog({ open, onClose, onReset, baseUrl }: Props) {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  const close = () => {
    setDone(false)
    onClose()
  }
  const confirm = async () => {
    setBusy(true)
    try {
      await http.post('/api/screen/token/reset')
      setDone(true)
      onReset()
    } catch (e) {
      toast.show(translateErrorValue(i18n, e), 'warn')
    } finally {
      setBusy(false)
    }
  }
  const origin = (baseUrl.trim() || window.location.origin).replace(/\/+$/, '')

  return (
    <Dialog open={open} onOpenChange={(v) => !v && !busy && close()}>
      <DialogContent
        title={done ? t('remote.token.doneTitle') : t('remote.token.title')}
        tag={done ? 'k6' : '!'}
        tagTone={done ? 'default' : 'crit'}
        role="alertdialog"
        footer={
          done ? (
            <Button size="sm" className="rounded-[2px]" onClick={close}>
              {t('remote.token.close')}
            </Button>
          ) : (
            <>
              <Button variant="outline" size="sm" className="rounded-[2px]" disabled={busy} onClick={close}>
                {t('remote.token.cancel')}
              </Button>
              <Button variant="destructive" size="sm" className="rounded-[2px]" disabled={busy} onClick={() => void confirm()}>
                <KeyRound size={14} />
                {t('remote.token.confirm')}
              </Button>
            </>
          )
        }
      >
        {done ? (
          <div data-testid="token-done" className="flex flex-col gap-2.5">
            <p>{t('remote.token.doneBody')}</p>
            <code className="block overflow-x-auto rounded-[2px] border border-border bg-panel-2 px-2.5 py-1.5 font-mono text-[12px] break-all whitespace-pre-wrap">{`${origin}/screen/auth?token=<token>`}</code>
            <p className="text-[12px] text-muted-foreground">{t('remote.token.doneHint')}</p>
          </div>
        ) : (
          <Note tone="crit" icon={<TriangleAlert size={16} />}>
            <b className="font-medium">{t('remote.token.warn')}</b>
            <br />
            {t('remote.token.consequence')}
          </Note>
        )}
      </DialogContent>
    </Dialog>
  )
}
