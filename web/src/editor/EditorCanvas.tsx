import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type PointerEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { screenHistoryProvider } from '@/screen/history-provider'
import { computeGrid, widgetRect } from '@/screen/grid'
import { GridView } from '@/screen/GridView'
import { HistoryContext, ScreenEnvProvider, ThemeRoot, useThemeRuntime, type InstanceDataMap } from '@/templates'
import type { ThemeRuntime } from '@/themes'
import type { ThemeId } from '@/themes'
import type { Grid, ResolvedScreen, WidgetSize } from '@/types/generated'
// 三套主题 token 只在编辑器画布与屏幕根引入，不进 main.tsx（管理端与屏幕端共用 index.html）
import '@/themes/index.css'
import { cn } from '@/lib/utils'
import { DEFAULT_VIEWPORT, RULER_SIZE, fitCanvasScale, pointerToScreen } from './geometry'
import { evaluatePlacement, snapToCell, type Cell, type GridWidgetRect } from './grid-ops'

export interface DragIntent {
  /** 画布上移动已有小组件时的 id；从库拖入时为空 */
  id?: string
  size: WidgetSize
}

export interface EditorCanvasProps {
  screen: ResolvedScreen
  grid: Grid
  data: InstanceDataMap
  themeId: ThemeId
  reduceEffects: boolean
  viewport: { w: number; h: number }
  selectedId: string | null
  /** 被拒绝的操作里冲突的小组件，红框高亮 */
  conflictIds: readonly string[]
  /** 从库拖入中的条目；canvas 据此画落点幽灵块 */
  dragEntry: DragIntent | null
  lang: 'zh' | 'en'
  timezone: string
  now: () => number
  onSelect: (id: string | null) => void
  onMove: (id: string, cell: Cell) => void
  onDropEntry: (cell: Cell) => void
  /** 主题运行时读数变化（含主题切换后重新 watch）时回调 */
  onThemeRuntime?: (rt: ThemeRuntime) => void
}

interface Ghost {
  cell: Cell
  size: WidgetSize
  ok: boolean
}

const MOVE_THRESHOLD_PX = 3

function RuntimeReporter({ onRuntime }: { onRuntime: (rt: ThemeRuntime) => void }) {
  const rt = useThemeRuntime()
  useEffect(() => {
    onRuntime(rt)
  }, [rt, onRuntime])
  return null
}

/** 标尺：列号与行号，选中小组件占用的范围琥珀高亮，与检查器的列、行联动 */
function Rulers({ cols, rows, cellW, cellH, scale, sel }: { cols: number; rows: number; cellW: number; cellH: number; scale: number; sel: GridWidgetRect | null }) {
  const cls = (on: boolean) =>
    cn('absolute flex items-center justify-center border-border font-mono text-[10px] text-muted-foreground', on && 'bg-signal text-primary-foreground')
  return (
    <>
      <div aria-hidden className="absolute top-0 right-0 h-[22px]" style={{ left: RULER_SIZE }}>
        {Array.from({ length: cols }, (_, i) => {
          const on = !!sel && i >= sel.col && i < sel.col + sel.w
          return (
            <span key={i} data-ruler="x" data-index={i + 1} data-highlight={on ? 'true' : undefined} className={cn(cls(on), 'inset-y-0 border-x')} style={{ left: i * cellW * scale, width: cellW * scale }}>
              {i + 1}
            </span>
          )
        })}
      </div>
      <div aria-hidden className="absolute bottom-0 left-0 w-[22px]" style={{ top: RULER_SIZE }}>
        {Array.from({ length: rows }, (_, j) => {
          const on = !!sel && j >= sel.row && j < sel.row + sel.h
          return (
            <span key={j} data-ruler="y" data-index={j + 1} data-highlight={on ? 'true' : undefined} className={cn(cls(on), 'inset-x-0 border-y')} style={{ top: j * cellH * scale, height: cellH * scale }}>
              {j + 1}
            </span>
          )
        })}
      </div>
    </>
  )
}

/**
 * 编辑器画布：按屏幕主题渲染真实小组件（复用屏幕端的 GridView），整体缩放进可用区域；
 * 上层是同位置的交互层（选中、拖动、幽灵块）与标尺。主题 token 通过画布容器上的 data-theme 生效。
 */
export function EditorCanvas(props: EditorCanvasProps) {
  const { screen, grid, data, themeId, reduceEffects, selectedId, conflictIds, dragEntry, lang, timezone, now, onSelect, onMove, onDropEntry, onThemeRuntime } = props
  const { t } = useTranslation()
  const vw = props.viewport.w > 0 ? props.viewport.w : DEFAULT_VIEWPORT.w
  const vh = props.viewport.h > 0 ? props.viewport.h : DEFAULT_VIEWPORT.h
  const areaRef = useRef<HTMLDivElement>(null)
  const bezelRef = useRef<HTMLDivElement>(null)
  const [scale, setScale] = useState(1)
  const [ghost, setGhost] = useState<Ghost | null>(null)
  const moveRef = useRef<{ id: string; pointer: number; offX: number; offY: number; startX: number; startY: number; active: boolean } | null>(null)

  useLayoutEffect(() => {
    const el = areaRef.current
    if (!el) return
    const measure = () => setScale(fitCanvasScale(el.clientWidth, el.clientHeight, vw, vh))
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [vw, vh])

  const metrics = useMemo(() => computeGrid(vw, vh, grid), [vw, vh, grid])
  const rects = useMemo<GridWidgetRect[]>(
    () => screen.widgets.map((w) => ({ id: w.id, col: w.col, row: w.row, w: w.size.cols, h: w.size.rows })),
    [screen.widgets],
  )
  const selected = rects.find((r) => r.id === selectedId) ?? null

  const bezelPoint = (clientX: number, clientY: number) => {
    const rect = bezelRef.current?.getBoundingClientRect() ?? { left: 0, top: 0 }
    return pointerToScreen(clientX, clientY, rect, scale)
  }
  const ghostAt = (left: number, top: number, size: WidgetSize, id?: string): Ghost => {
    const cell = snapToCell(left, top, metrics, size)
    const cand = { id: id ?? '', col: cell.col, row: cell.row, w: size.cols, h: size.rows }
    return { cell, size, ok: evaluatePlacement(grid, rects, cand).ok }
  }

  // 移动已有小组件：指针按下选中，位移超过阈值才算拖动；落点按格吸附，放不下显示红色幽灵块
  const onWidgetPointerDown = (e: PointerEvent<HTMLElement>, id: string) => {
    if (e.button !== 0) return
    onSelect(id)
    const r = rects.find((x) => x.id === id)
    if (!r) return
    const p = bezelPoint(e.clientX, e.clientY)
    const wr = widgetRect(metrics, { col: r.col, row: r.row, size: { cols: r.w, rows: r.h } })
    moveRef.current = { id, pointer: e.pointerId, offX: p.x - wr.left, offY: p.y - wr.top, startX: e.clientX, startY: e.clientY, active: false }
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onWidgetPointerMove = (e: PointerEvent<HTMLElement>) => {
    const m = moveRef.current
    if (!m || m.pointer !== e.pointerId) return
    if (!m.active && Math.hypot(e.clientX - m.startX, e.clientY - m.startY) < MOVE_THRESHOLD_PX) return
    m.active = true
    const r = rects.find((x) => x.id === m.id)
    if (!r) return
    const p = bezelPoint(e.clientX, e.clientY)
    setGhost(ghostAt(p.x - m.offX, p.y - m.offY, { cols: r.w, rows: r.h }, m.id))
  }
  const endWidgetPointer = (e: PointerEvent<HTMLElement>, commit: boolean) => {
    const m = moveRef.current
    if (!m || m.pointer !== e.pointerId) return
    moveRef.current = null
    e.currentTarget.releasePointerCapture?.(e.pointerId)
    setGhost(null)
    if (!commit || !m.active) return
    // 松手时按 pointerup 的坐标重新算落点，不依赖上一帧的 ghost 状态
    const r = rects.find((x) => x.id === m.id)
    if (!r) return
    const p = bezelPoint(e.clientX, e.clientY)
    onMove(m.id, ghostAt(p.x - m.offX, p.y - m.offY, { cols: r.w, rows: r.h }, m.id).cell)
  }

  // 从库拖入：落点以指针为中心
  const onDragOver = (e: DragEvent<HTMLElement>) => {
    if (!dragEntry) return
    e.preventDefault()
    const p = bezelPoint(e.clientX, e.clientY)
    const wr = widgetRect(metrics, { col: 0, row: 0, size: dragEntry.size })
    setGhost(ghostAt(p.x - wr.width / 2, p.y - wr.height / 2, dragEntry.size))
  }
  const onDrop = (e: DragEvent<HTMLElement>) => {
    if (!dragEntry) return
    e.preventDefault()
    const p = bezelPoint(e.clientX, e.clientY)
    const wr = widgetRect(metrics, { col: 0, row: 0, size: dragEntry.size })
    const g = ghostAt(p.x - wr.width / 2, p.y - wr.height / 2, dragEntry.size)
    setGhost(null)
    onDropEntry(g.cell)
  }

  const bw = Math.floor(vw * scale)
  const bh = Math.floor(vh * scale)
  const ghostRect = ghost ? widgetRect(metrics, { col: ghost.cell.col, row: ghost.cell.row, size: ghost.size }) : null

  return (
    <div
      ref={areaRef}
      data-testid="editor-canvas-area"
      aria-label={t('layoutEd.canvas.label')}
      className="grid min-h-0 min-w-0 flex-1 place-items-center overflow-hidden bg-panel-2 p-3"
      onClick={(e) => {
        if (e.target === e.currentTarget) onSelect(null)
      }}
    >
      <div className="relative" style={{ width: bw + RULER_SIZE, height: bh + RULER_SIZE }} onClick={(e) => e.target === e.currentTarget && onSelect(null)}>
        <Rulers cols={grid.cols} rows={grid.rows} cellW={metrics.cellW} cellH={metrics.cellH} scale={scale} sel={selected} />
        <div
          ref={bezelRef}
          data-testid="editor-bezel"
          className="absolute overflow-hidden border border-line-strong bg-black"
          style={{ left: RULER_SIZE, top: RULER_SIZE, width: bw, height: bh }}
          onDragOver={onDragOver}
          onDragLeave={() => setGhost(null)}
          onDrop={onDrop}
          onClick={(e) => {
            if (e.target === e.currentTarget) onSelect(null)
          }}
        >
          <div style={{ width: vw, height: vh, transform: `scale(${scale})`, transformOrigin: '0 0' }} className="relative">
            <ScreenEnvProvider now={now} timezone={timezone} lang={lang}>
              <HistoryContext.Provider value={screenHistoryProvider}>
                <ThemeRoot themeId={themeId} reduceEffects={reduceEffects} className="absolute inset-0 overflow-hidden bg-s-bg text-s-fg">
                  {onThemeRuntime && <RuntimeReporter onRuntime={onThemeRuntime} />}
                  <GridView screen={screen} grid={grid} width={vw} height={vh} data={data} />
                </ThemeRoot>
              </HistoryContext.Provider>
            </ScreenEnvProvider>
            {/* 交互层：与小组件同位置，放在主题子树之外 */}
            <div className="absolute inset-0" data-testid="editor-overlay" onClick={(e) => e.target === e.currentTarget && onSelect(null)}>
              {screen.widgets.map((w) => {
                const rect = widgetRect(metrics, { col: w.col, row: w.row, size: w.size })
                const isSel = w.id === selectedId
                const isConflict = conflictIds.includes(w.id)
                return (
                  <div
                    key={w.id}
                    role="button"
                    tabIndex={0}
                    data-testid={`editor-widget-${w.id}`}
                    data-editor-widget=""
                    data-selected={isSel ? 'true' : undefined}
                    data-conflict={isConflict ? 'true' : undefined}
                    aria-pressed={isSel}
                    aria-label={t('layoutEd.canvas.widget', { title: w.title || w.id, col: w.col + 1, row: w.row + 1, size: `${w.size.cols}×${w.size.rows}` })}
                    className={cn('absolute cursor-grab touch-none outline-none', isSel && 'cursor-grabbing')}
                    style={{
                      left: rect.left,
                      top: rect.top,
                      width: rect.width,
                      height: rect.height,
                      boxShadow: isConflict ? 'inset 0 0 0 3px var(--status-crit)' : isSel ? 'inset 0 0 0 2px var(--signal)' : undefined,
                    }}
                    onPointerDown={(e) => onWidgetPointerDown(e, w.id)}
                    onPointerMove={onWidgetPointerMove}
                    onPointerUp={(e) => endWidgetPointer(e, true)}
                    onPointerCancel={(e) => endWidgetPointer(e, false)}
                    onFocus={() => !isSel && onSelect(w.id)}
                  >
                    {isSel && (
                      <span className={cn('absolute right-0 bg-signal px-1.5 font-mono text-[11px] whitespace-nowrap text-primary-foreground', w.row === 0 ? 'top-full' : 'bottom-full')}>
                        c{w.col + 1} r{w.row + 1} · {w.size.cols}×{w.size.rows}
                      </span>
                    )}
                  </div>
                )
              })}
              {ghost && ghostRect && (
                <div
                  data-testid="editor-ghost"
                  data-ok={ghost.ok ? 'true' : 'false'}
                  className={cn('pointer-events-none absolute grid place-items-center border-2 border-dashed font-mono text-sm', ghost.ok ? 'border-signal bg-signal/20 text-signal-text' : 'border-status-crit bg-status-crit/25 text-status-crit')}
                  style={{ left: ghostRect.left, top: ghostRect.top, width: ghostRect.width, height: ghostRect.height }}
                >
                  {ghost.ok ? `c${ghost.cell.col + 1} r${ghost.cell.row + 1}` : t('layoutEd.canvas.noRoom')}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
