import { Monitor, Trash2, TriangleAlert } from 'lucide-react'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import type { DeviceImpact } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogClose, DialogContent } from '@/ui/dialog'
import { Input } from '@/ui/input'
import { Note } from '@/ui/note'
import { StatusShape } from '@/ui/status-shape'

export interface RevokeTarget {
  id: string
  name: string
}

interface RevokeDialogProps {
  // 要吊销的设备；null 表示关闭。内容在打开时才挂载，输入与影响列表随之重置。
  target: RevokeTarget | null
  onCancel(): void
  // 吊销请求成功（204）之后调用
  onRevoked(target: RevokeTarget): void
}

export function RevokeDialog({ target, onCancel, onRevoked }: RevokeDialogProps) {
  // 吊销请求进行中不允许关闭（Esc、遮罩、关闭按钮都不行）
  const [busy, setBusy] = useState(false)
  return (
    <Dialog open={target !== null} onOpenChange={(o) => !o && !busy && onCancel()}>
      {target && <RevokeBody key={target.id} target={target} onCancel={onCancel} onRevoked={onRevoked} onBusy={setBusy} />}
    </Dialog>
  )
}

type Impact = { state: 'loading' } | { state: 'error'; detail: string } | { state: 'ok'; impact: DeviceImpact }

function RevokeBody({ target, onCancel, onRevoked, onBusy }: { target: RevokeTarget; onCancel(): void; onRevoked(t: RevokeTarget): void; onBusy(b: boolean): void }) {
  const { t, i18n } = useTranslation()
  const [impact, setImpact] = useState<Impact>({ state: 'loading' })
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [reload, setReload] = useState(0)

  useEffect(() => {
    let live = true
    http.get<DeviceImpact>(`/api/devices/${target.id}/impact`).then(
      (impact) => live && setImpact({ state: 'ok', impact }),
      (e) => live && setImpact({ state: 'error', detail: translateErrorValue(i18n, e) }),
    )
    return () => {
      live = false
    }
  }, [target.id, i18n, reload])

  const retry = useCallback(() => {
    setImpact({ state: 'loading' })
    setReload((n) => n + 1)
  }, [])

  const matched = typed.trim() === target.name
  const canRevoke = impact.state === 'ok' && matched && !busy

  async function revoke() {
    if (!canRevoke) return
    setBusy(true)
    onBusy(true)
    setError(null)
    try {
      await http.delete(`/api/devices/${target.id}`)
      onBusy(false)
      onRevoked(target)
    } catch (e) {
      setError(translateErrorValue(i18n, e))
      setBusy(false)
      onBusy(false)
    }
  }

  return (
    <DialogContent
      tag="!"
      tagTone="crit"
      role="alertdialog"
      title={t('devices.revoke.title', { name: target.name })}
      className="w-[min(560px,calc(100vw-32px))]"
      footer={
        <>
          <DialogClose asChild>
            <Button variant="outline" size="sm" className="rounded-[2px]" disabled={busy} onClick={onCancel}>{t('common.cancel')}</Button>
          </DialogClose>
          <Button variant="destructive" size="sm" className="rounded-[2px]" disabled={!canRevoke} onClick={() => void revoke()}>
            <Trash2 /> {busy ? t('devices.revoke.submitting') : t('devices.revoke.submit')}
          </Button>
        </>
      }
    >
      <p className="m-0">{t('devices.revoke.body')}</p>
      <div className="mt-3 rounded-[2px] border border-border">
        {impact.state === 'loading' && <div role="status" className="px-3 py-3 text-[12.5px] text-muted-foreground">{t('devices.revoke.loading')}</div>}
        {impact.state === 'error' && (
          <div className="flex flex-wrap items-center gap-2 px-3 py-3">
            <Note tone="crit" role="alert" icon={<TriangleAlert size={15} />} className="flex-1">
              {t('devices.revoke.failed', { detail: impact.detail })}
            </Note>
            <Button variant="outline" size="sm" className="rounded-[2px]" onClick={retry}>{t('devices.revoke.retry')}</Button>
          </div>
        )}
        {impact.state === 'ok' && (
          <>
            <Group title={t('devices.revoke.instances')} count={t('devices.revoke.count', { count: impact.impact.instances.length })} empty={t('devices.revoke.noInstances')} first>
              {impact.impact.instances.map((i) => (
                <li key={i.id}><StatusShape state="broken" size={14} />{i.name}</li>
              ))}
            </Group>
            <Group title={t('devices.revoke.screens')} count={t('devices.revoke.count', { count: impact.impact.screens.length })} empty={t('devices.revoke.noScreens')}>
              {impact.impact.screens.map((s) => (
                <li key={s.id}>
                  <Monitor size={14} aria-hidden="true" />
                  {s.name}
                  <span className="ml-auto text-[11.5px] text-muted-foreground">{t('devices.revoke.screenNote')}</span>
                </li>
              ))}
            </Group>
            <Group title={t('devices.revoke.rules')} count={t('devices.revoke.countRules', { count: impact.impact.rules.length })} empty={t('devices.revoke.noRules')}>
              {impact.impact.rules.map((r) => (
                <li key={r.id}>{r.name}</li>
              ))}
            </Group>
          </>
        )}
      </div>
      <div className="pt-3.5">
        <label htmlFor="revoke-name" className="block text-[12.5px]">
          {t('devices.revoke.inputPrompt', { name: target.name })}
        </label>
        <Input
          id="revoke-name"
          className="mt-1.5 font-mono"
          autoComplete="off"
          spellCheck={false}
          aria-label={t('devices.revoke.inputLabel')}
          value={typed}
          disabled={busy}
          onChange={(e) => setTyped(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void revoke()
            }
          }}
        />
      </div>
      {error && <Note tone="crit" role="alert" icon={<TriangleAlert size={15} />} className="mt-3">{error}</Note>}
    </DialogContent>
  )
}

function Group({ title, count, empty, first, children }: { title: string; count: string; empty: string; first?: boolean; children: ReactNode }) {
  const items = Array.isArray(children) ? children : [children]
  const has = items.length > 0
  return (
    <div className={first ? '' : 'border-t border-border'}>
      <h4 className="m-0 flex items-center justify-between px-3 pt-2 text-[12px] font-medium text-muted-foreground">
        {title}
        <span className="font-mono">{count}</span>
      </h4>
      {has ? (
        <ul className="m-0 list-none px-3 pb-2 text-[12.5px] [&>li]:flex [&>li]:items-center [&>li]:gap-2 [&>li]:py-0.5">{children}</ul>
      ) : (
        <div className="px-3 pb-2 text-[12.5px] text-muted-foreground">{empty}</div>
      )}
    </div>
  )
}
