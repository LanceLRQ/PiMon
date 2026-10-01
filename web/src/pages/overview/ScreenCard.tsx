import { ArrowLeftRight, Power, RefreshCw, Sun } from 'lucide-react'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { DEFAULT_VIEWPORT } from '@/editor/geometry'
import { ScreenThumb, useThumbScreens } from '@/editor/ScreenThumb'
import { translateErrorValue } from '@/i18n/errors'
import { formatDateTime, parseTime } from '@/lib/time'
import { usePlugins } from '@/pages/instances/use-plugins'
import { effectiveTouch } from '@/pages/screens/touch'
import { useScreenStatus } from '@/pages/screens/use-screen-status'
import { selectInstances, selectLayout, selectScreenState, selectSettings, serverNow, useLiveStore } from '@/store/live-store'
import { DEFAULT_THEME_ID, isThemeId, type ThemeId } from '@/themes'
import type { ScreenControlRequest } from '@/types/generated'
import { NumberTag } from '@/ui/numbered-label'
import { StatusShape } from '@/ui/status-shape'
import { useToast } from '@/ui/toast'

const THUMB_WIDTH = 220
const WAKE_MINUTES = 30

/** 总览页的屏幕卡片：当前 screen 的实时缩略图、显示器参数与 k1–k4 远程按键 */
export function ScreenCard() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const { status, failed, reload } = useScreenStatus()
  const layout = useLiveStore(selectLayout)
  const liveState = useLiveStore(selectScreenState)
  const settings = useLiveStore(selectSettings)
  const instances = useLiveStore(selectInstances)
  const plugins = usePlugins()
  const [busy, setBusy] = useState(false)
  const lang = i18n.language === 'en' ? 'en' : 'zh'
  const now = useCallback(() => serverNow(), [])

  const screens = useMemo(() => layout?.layout.screens ?? [], [layout])
  // 离线后服务端不会清空 current_screen：只有在线时才认它，否则回到首页
  const online = status?.online === true
  const current = online ? screens.find((s) => s.id === status.current_screen) : undefined
  const shown = current ?? screens.find((s) => s.id === 'index') ?? screens[0]
  const shownList = useMemo(() => (shown ? [shown] : []), [shown])
  const { resolved, data } = useThumbScreens(shownList, plugins.list?.plugins ?? [], instances)

  const state = liveState ?? status?.state
  const off = state?.mode === 'off'
  const themeId: ThemeId = isThemeId(state?.theme_id) ? (state!.theme_id as ThemeId) : DEFAULT_THEME_ID
  const viewport = status?.viewport ? { w: status.viewport.w, h: status.viewport.h } : DEFAULT_VIEWPORT
  const nextScreen = (() => {
    if (screens.length < 2) return undefined
    const i = screens.findIndex((s) => s.id === shown?.id)
    return screens[(i + 1) % screens.length]
  })()

  const send = async (req: ScreenControlRequest) => {
    setBusy(true)
    try {
      await http.post('/api/screen/control', req)
      toast.show(t('overview.screen.sent', { action: t(`overview.screen.action.${req.action}`) }))
      void reload()
    } catch (e) {
      toast.show(translateErrorValue(i18n, e), 'warn')
    } finally {
      setBusy(false)
    }
  }

  const unknown = t('overview.screen.unknown')
  const touch = settings ? effectiveTouch(settings.screen.input_mode, status?.coarse_pointer) : null
  const nextMs = parseTime(state?.next_change)
  const rows: { k: string; v: ReactNode }[] = [
    { k: t('overview.screen.kvResolution'), v: status?.viewport ? `${status.viewport.w}×${status.viewport.h}` : unknown },
    { k: t('overview.screen.kvGrid'), v: layout ? `${layout.layout.grid.cols}×${layout.layout.grid.rows}` : unknown },
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
      k: 'k3',
      icon: <Power size={15} />,
      label: off ? t('overview.screen.keyOn') : t('overview.screen.keyOff'),
      disabled: !ready,
      onClick: () => send({ action: off ? 'on' : 'off' }),
    },
    {
      k: 'k4',
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
          {resolved[0] && layout ? (
            <div className="relative" style={{ width: THUMB_WIDTH }}>
              <ScreenThumb
                screen={resolved[0]}
                grid={layout.layout.grid}
                data={data}
                themeId={themeId}
                reduceEffects={settings?.reduce_effects ?? false}
                viewport={viewport}
                width={THUMB_WIDTH}
                lang={lang}
                timezone={settings?.timezone ?? 'UTC'}
                now={now}
                label={t('overview.screen.thumbLabel')}
              />
              {off && <div className="absolute inset-0 grid place-items-center bg-black/80 text-[12px] text-white">{t('overview.screen.thumbOff')}</div>}
            </div>
          ) : (
            <div className="grid h-[132px] place-items-center rounded-[2px] border border-dashed border-line-strong bg-panel-2/50 text-[13px] text-muted-foreground" style={{ width: THUMB_WIDTH }}>
              {t('overview.screen.thumbEmpty')}
            </div>
          )}
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
          <button
            key={k.k}
            type="button"
            disabled={busy || k.disabled}
            onClick={() => void k.onClick()}
            className="flex flex-col gap-1 rounded-[2px] border border-line-strong px-2.5 py-2 text-left text-[12px] outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 disabled:hover:bg-transparent"
          >
            <span className="flex items-center justify-between">
              {k.icon}
              <NumberTag no={k.k} />
            </span>
            <span>{k.label}</span>
            {k.sub && <span className="truncate font-mono text-[11px] text-muted-foreground">{k.sub}</span>}
          </button>
        ))}
      </div>
    </>
  )
}
