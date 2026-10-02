import { ArrowLeftRight, Power, RefreshCw, Sun } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { formatDateTime, parseTime } from '@/lib/time'
import { RemoteKey, useScreenControl, WAKE_MINUTES } from '@/pages/remote/remote-keys'
import { effectiveTouch } from '@/pages/screens/touch'
import { useScreenStatus } from '@/pages/screens/use-screen-status'
import { LiveThumb, useScreenView } from '@/pages/screens/use-screen-view'
import { StatusShape } from '@/ui/status-shape'

const THUMB_WIDTH = 220

/** 总览页的屏幕卡片：当前 screen 的实时缩略图、显示器参数与 k1–k4 远程按键 */
export function ScreenCard() {
  const { t, i18n } = useTranslation()
  const { status, failed, reload } = useScreenStatus()
  const view = useScreenView(status)
  const { busy, send } = useScreenControl(reload)
  const { screens, online, current, shown, state, off, settings } = view
  const layoutGrid = view.grid
  const nextScreen = (() => {
    if (screens.length < 2) return undefined
    const i = screens.findIndex((s) => s.id === shown?.id)
    return screens[(i + 1) % screens.length]
  })()

  const unknown = t('overview.screen.unknown')
  const touch = settings ? effectiveTouch(settings.screen.input_mode, status?.coarse_pointer) : null
  const nextMs = parseTime(state?.next_change)
  const rows: { k: string; v: ReactNode }[] = [
    { k: t('overview.screen.kvResolution'), v: status?.viewport ? `${status.viewport.w}×${status.viewport.h}` : unknown },
    { k: t('overview.screen.kvGrid'), v: layoutGrid ? `${layoutGrid.cols}×${layoutGrid.rows}` : unknown },
    { k: t('overview.screen.kvInput'), v: touch === null ? unknown : t(touch ? 'overview.screen.touchYes' : 'overview.screen.touchNo') },
    { k: t('overview.screen.kvCurrent'), v: current ? current.id : unknown },
    { k: t('overview.screen.kvTheme'), v: state?.theme_id ?? unknown },
    { k: t('overview.screen.kvNext'), v: nextMs === null ? unknown : formatDateTime(nextMs, i18n.language) },
  ]

  const ready = status !== null
  const keys = [
    {
      k: 'k1',
      icon: <RefreshCw size={15} />,
      label: t('overview.screen.keyRefresh'),
      disabled: !ready || !online,
      onClick: () => send({ action: 'refresh' }),
    },
    {
      k: 'k2',
      icon: <ArrowLeftRight size={15} />,
      label: t('overview.screen.keySwitch'),
      sub: nextScreen ? t('overview.screen.keySwitchTo', { name: nextScreen.name }) : undefined,
      disabled: !ready || !online || !nextScreen,
      onClick: () => nextScreen && send({ action: 'switch', screen_id: nextScreen.id }),
    },
    {
      // 编号与远程操作页一致：开屏是 k3，关屏是 k4，当前适用哪个就显示哪个
      k: off ? 'k3' : 'k4',
      icon: <Power size={15} />,
      label: off ? t('overview.screen.keyOn') : t('overview.screen.keyOff'),
      disabled: !ready,
      onClick: () => send({ action: off ? 'on' : 'off' }),
    },
    {
      k: 'k5',
      icon: <Sun size={15} />,
      label: t('overview.screen.keyWake'),
      sub: t('overview.screen.wakeMinutes', { n: WAKE_MINUTES }),
      disabled: !ready,
      onClick: () => send({ action: 'wake', minutes: WAKE_MINUTES }),
    },
  ]

  return (
    <>
      <div className="flex items-center gap-1.5 px-4 pt-3 text-[12px] text-muted-foreground">
        {status ? (
          <>
            <StatusShape state={online ? 'ok' : 'offline'} size={12} />
            {online ? t('overview.screen.online') : status.last_seen ? t('overview.screen.offline') : t('overview.screen.never')}
          </>
        ) : failed ? (
          t('overview.screen.unavailable')
        ) : null}
      </div>
      <div className="flex gap-4 px-4 pt-2 mobile:flex-col">
        <div className="shrink-0">
          <LiveThumb view={view} width={THUMB_WIDTH} label={t('overview.screen.thumbLabel')} offLabel={t('overview.screen.thumbOff')} emptyLabel={t('overview.screen.thumbEmpty')} />
        </div>
        <dl className="min-w-0 flex-1 text-[12.5px]">
          {rows.map((r) => (
            <div key={r.k} className="flex items-baseline justify-between gap-3 border-b border-border py-1 last:border-b-0">
              <dt className="shrink-0 text-muted-foreground">{r.k}</dt>
              <dd className="min-w-0 truncate text-right font-mono text-[12px]">{r.v}</dd>
            </div>
          ))}
        </dl>
      </div>
      <div className="grid grid-cols-4 gap-2 px-4 py-3 mobile:grid-cols-2">
        {keys.map((k) => (
          <RemoteKey key={k.k} k={k.k} icon={k.icon} label={k.label} sub={k.sub} disabled={busy || k.disabled} onClick={() => void k.onClick()} />
        ))}
      </div>
    </>
  )
}
