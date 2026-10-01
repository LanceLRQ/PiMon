import { ArrowLeftRight, Clock, KeyRound, Power, RefreshCw, Sun } from 'lucide-react'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { formatClockInZone, formatIn, parseTime } from '@/lib/time'
import { effectiveTouch } from '@/pages/screens/touch'
import { useScreenStatus } from '@/pages/screens/use-screen-status'
import { LiveThumb, useScreenView } from '@/pages/screens/use-screen-view'
import { useIsMobile } from '@/lib/use-mobile'
import { cn } from '@/lib/utils'
import { serverNow } from '@/store/live-store'
import type { ScreenOp } from '@/types/generated'
import { PageHeader } from '@/ui/page-header'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { Section } from '@/ui/section'
import { Segmented } from '@/ui/segmented'
import { Select } from '@/ui/select'
import { StatusShape } from '@/ui/status-shape'
import { RemoteKey, RemoteKeyFrame, useScreenControl } from './remote-keys'
import { TokenResetDialog } from './TokenResetDialog'
import { useScreenOps } from './use-ops'

const WAKE_CHOICES = ['15', '30', '60'] as const

export function RemotePage() {
  const { t, i18n } = useTranslation()
  const { status, failed, reload: reloadStatus } = useScreenStatus()
  const { ops, reload: reloadOps } = useScreenOps()
  const view = useScreenView(status)
  const mobile = useIsMobile()
  const refreshAll = useCallback(() => Promise.all([reloadStatus(), reloadOps()]).then(() => undefined), [reloadStatus, reloadOps])
  const { busy, send } = useScreenControl(refreshAll)
  const [target, setTarget] = useState<string>('')
  const [wake, setWake] = useState<(typeof WAKE_CHOICES)[number]>('30')
  const [tokenOpen, setTokenOpen] = useState(false)

  const { screens, online, state, off, settings } = view
  const ready = status !== null
  const tz = settings?.timezone ?? 'UTC'
  const nowMs = serverNow()
  const clock = (ms: number) => formatClockInZone(ms, nowMs, tz, i18n.language)
  const targetId = screens.some((s) => s.id === target) ? target : (screens.find((s) => s.id !== view.current?.id) ?? screens[0])?.id ?? ''
  const nextMs = parseTime(state?.next_change)
  const untilMs = parseTime(state?.until)
  const unknown = t('overview.screen.unknown')
  const touch = settings ? effectiveTouch(settings.screen.input_mode, status?.coarse_pointer) : null

  const reason = (() => {
    if (!state) return null
    switch (state.reason) {
      case 'remote_on':
      case 'remote_off':
        return t(`remote.reason.${state.reason}`, { until: clock(untilMs ?? nextMs ?? nowMs) })
      case 'wake':
        return t('remote.reason.wake', { until: clock(untilMs ?? nowMs) })
      default:
        return t('remote.reason.schedule')
    }
  })()

  const stateRow = (
    <div data-testid="remote-state" className="flex flex-col gap-1.5 px-4 pt-3.5 pb-1">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {status ? <StatusShape state={online ? (off ? 'unknown' : 'ok') : 'offline'} size={18} /> : null}
        <span className="text-[19px] font-medium mobile:text-[18px]">{state ? t(off ? 'remote.state.off' : 'remote.state.on') : unknown}</span>
        {!online && status && <span className="rounded-[2px] border border-status-warn px-1.5 text-[11.5px] leading-[18px]">{t('remote.state.offline')}</span>}
        <span className="font-mono text-[12.5px] text-muted-foreground">
          {online && view.current ? `${view.current.id} · ` : ''}
          {state?.theme_id ?? ''}
        </span>
      </div>
      {reason && <div className="text-[12.5px] leading-[1.55] text-ink-2">{reason}</div>}
      <div className="text-[12.5px] text-ink-2">
        {nextMs === null ? (state ? t('remote.nextNone') : '') : t('remote.next', { at: clock(nextMs), in: formatIn(t, nowMs, nextMs) })}
      </div>
      {failed && !status && <div className="text-[12px] text-muted-foreground">{t('overview.screen.unavailable')}</div>}
    </div>
  )

  const kvRow = (k: string, v: ReactNode, hl = false) => (
    <div className="flex items-baseline justify-between gap-3 border-b border-border px-4 py-1.5 text-[12.5px] last:border-b-0">
      <dt className="shrink-0 text-muted-foreground">{k}</dt>
      <dd className={cn('min-w-0 truncate text-right font-mono text-[12px]', hl && 'text-signal-text')}>{v}</dd>
    </div>
  )
  const body = (
    <>
      <div className="px-4 py-3">
        <LiveThumb view={view} width={mobile ? 200 : 260} label={t('overview.screen.thumbLabel')} offLabel={t('overview.screen.thumbOff')} emptyLabel={t('overview.screen.thumbEmpty')} />
      </div>
      <dl data-testid="remote-kv" className="border-t border-border">
        {kvRow(
          t('remote.kv.display'),
          !status ? unknown : online ? (status.viewport ? t('remote.kv.onlineVp', { w: status.viewport.w, h: status.viewport.h }) : t('remote.kv.online')) : status.last_seen ? t('remote.kv.offline') : t('remote.kv.never'),
        )}
        {kvRow(t('remote.kv.screen'), online && view.current ? view.current.id : '—')}
        {kvRow(t('remote.kv.theme'), state?.theme_id ?? unknown)}
        {kvRow(t('remote.kv.next'), nextMs === null ? '—' : clock(nextMs), nextMs !== null)}
        {kvRow(t('remote.kv.wake'), state?.reason === 'wake' && untilMs !== null ? t('remote.kv.wakeUntil', { time: clock(untilMs) }) : '—')}
        {kvRow(t('remote.kv.input'), touch === null ? unknown : t(touch ? 'overview.screen.touchYes' : 'overview.screen.touchNo'))}
      </dl>
    </>
  )

  const idleSeconds = settings?.screen.idle_home_seconds ?? 60
  // 手机上按「开屏 关屏 / 切换 / 临时亮屏 / 刷新 / 重置令牌」排布，桌面按 k1–k6 顺序
  const keypad = (
    <div data-testid="remote-keys" className="grid grid-cols-2 gap-2 p-3.5 mobile:grid-cols-2 mobile:p-3">
      <RemoteKey k="k1" icon={<RefreshCw size={15} />} label={t('remote.keys.refresh.label')} desc={t('remote.keys.refresh.desc')} disabled={busy || !ready || !online} onClick={() => void send({ action: 'refresh' })} className="mobile:order-5 mobile:col-span-2" />
      <RemoteKeyFrame k="k2" icon={<ArrowLeftRight size={15} />} label={t('remote.keys.switch.label')} desc={t('remote.keys.switch.desc', { n: idleSeconds })} className="col-span-2 mobile:order-3">
        <Select className="flex-1" aria-label={t('remote.keys.switch.target')} value={targetId} disabled={!ready || !online || screens.length === 0} onChange={(e) => setTarget(e.target.value)}>
          {screens.map((s) => (
            <option key={s.id} value={s.id}>
              {s.id} {s.name}
            </option>
          ))}
        </Select>
        <Button variant="outline" size="sm" className="rounded-[2px]" disabled={busy || !ready || !online || !targetId} onClick={() => void send({ action: 'switch', screen_id: targetId })}>
          {t('remote.keys.switch.go')}
        </Button>
      </RemoteKeyFrame>
      <RemoteKey k="k3" icon={<Sun size={15} />} label={t('remote.keys.on.label')} desc={t('remote.keys.on.desc')} disabled={busy || !ready} onClick={() => void send({ action: 'on' })} className="mobile:order-1" />
      <RemoteKey k="k4" icon={<Power size={15} />} label={t('remote.keys.off.label')} desc={t('remote.keys.off.desc')} disabled={busy || !ready} onClick={() => void send({ action: 'off' })} className="mobile:order-2" />
      <RemoteKeyFrame k="k5" icon={<Clock size={15} />} label={t('remote.keys.wake.label')} desc={t('remote.keys.wake.desc')} className="col-span-2 mobile:order-4">
        <Segmented
          ariaLabel={t('remote.keys.wake.minutes')}
          value={wake}
          onChange={setWake}
          options={WAKE_CHOICES.map((m) => ({ value: m, label: m, title: m }))}
        />
        <span className="text-[12px] text-muted-foreground">{t('remote.keys.wake.unit')}</span>
        <Button variant="outline" size="sm" className="ml-auto rounded-[2px]" disabled={busy || !ready} onClick={() => void send({ action: 'wake', minutes: Number(wake) })}>
          {t('remote.keys.wake.go')}
        </Button>
      </RemoteKeyFrame>
      <RemoteKey k="k6" icon={<KeyRound size={15} />} label={t('remote.keys.token.label')} desc={t('remote.keys.token.desc')} danger disabled={busy} onClick={() => setTokenOpen(true)} className="col-span-2 mobile:order-6" />
    </div>
  )

  const opDetail = (o: ScreenOp): string => {
    if (o.action === 'switch') return t('remote.ops.toScreen', { id: String(o.params?.screen_id ?? '') })
    if (o.action === 'wake') return t('remote.ops.minutes', { n: Number(o.params?.minutes ?? 0) })
    return ''
  }
  const opRows = useMemo(() => ops ?? [], [ops])

  const statusHead = <Section no="02.1" title={t('remote.status.title')} meta={t('remote.status.meta')}>{stateRow}{mobile ? null : body}</Section>
  const keysHead = (
    <Section no="02.2" title={t('remote.keys.title')} meta={t('remote.keys.meta')}>
      {!online && status && (
        <div className="px-3.5 pt-3.5">
          <Note tone="warn" role="status">
            {t('remote.offlineHint')}
          </Note>
        </div>
      )}
      {keypad}
    </Section>
  )

  return (
    <>
      <PageHeader no="02d" title={t('pages.remote')} sub={t('remote.sub')} />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        {mobile ? (
          <>
            {statusHead}
            {keysHead}
            <Section no="02.1" title={t('remote.status.detail')}>{body}</Section>
          </>
        ) : (
          <div className="grid grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)] items-start gap-4">
            {statusHead}
            {keysHead}
          </div>
        )}
        <Section no="02.3" title={t('remote.ops.title')} meta={t('remote.ops.meta', { n: 5 })}>
          {opRows.length === 0 ? (
            <div className="px-4 py-3 text-[13px] text-muted-foreground">{t('remote.ops.empty')}</div>
          ) : (
            <table className="w-full text-[13px]">
              <tbody data-testid="remote-log">
                {opRows.map((o) => {
                  const at = parseTime(o.at)
                  const oneShot = o.action === 'refresh' || o.action === 'switch'
                  return (
                    <tr key={o.id} className="border-b border-border last:border-b-0">
                      <td className="w-[100px] px-4 py-2 font-mono text-[12px] whitespace-nowrap">{at === null ? unknown : clock(at)}</td>
                      <td className="px-2 py-2">
                        {t(`remote.ops.action.${o.action}`, { defaultValue: o.action })}
                        {opDetail(o) && <span className="ml-2 font-mono text-[12px] text-muted-foreground">{opDetail(o)}</span>}
                        {oneShot && (
                          <span className={cn('ml-2 rounded-[2px] border px-1.5 text-[11px] leading-[16px]', o.delivered ? 'border-border text-muted-foreground' : 'border-status-warn')}>
                            {t(o.delivered ? 'remote.ops.delivered' : 'remote.ops.undelivered')}
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-2 text-right font-mono text-[12px] text-muted-foreground mobile:hidden">{t('remote.ops.from', { ip: o.client_ip })}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </Section>
      </div>
      <TokenResetDialog open={tokenOpen} onClose={() => setTokenOpen(false)} onReset={() => void refreshAll()} baseUrl={settings?.access_url ?? ''} />
    </>
  )
}
