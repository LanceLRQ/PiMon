import { memo } from 'react'
import type { Grid, ResolvedScreen } from '@/types/generated'
import { WidgetView, type InstanceDataMap } from '@/templates'
import { computeGrid, widgetRect } from './grid'

export interface GridViewProps {
  screen: ResolvedScreen
  grid: Grid
  /** 容器像素尺寸；屏幕根传 viewport，编辑器画布与缩略图传各自容器的尺寸 */
  width: number
  height: number
  data: InstanceDataMap
  /** 给出时小组件可点击（有触摸时点开详情层） */
  onWidgetClick?: (widgetId: string) => void
  /** 高亮某个小组件（严重告警跳转，M4 使用） */
  highlightId?: string
  className?: string
}

/**
 * 网格渲染：一个 screen 一屏显示，容器 overflow hidden、无滚动；
 * 单元格尺寸由容器尺寸与列行数算出，小组件按位置绝对定位并渲染 D5 的 WidgetView。
 * 小组件间距取主题的 --grid-gap，每侧各收一半。需要放在 data-theme 子树内。
 */
function GridViewImpl({ screen, grid, width, height, data, onWidgetClick, highlightId, className }: GridViewProps) {
  const metrics = computeGrid(width, height, grid)
  return (
    <div
      data-screen-grid
      data-screen-id={screen.id}
      className={`relative overflow-hidden ${className ?? ''}`}
      style={{ width, height }}
    >
      {screen.widgets.map((widget) => {
        const rect = widgetRect(metrics, widget)
        return (
          <div
            key={widget.id}
            data-widget-id={widget.id}
            data-clickable={onWidgetClick ? 'true' : undefined}
            data-highlight={widget.id === highlightId ? 'true' : undefined}
            className="absolute box-border"
            style={{
              left: rect.left,
              top: rect.top,
              width: rect.width,
              height: rect.height,
              padding: 'calc(var(--grid-gap, 0px) / 2)',
              ...(widget.id === highlightId ? { outline: '3px solid var(--primary)', outlineOffset: '-3px' } : null),
            }}
            onClick={onWidgetClick ? () => onWidgetClick(widget.id) : undefined}
          >
            <div className="h-full w-full">
              <WidgetView widget={widget} data={data} />
            </div>
          </div>
        )
      })}
    </div>
  )
}

/** props 没变时不重渲：树莓派上时钟校正等无关变化不应带动整棵网格 */
export const GridView = memo(GridViewImpl)
