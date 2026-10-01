import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react'
import { useTranslation } from 'react-i18next'
import { HistoryContext, ScreenEnvProvider, ThemeRoot, type InstanceDataMap } from '@/templates'
import { getTheme } from '@/themes'
import type { ResolvedLayout } from '@/types/generated'
import { DetailLayer, refTitleKey } from './DetailLayer'
import { GridView } from './GridView'
import { screenHistoryProvider } from './history-provider'
import { screenNow, useScreenStore, type ScreenStore } from './screen-store'
import { ScreenNavigator, type NavConfig } from './state-machine'
import { initTheme } from '@/admin-theme/theme'
import { applyScreenTheme, clearScreenTheme, persistScreenTheme, readStoredScreenTheme } from './theme-apply'
import { useHasTouch } from './touch-capability'
import { useViewportSize, useWakeFrozenSize } from './use-viewport-size'
import { WakeTouchGuard } from './wake-guard'

/** 屏幕向中枢上报的最小接口（ViewportReporter 满足） */
export interface ScreenReporter {
  reportCurrentScreen(id: string): void
  onScreenMode(mode: 'on' | 'off'): void
}

export interface ScreenViewProps {
  store: ScreenStore
  nav: ScreenNavigator
  reporter?: ScreenReporter
}

/** 横向位移达到该像素且明显大于纵向位移才算左右滑动 */
const SWIPE_MIN_PX = 60

const defaultScreenSettings = {
  carousel_mode: 'auto',
  idle_home_seconds: 60,
  default_dwell_seconds: 15,
  input_mode: 'auto',
}

function navConfigOf(layout: ResolvedLayout, s: typeof defaultScreenSettings, touch: boolean): NavConfig {
  return {
    screens: layout.screens.map((sc) => ({
      id: sc.id,
      dwellSeconds: sc.dwell_seconds,
      inRotation: sc.in_rotation,
      widgetIds: sc.widgets.map((w) => w.id),
    })),
    carouselMode: s.carousel_mode === 'home_only' ? 'home_only' : 'auto',
    idleHomeSeconds: s.idle_home_seconds,
    defaultDwellSeconds: s.default_dwell_seconds,
    touch,
  }
}

function titlesOf(layout: ResolvedLayout | null): Map<string, string> {
  const out = new Map<string, string>()
  for (const sc of layout?.screens ?? []) {
    for (const w of sc.widgets) {
      for (const refs of Object.values(w.slots)) {
        for (const r of refs) if (r.title) out.set(refTitleKey(r.instance_id, r.item), r.title)
      }
    }
  }
  return out
}

/**
 * 屏幕端应用主体：把屏幕 store 的布局、状态与数据渲染成一屏网格。
 * 状态机（轮播、触摸、远程命令、详情层）由外部传入的 ScreenNavigator 驱动；
 * 关屏时只渲染纯黑遮罩并卸载小组件，停止一切动效；有触摸时支持滑动切屏与点开详情层。
 */
export function ScreenView({ store, nav, reporter }: ScreenViewProps) {
  const { i18n } = useTranslation()
  // 按切片订阅：时钟校正、连接状态等无关变化不触发整棵网格重渲
  const layout = useScreenStore((s) => s.layout, store)
  const settings = useScreenStore((s) => s.settings, store)
  const screenState = useScreenStore((s) => s.screenState, store)
  const data = useScreenStore((s) => s.data, store)
  const mode = screenState?.mode === 'off' ? 'off' : 'on'
  const { width, height } = useWakeFrozenSize(useViewportSize(), mode)
  const screenSettings = settings?.screen ?? defaultScreenSettings
  const touch = useHasTouch(screenSettings.input_mode)
  const themeId = getTheme(screenState?.theme_id ?? readStoredScreenTheme()).id
  const reduceEffects = settings?.reduce_effects ?? false
  const lang = settings?.language === 'en' ? 'en' : 'zh'
  const timezone = settings?.timezone ?? 'UTC'
  const now = useCallback(() => screenNow(store), [store])

  // 状态机配置随布局与设置更新；重复配置不重置计时
  const navConfig = useMemo(
    () => (layout ? navConfigOf(layout, screenSettings, touch) : null),
    [layout, screenSettings, touch],
  )
  useLayoutEffect(() => {
    if (navConfig) nav.configure(navConfig)
  }, [nav, navConfig])
  const navState = useSyncExternalStore(nav.subscribe, nav.getState)

  // 主题：根元素 data-theme 与 data-reduce-effects 同元素；以服务端状态为准并写回 localStorage
  useLayoutEffect(() => {
    applyScreenTheme(themeId, reduceEffects)
  }, [themeId, reduceEffects])
  // 离开屏幕端时撤掉根元素上的屏幕主题，并恢复管理端主题（管理员预览 /screen 后返回管理页不能串色）
  useLayoutEffect(
    () => () => {
      clearScreenTheme()
      initTheme()
    },
    [],
  )
  const hasScreenState = screenState !== null
  useEffect(() => {
    if (hasScreenState) persistScreenTheme(themeId)
  }, [hasScreenState, themeId])

  useEffect(() => {
    if (i18n.language !== lang) void i18n.changeLanguage(lang)
  }, [i18n, lang])

  // 页面不滚动、不出滚动条
  useEffect(() => {
    const el = document.documentElement
    const prev = el.style.overflow
    el.style.overflow = 'hidden'
    return () => {
      el.style.overflow = prev
    }
  }, [])

  const [wakeGuard] = useState(() => new WakeTouchGuard(now))
  const onWidgetClick = useCallback((id: string) => nav.openDetail(id), [nav])
  useEffect(() => {
    wakeGuard.setMode(mode)
    nav.setActive(mode === 'on')
    reporter?.onScreenMode(mode)
  }, [mode, nav, reporter, wakeGuard])

  const hasLayout = layout !== null
  const currentScreenId = navState.screenId
  useEffect(() => {
    if (hasLayout) reporter?.reportCurrentScreen(currentScreenId)
  }, [hasLayout, currentScreenId, reporter])

  // 触摸：滑动切屏；唤醒后的第一次触摸只点亮不点击
  const gestureRef = useRef<{ start: { x: number; y: number } | null; suppressClick: boolean }>({ start: null, suppressClick: false })
  const onPointerDownCapture = (e: ReactPointerEvent) => {
    const gesture = gestureRef.current
    if (!touch || mode === 'off') return
    gesture.suppressClick = false
    if (wakeGuard.consume()) {
      e.stopPropagation()
      gesture.start = null
      gesture.suppressClick = true
      return
    }
    nav.touch()
    gesture.start = { x: e.clientX, y: e.clientY }
  }
  const onPointerUpCapture = (e: ReactPointerEvent) => {
    const gesture = gestureRef.current
    const start = gesture.start
    gesture.start = null
    if (!start || !touch) return
    const dx = e.clientX - start.x
    const dy = e.clientY - start.y
    if (Math.abs(dx) >= SWIPE_MIN_PX && Math.abs(dx) > Math.abs(dy) * 1.5) {
      gesture.suppressClick = true
      nav.swipe(dx < 0 ? 1 : -1)
    }
  }
  const onClickCapture = (e: ReactMouseEvent) => {
    const gesture = gestureRef.current
    if (!gesture.suppressClick) return
    gesture.suppressClick = false
    e.stopPropagation()
    e.preventDefault()
  }

  const titles = useMemo(() => titlesOf(layout), [layout])
  const screen = layout ? (layout.screens.find((s) => s.id === currentScreenId) ?? layout.screens[0]) : undefined
  const detailWidget = navState.detail
    ? layout?.screens.find((s) => s.id === navState.detail!.screenId)?.widgets.find((w) => w.id === navState.detail!.widgetId)
    : undefined
  const dataMap: InstanceDataMap = data

  return (
    <ScreenEnvProvider now={now} timezone={timezone} lang={lang}>
      <HistoryContext.Provider value={screenHistoryProvider}>
        <ThemeRoot themeId={themeId} reduceEffects={reduceEffects} className="fixed inset-0 overflow-hidden bg-s-bg text-s-fg">
          <div
            data-screen-root
            className="absolute inset-0 select-none"
            style={{ touchAction: 'pan-y' }}
            onPointerDownCapture={onPointerDownCapture}
            onPointerUpCapture={onPointerUpCapture}
            onClickCapture={onClickCapture}
          >
            {mode === 'off' ? (
              <div data-screen-off className="absolute inset-0 z-50" style={{ backgroundColor: 'black' }} />
            ) : (
              <>
                {layout && screen && (
                  <GridView
                    screen={screen}
                    grid={layout.grid}
                    width={width}
                    height={height}
                    data={dataMap}
                    onWidgetClick={touch ? onWidgetClick : undefined}
                  />
                )}
                {detailWidget && (
                  <DetailLayer
                    widget={detailWidget}
                    data={dataMap}
                    titles={titles}
                    onClose={() => nav.closeDetail()}
                    onActivity={() => nav.detailActivity()}
                  />
                )}
              </>
            )}
          </div>
        </ThemeRoot>
      </HistoryContext.Provider>
    </ScreenEnvProvider>
  )
}
