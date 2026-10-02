import { useMemo } from 'react'
import { screenHistoryProvider } from '@/screen/history-provider'
import { GridView } from '@/screen/GridView'
import { HistoryContext, ScreenEnvProvider, ThemeRoot, type InstanceDataMap } from '@/templates'
import type { ThemeId } from '@/themes'
import type { Grid, Instance, LayoutScreen, PluginInfo, ResolvedScreen } from '@/types/generated'
// 三套主题 token 只在用到屏幕渲染的模块引入，不进 main.tsx（管理端与屏幕端共用 index.html）
import '@/themes/index.css'
import { DEFAULT_VIEWPORT } from './geometry'
import { referencedInstanceIds, resolveScreen } from './resolve-draft'
import { useCanvasData } from './use-canvas-data'

export interface ScreenThumbProps {
  screen: ResolvedScreen
  grid: Grid
  data: InstanceDataMap
  themeId: ThemeId
  reduceEffects: boolean
  /** 显示器视口（CSS 像素），缩略图按它的比例渲染后整体缩小 */
  viewport: { w: number; h: number }
  /** 缩略图显示宽度（像素） */
  width: number
  lang: 'zh' | 'en'
  timezone: string
  now: () => number
  label: string
}

/**
 * 缩略图：与编辑器画布同一套渲染（屏幕端 GridView + 模板），在视口尺寸下排版后用 transform 缩小，
 * 不用 iframe（/screen 响应 X-Frame-Options: DENY）。只读，不响应指针。
 */
export function ScreenThumb({ screen, grid, data, themeId, reduceEffects, viewport, width, lang, timezone, now, label }: ScreenThumbProps) {
  const vw = viewport.w > 0 ? viewport.w : DEFAULT_VIEWPORT.w
  const vh = viewport.h > 0 ? viewport.h : DEFAULT_VIEWPORT.h
  // 外框带 1px 描边（border-box）：内容区比 width 窄 2px，缩放按内容区算，内层不会溢出外框
  const scale = (width - 2) / vw
  return (
    <div
      role="img"
      aria-label={label}
      data-testid="screen-thumb"
      data-screen-id={screen.id}
      className="relative overflow-hidden border border-line-strong bg-black"
      style={{ width, height: Math.round(vh * scale) + 2 }}
    >
      <div aria-hidden inert className="pointer-events-none absolute top-0 left-0" style={{ width: vw, height: vh, transform: `scale(${scale})`, transformOrigin: '0 0' }}>
        <ScreenEnvProvider now={now} timezone={timezone} lang={lang}>
          <HistoryContext.Provider value={screenHistoryProvider}>
            <ThemeRoot themeId={themeId} reduceEffects={reduceEffects} className="absolute inset-0 overflow-hidden bg-s-bg text-s-fg">
              <GridView screen={screen} grid={grid} width={vw} height={vh} data={data} />
            </ThemeRoot>
          </HistoryContext.Provider>
        </ScreenEnvProvider>
      </div>
    </div>
  )
}

/**
 * 一组 screen 的缩略图数据：本地解析（与画布同口径，随草稿实时变化）加一次合并取数。
 * 取不到数据时数据项为空，模板显示为未知，不当作零。
 */
export function useThumbScreens(
  screens: readonly LayoutScreen[],
  plugins: readonly PluginInfo[],
  instances: readonly Instance[],
): { resolved: ResolvedScreen[]; data: InstanceDataMap } {
  const resolved = useMemo(() => screens.map((s) => resolveScreen(s, { plugins, instances })), [screens, plugins, instances])
  const ids = useMemo(() => referencedInstanceIds(resolved.flatMap((s) => s.widgets)), [resolved])
  const data = useCanvasData(ids, instances)
  return { resolved, data }
}
