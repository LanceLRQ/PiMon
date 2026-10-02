import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { DEFAULT_VIEWPORT } from '@/editor/geometry'
import { ScreenThumb, useThumbScreens } from '@/editor/ScreenThumb'
import { usePlugins } from '@/pages/instances/use-plugins'
import { selectInstances, selectLayout, selectScreenState, selectSettings, serverNow, useLiveStore } from '@/store/live-store'
import { DEFAULT_THEME_ID, isThemeId, type ThemeId } from '@/themes'
import type { LayoutScreen, ResolvedScreen, ScreenState, ScreenStatus, Settings } from '@/types/generated'
import type { InstanceDataMap } from '@/templates'

export interface ScreenView {
  screens: LayoutScreen[]
  online: boolean
  /** 屏幕上报的当前 screen；离线后服务端不会清空 current_screen，所以只在线时才认 */
  current: LayoutScreen | undefined
  /** 缩略图展示的 screen：当前的，否则首页 */
  shown: LayoutScreen | undefined
  resolved: ResolvedScreen[]
  data: InstanceDataMap
  state: ScreenState | undefined
  off: boolean
  themeId: ThemeId
  viewport: { w: number; h: number }
  settings: Settings | null
  grid: { cols: number; rows: number } | undefined
}

/** 显示器的当前视图：总览屏幕卡与远程操作页共用，布局与屏幕状态取自实时存储，在线与 viewport 取自 status */
export function useScreenView(status: ScreenStatus | null): ScreenView {
  const layout = useLiveStore(selectLayout)
  const liveState = useLiveStore(selectScreenState)
  const settings = useLiveStore(selectSettings)
  const instances = useLiveStore(selectInstances)
  const plugins = usePlugins()
  const screens = useMemo(() => layout?.layout.screens ?? [], [layout])
  const online = status?.online === true
  const current = online ? screens.find((s) => s.id === status.current_screen) : undefined
  const shown = current ?? screens.find((s) => s.id === 'index') ?? screens[0]
  const shownList = useMemo(() => (shown ? [shown] : []), [shown])
  const { resolved, data } = useThumbScreens(shownList, plugins.list?.plugins ?? [], instances)
  const state = liveState ?? status?.state
  const themeId: ThemeId = isThemeId(state?.theme_id) ? (state!.theme_id as ThemeId) : DEFAULT_THEME_ID
  const viewport = status?.viewport ? { w: status.viewport.w, h: status.viewport.h } : DEFAULT_VIEWPORT
  return { screens, online, current, shown, resolved, data, state, off: state?.mode === 'off', themeId, viewport, settings, grid: layout?.layout.grid }
}

/** 当前 screen 的实时缩略图；关屏时盖遮罩，没有布局时显示占位 */
export function LiveThumb({ view, width, offLabel, emptyLabel, label }: { view: ScreenView; width: number; offLabel: string; emptyLabel: string; label: string }) {
  const { i18n } = useTranslation()
  const now = useCallback(() => serverNow(), [])
  const lang = i18n.language === 'en' ? 'en' : 'zh'
  if (!view.resolved[0] || !view.grid) {
    return (
      <div className="grid h-[132px] place-items-center rounded-[2px] border border-dashed border-line-strong bg-panel-2/50 text-[13px] text-muted-foreground" style={{ width }}>
        {emptyLabel}
      </div>
    )
  }
  return (
    <div className="relative" style={{ width }}>
      <ScreenThumb
        screen={view.resolved[0]}
        grid={view.grid}
        data={view.data}
        themeId={view.themeId}
        reduceEffects={view.settings?.reduce_effects ?? false}
        viewport={view.viewport}
        width={width}
        lang={lang}
        timezone={view.settings?.timezone ?? 'UTC'}
        now={now}
        label={label}
      />
      {view.off && <div className="absolute inset-0 grid place-items-center bg-black/80 text-[12px] text-white">{offLabel}</div>}
    </div>
  )
}
