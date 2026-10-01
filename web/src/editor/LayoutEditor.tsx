import { Monitor, Power } from 'lucide-react'
import { useCallback, useEffect, useMemo, useReducer, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { usePlugins } from '@/pages/instances/use-plugins'
import { cn } from '@/lib/utils'
import { useIsMobile } from '@/lib/use-mobile'
import { selectInstances, selectSettings, serverNow, useLiveStore } from '@/store/live-store'
import { DEFAULT_THEME_ID, isThemeId, themes, type ThemeId, type ThemeRuntime } from '@/themes'
import type { Grid, LayoutState, ScreenStatus, WidgetCatalog, WidgetSize } from '@/types/generated'
import { Button } from '@/ui/button'
import { NumberTag } from '@/ui/numbered-label'
import { Note } from '@/ui/note'
import { Select } from '@/ui/select'
import { useToast } from '@/ui/toast'
import { DEFAULT_VIEWPORT } from './geometry'
import { EditorCanvas, type DragIntent } from './EditorCanvas'
import { firstFreeSpot, moveByKey, occupiedCells, type Cell } from './grid-ops'
import { Inspector } from './Inspector'
import { allowedSizesOf, buildLibrary, createWidget, newWidgetId, templateOf, type LibraryEntry } from './library'
import { resolveScreen, referencedInstanceIds } from './resolve-draft'
import { currentScreen, editorReducer, gridShrinkConflicts, initialEditorState, toRect, type Rejection } from './state'
import { useCanvasData } from './use-canvas-data'
import { WidgetLibrary } from './WidgetLibrary'

const gridPresets: Grid[] = [
  { cols: 6, rows: 4 },
  { cols: 8, rows: 5 },
  { cols: 10, rows: 6 },
]

const editableTarget = (t: EventTarget | null) => t instanceof HTMLElement && t.closest('input, textarea, select, [contenteditable="true"]') !== null

interface ServerData {
  catalog: WidgetCatalog | null
  status: ScreenStatus | null
}

export function LayoutEditor() {
  const mobile = useIsMobile()
  return mobile ? <MobileNotice /> : <EditorBody />
}

function MobileNotice() {
  const { t } = useTranslation()
  return (
    <div className="p-3.5">
      <div className="mb-3 flex items-baseline gap-2.5">
        <NumberTag no="02b" />
        <h1 className="text-[17px] font-medium">{t('pages.layoutEditor')}</h1>
      </div>
      <Note tone="info" icon={<Monitor size={16} />}>
        <b>{t('layoutEd.mobile.title')}</b>
        <br />
        {t('layoutEd.mobile.body')}
      </Note>
      <p className="mt-3">
        <Button asChild variant="outline" size="sm">
          <Link to="/screens/remote">
            <Power size={15} />
            {t('layoutEd.mobile.remote')}
          </Link>
        </Button>
      </p>
    </div>
  )
}

function rejectionMessage(t: (k: string, o?: Record<string, unknown>) => string, r: Rejection): string {
  if (r.reason === 'out_of_bounds' && !r.widgetId) return t('layoutEd.reject.gridShrink', { n: r.conflicts.length })
  return t(`layoutEd.reject.${r.reason}`)
}

function EditorBody() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [state, dispatch] = useReducer(editorReducer, undefined, initialEditorState)
  const [server, setServer] = useState<ServerData | null>(null)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [previewTheme, setPreviewTheme] = useState<ThemeId | null>(null)
  const [dragEntry, setDragEntry] = useState<(DragIntent & { entry: LibraryEntry }) | null>(null)
  const [runtime, setRuntime] = useState<ThemeRuntime | null>(null)
  const plugins = usePlugins()
  const instances = useLiveStore(selectInstances)
  const settings = useLiveStore(selectSettings)
  const lang = i18n.language === 'en' ? 'en' : 'zh'

  useEffect(() => {
    const ctrl = new AbortController()
    Promise.all([
      http.get<LayoutState>('/api/screens', { signal: ctrl.signal }),
      http.get<WidgetCatalog>('/api/screens/catalog', { signal: ctrl.signal }),
      // 显示器状态只用于画布比例与默认主题，取不到不影响编辑
      http.get<ScreenStatus>('/api/screen/status', { signal: ctrl.signal }).catch(() => null),
    ])
      .then(([layout, catalog, status]) => {
        setLoadError(null)
        setServer({ catalog, status })
        dispatch({ type: 'load', version: layout.version, layout: layout.layout })
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) setLoadError(e)
      })
    return () => ctrl.abort()
  }, [reloadKey])

  const catalog = server?.catalog ?? null
  const pluginList = useMemo(() => plugins.list?.plugins ?? [], [plugins.list])
  const library = useMemo(
    () => buildLibrary(pluginList, catalog, (tpl) => t(`layoutEd.template.${tpl}`, { defaultValue: tpl })),
    [pluginList, catalog, t],
  )
  const screen = currentScreen(state)
  const grid = state.draft.grid
  const widgets = useMemo(() => screen?.widgets ?? [], [screen])
  const rects = useMemo(() => widgets.map(toRect), [widgets])
  const selected = widgets.find((w) => w.id === state.selectedId) ?? null

  const resolved = useMemo(
    () => (screen ? resolveScreen(screen, { plugins: pluginList, instances }) : null),
    [screen, pluginList, instances],
  )
  const instanceIds = useMemo(() => referencedInstanceIds(resolved?.widgets ?? []), [resolved])
  const data = useCanvasData(instanceIds, instances)

  const statusTheme = server?.status?.state.theme_id
  const themeId: ThemeId = previewTheme ?? (isThemeId(statusTheme) ? statusTheme : DEFAULT_THEME_ID)
  const viewport = server?.status?.viewport ? { w: server.status.viewport.w, h: server.status.viewport.h } : DEFAULT_VIEWPORT
  const now = useCallback(() => serverNow(), [])

  // 操作被拒绝：toast 说明并让冲突块红框高亮一会儿
  const rejection = state.rejection
  useEffect(() => {
    if (!rejection) return
    toast.show(rejectionMessage(t, rejection), 'warn')
    const timer = setTimeout(() => dispatch({ type: 'clearRejection' }), 2500)
    return () => clearTimeout(timer)
  }, [rejection, t, toast])

  const addEntry = (entry: LibraryEntry, cell?: Cell) => {
    // 点「添加」时取第一个放得下的尺寸与空位；拖入时用库里第一个尺寸，落点由指针决定
    const size = cell ? entry.sizes[0] : entry.sizes.find((s) => firstFreeSpot(grid, rects, s))
    const at = cell ?? (size ? firstFreeSpot(grid, rects, size) : null)
    if (!size || !at) {
      toast.show(t('layoutEd.reject.no_space'), 'warn')
      return
    }
    const ids = new Set(state.draft.screens.flatMap((s) => s.widgets.map((w) => w.id)))
    dispatch({ type: 'add', widget: createWidget(entry, size, at, newWidgetId(ids), instances) })
  }

  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (editableTarget(e.target)) return
    if (e.key === 'Escape') {
      dispatch({ type: 'select', id: null })
      return
    }
    if (!selected) return
    if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault()
      dispatch({ type: 'remove', id: selected.id })
      return
    }
    if (!e.key.startsWith('Arrow')) return
    e.preventDefault()
    const next = moveByKey(e.key, grid, rects, toRect(selected))
    if (next) return dispatch({ type: 'move', id: selected.id, ...next })
    // 走不动时把想去的格交给状态层，给出越界或冲突的反馈
    const d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key]
    if (d) dispatch({ type: 'move', id: selected.id, col: selected.col + d[0], row: selected.row + d[1] })
  }

  const onGridChange = (value: string) => {
    const [cols, rows] = value.split('x').map(Number)
    dispatch({ type: 'setGrid', grid: { cols, rows } })
  }

  if (loadError) {
    return (
      <div className="p-6">
        <Note tone="crit" role="alert">
          {t('layoutEd.loadFailed', { error: translateErrorValue(i18n, loadError) })}
          <div className="mt-2">
            <Button variant="outline" size="sm" onClick={() => setReloadKey((k) => k + 1)}>
              {t('layoutEd.retry')}
            </Button>
          </div>
        </Note>
      </div>
    )
  }
  if (!server || !screen || !resolved) {
    return <div role="status" className="p-6 text-[13px] text-muted-foreground">{t('layoutEd.loading')}</div>
  }

  const gridOptions = gridPresets.some((g) => g.cols === grid.cols && g.rows === grid.rows) ? gridPresets : [...gridPresets, grid]
  const shrinkHint = (g: Grid) => gridShrinkConflicts(state.draft, g).length

  const allowedSizes: WidgetSize[] = selected ? allowedSizesOf(selected, pluginList, catalog) : []
  const tpl = selected ? templateOf(selected, pluginList) || selected.template || '' : ''
  const status = server.status
  const occupied = occupiedCells(grid, rects)

  return (
    <div data-testid="layout-editor" className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-card px-4 py-2">
        <div className="flex items-baseline gap-2.5">
          <NumberTag no="02" className="border-foreground text-foreground" />
          <h1 className="text-[15px] font-medium">{t('pages.layoutEditor')}</h1>
        </div>
        <div role="tablist" aria-label={t('layoutEd.tabs')} className="flex gap-1">
          {state.draft.screens.map((s) => (
            <button
              key={s.id}
              type="button"
              role="tab"
              aria-selected={s.id === screen.id}
              onClick={() => dispatch({ type: 'selectScreen', id: s.id })}
              className={cn(
                'inline-flex h-8 items-center gap-1.5 rounded-[2px] border px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
                s.id === screen.id ? 'border-foreground bg-inv-bg text-inv-ink' : 'border-border hover:bg-panel-2',
              )}
            >
              <span className="font-mono text-[11px] opacity-70">{s.id}</span>
              {s.name}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-3 text-[12.5px]">
          <label className="flex items-center gap-1.5">
            <span className="text-muted-foreground">{t('layoutEd.grid.label')}</span>
            <Select aria-label={t('layoutEd.grid.label')} value={`${grid.cols}x${grid.rows}`} onChange={(e) => onGridChange(e.target.value)} className="w-[110px]">
              {gridOptions.map((g) => (
                <option key={`${g.cols}x${g.rows}`} value={`${g.cols}x${g.rows}`}>
                  {g.cols}×{g.rows}
                  {shrinkHint(g) > 0 ? ` · ${t('layoutEd.grid.oob', { n: shrinkHint(g) })}` : ''}
                </option>
              ))}
            </Select>
          </label>
          <span className="font-mono text-muted-foreground" data-testid="viewport-info">
            {viewport.w}×{viewport.h}
            {status ? ` · ${status.online ? t('layoutEd.screen.online') : t('layoutEd.screen.offline')}` : ''}
          </span>
          <span className="font-mono text-muted-foreground" data-testid="base-version">
            {t('layoutEd.baseVersion', { version: state.baseVersion })}
          </span>
        </div>
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-[260px_minmax(0,1fr)_300px]">
        <WidgetLibrary
          entries={library}
          onAdd={(e) => addEntry(e)}
          onDragStart={(entry) => setDragEntry({ entry, size: entry.sizes[0] })}
          onDragEnd={() => setDragEntry(null)}
        />
        <div className="flex min-h-0 min-w-0 flex-col">
          <div className="flex items-center gap-3 border-b border-border bg-card px-3.5 py-1.5 text-[12px]">
            <span>
              <b className="font-medium">{screen.id}</b> {screen.name}
            </span>
            <span className="font-mono text-muted-foreground" data-testid="stage-info">
              {runtime?.themeId ?? themeId} · {viewport.w}×{viewport.h} · {grid.cols}×{grid.rows} · {t('layoutEd.stage.occupied', { used: occupied, total: grid.cols * grid.rows })}
            </span>
            <span className="flex-1" />
            <label className="flex items-center gap-1.5">
              <span className="text-muted-foreground">{t('layoutEd.stage.preview')}</span>
              <Select aria-label={t('layoutEd.stage.preview')} value={themeId} onChange={(e) => setPreviewTheme(e.target.value as ThemeId)} className="w-[150px]">
                {themes.map((th) => (
                  <option key={th.id} value={th.id}>
                    {th.name[lang]}
                  </option>
                ))}
              </Select>
            </label>
          </div>
          <EditorCanvas
            screen={resolved}
            grid={grid}
            data={data}
            themeId={themeId}
            reduceEffects={settings?.reduce_effects ?? false}
            viewport={viewport}
            selectedId={state.selectedId}
            conflictIds={state.rejection?.conflicts ?? []}
            dragEntry={dragEntry}
            lang={lang}
            timezone={settings?.timezone ?? 'UTC'}
            now={now}
            onSelect={(id) => dispatch({ type: 'select', id })}
            onMove={(id, cell) => dispatch({ type: 'move', id, ...cell })}
            onDropEntry={(cell) => {
              if (dragEntry) addEntry(dragEntry.entry, cell)
              setDragEntry(null)
            }}
            onKeyDown={onKeyDown}
            onThemeRuntime={setRuntime}
          />
          <div className="flex flex-wrap gap-x-4 gap-y-1 border-t border-border bg-card px-3.5 py-1.5 text-[11.5px] text-muted-foreground">
            <span><kbd className="font-mono">{t('layoutEd.keys.dragKey')}</kbd> {t('layoutEd.keys.drag')}</span>
            <span><kbd className="font-mono">← ↑ → ↓</kbd> {t('layoutEd.keys.move')}</span>
            <span><kbd className="font-mono">del</kbd> {t('layoutEd.keys.delete')}</span>
            <span><kbd className="font-mono">esc</kbd> {t('layoutEd.keys.esc')}</span>
          </div>
        </div>
        <aside aria-label={t('layoutEd.insp.title')} className="flex min-h-0 flex-col border-l border-border bg-card">
          <div className="flex h-10 items-center gap-2.5 border-b border-border px-3.5">
            <NumberTag no="02.3" />
            <h2 className="text-[14px] font-medium">{t('layoutEd.insp.title')}</h2>
            <span className="ml-auto text-[12px] text-muted-foreground">{selected ? t('layoutEd.insp.metaWidget') : t('layoutEd.insp.metaScreen')}</span>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            <Inspector
              widget={selected}
              screen={screen}
              grid={grid}
              plugins={pluginList}
              instances={instances}
              template={tpl}
              allowedSizes={allowedSizes}
              onMove={(cell) => selected && dispatch({ type: 'move', id: selected.id, ...cell })}
              onResize={(size) => selected && dispatch({ type: 'resize', id: selected.id, size, allowed: allowedSizes })}
              onOptions={(patch) => selected && dispatch({ type: 'setOptions', id: selected.id, patch })}
              onBinding={(binding) => selected && dispatch({ type: 'setBinding', id: selected.id, binding })}
              onRemove={() => selected && dispatch({ type: 'remove', id: selected.id })}
            />
          </div>
        </aside>
      </div>
    </div>
  )
}
