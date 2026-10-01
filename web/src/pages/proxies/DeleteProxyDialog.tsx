import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import type { Proxy, ProxyReferrer } from '@/types/generated'
import { Button } from '@/ui/button'
import { Checkbox } from '@/ui/checkbox'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'
import { StatusShape } from '@/ui/status-shape'

interface Props {
  proxy: Proxy | null
  onClose(): void
  onDeleted(proxy: Proxy): void
}

function referrersOf(err: unknown): ProxyReferrer[] | null {
  if (!isApiError(err) || err.code !== 'proxy.in_use') return null
  const raw = err.details.instances
  return Array.isArray(raw) ? (raw as ProxyReferrer[]) : []
}

// 删除代理：被引用时先列出受影响的实例，勾选「我已了解」后才带 force 删除（引用它的实例改为直连）。
// 列表里没有引用但服务端回 409（刚被引用）时，转入同样的二次确认。
export function DeleteProxyDialog({ proxy, onClose, onDeleted }: Props) {
  return (
    <Dialog open={proxy !== null} onOpenChange={(o) => !o && onClose()}>
      {proxy && <DeleteBody key={proxy.id} proxy={proxy} onClose={onClose} onDeleted={onDeleted} />}
    </Dialog>
  )
}

function DeleteBody({ proxy, onClose, onDeleted }: { proxy: Proxy; onClose(): void; onDeleted(p: Proxy): void }) {
  const { t, i18n } = useTranslation()
  const [refs, setRefs] = useState<ProxyReferrer[]>(proxy.referrers)
  const [raced, setRaced] = useState(false)
  const [ack, setAck] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const referenced = refs.length > 0

  async function submit() {
    setBusy(true)
    setError(null)
    try {
      await http.delete(`/api/proxies/${proxy.id}`, referenced ? { query: { force: 1 } } : undefined)
      onDeleted(proxy)
    } catch (e) {
      const now = referrersOf(e)
      if (now) {
        // 删除前刚被引用：列出受影响的实例，要求确认后再强制删除
        setRefs(now)
        setRaced(true)
        setAck(false)
      } else {
        setError(translateErrorValue(i18n, e))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <DialogContent
      tag="!"
      tagTone="crit"
      title={t('proxies.deleteDialog.title', { name: proxy.name })}
      footer={
        <>
          <Button variant="outline" className="rounded-[2px]" disabled={busy} onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="destructive" className="rounded-[2px]" disabled={busy || (referenced && !ack)} onClick={() => void submit()}>
            {t('proxies.deleteDialog.confirm')}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {raced && <Note tone="warn">{t('proxies.deleteDialog.inUse')}</Note>}
        <p>{referenced ? t('proxies.deleteDialog.bodyRefs', { count: refs.length }) : t('proxies.deleteDialog.bodyNone')}</p>
        {referenced && (
          <>
            <ul className="max-h-48 divide-y divide-border overflow-y-auto rounded-[2px] border border-border">
              {refs.map((r) => (
                <li key={r.id} className="flex items-center gap-2 px-2.5 py-1.5 text-[13px]">
                  <StatusShape state="broken" size={14} />
                  <span className="min-w-0 truncate">{r.name}</span>
                  <span className="ml-auto font-mono text-[11px] text-muted-foreground">{r.id}</span>
                </li>
              ))}
            </ul>
            <label className="flex cursor-pointer items-start gap-2.5">
              <Checkbox checked={ack} onChange={setAck} ariaLabel={t('proxies.deleteDialog.ack')} className="mt-0.5" />
              <span onClick={() => setAck(!ack)}>{t('proxies.deleteDialog.ack')}</span>
            </label>
          </>
        )}
        {error && <Note tone="crit" role="alert">{error}</Note>}
      </div>
    </DialogContent>
  )
}
