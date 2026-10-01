import { History, Monitor, Power, Undo2, Upload } from 'lucide-react'
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { UnsavedGuard } from '@/app/unsaved-guard'
import { translateErrorValue } from '@/i18n/errors'
import { usePlugins } from '@/pages/instances/use-plugins'
import { cn } from '@/lib/utils'
import { useIsMobile } from '@/lib/use-mobile'
import { selectInstances, selectSettings, serverNow, useLiveStore } from '@/store/live-store'
import { DEFAULT_THEME_ID, isThemeId, themes, type ThemeId, type ThemeRuntime } from '@/themes'
import type { Grid, LayoutState, ScreenStatus, WidgetCatalog, WidgetSize } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { OutOfBoundsDialog, type OutOfBoundsItem } from '@/ui/out-of-bounds-dialog'
import { NumberTag } from '@/ui/numbered-label'
import { Note } from '@/ui/note'
import { Segmented } from '@/ui/segmented'
import { Select } from '@/ui/select'
import { useToast } from '@/ui/toast'
import { ChangeList } from './ChangeList'
import { diffLayouts, widgetLabel, type Change } from './changes'
import { ConflictDialog } from './ConflictDialog'
import { DEFAULT_VIEWPORT } from './geometry'
import { HistorySheet } from './HistorySheet'
import { MAX_SCREENS, nextScreenId } from './screen-rules'
import { ScreenTabs } from './ScreenTabs'
import { EditorCanvas, type DragIntent } from './EditorCanvas'
import { firstFreeSpot, moveByKey, occupiedCells, type Cell } from './grid-ops'
import { Inspector } from './Inspector'
import { allowedSizesOf, buildLibrary, createWidget, newWidgetId, templateOf, type LibraryEntry } from './library'
import { applyPreviewState, previewStates, type PreviewState } from './preview-state'
import { resolveScreen, referencedInstanceIds } from './resolve-draft'
import { changeList, currentScreen, editorReducer, type EditorAction, gridShrinkConflicts, initialEditorState, toRect, type Rejection } from './state'
import { useCanvasData } from './use-canvas-data'
import { WidgetLibrary } from './WidgetLibrary'

const gridPresets: Grid[] = [
  { cols: 6, rows: 4 },
  { cols: 8, rows: 5 },
  { cols: 10, rows: 6 },
]

// 输入控件里的按键归输入控件；单选组与标签页自己用方向键切换
const editableTarget = (t: EventTarget | null) => t instanceof HTMLElement && t.closest('input, textarea, select, [contenteditable="true"], [contenteditable=""]') !== null
// 焦点在按钮等可交互控件上时，Delete、Backspace 与方向键归控件自己（画布上的小组件除外，它们靠焦点选中）
const interactiveTarget = (t: EventTarget | null) =>
  t instanceof HTMLElement &&
  t.closest('[data-editor-widget]') === null &&
  t.closest('button, a[href], summary, [role="button"], [role="tab"], [role="radio"], [role="checkbox"], [role="switch"], [role="option"], [role="menuitem"]') !== null
const ownsArrows = (t: EventTarget | null) => t instanceof HTMLElement && t.closest('[role="radiogroup"], [role="tablist"]') !== null

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
  if (r.undo) return t(r.reason === 'overlap' ? 'layoutEd.undo.conflict' : 'layoutEd.undo.outOfBounds')
  if (r.reason === 'out_of_bounds' && !r.widgetId) return t('layoutEd.reject.gridShrink', { n: r.conflicts.length })
  return t(`layoutEd.reject.${r.reason}`)
}

interface ConflictInfo {
  latestVersion: number
  theirs: Change[] | null
  busy: 'view' | 'overwrite' | null
  error: string | null
}

function EditorBody() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [state, rawDispatch] = useReducer(editorReducer, undefined, initialEditorState)
  // 保存在途时锁定所有编辑：成功后会用服务端返回整份替换草稿，期间的编辑会丢失（load 例外）
  const savingRef = useRef(false)
  const dispatch = useCallback((a: EditorAction) => {
    if (savingRef.current && a.type !== 'load') return
    rawDispatch(a)
  }, [])
  const [editingScreen, setEditingScreen] = useState<string | null>(null)
  const [server, setServer] = useState<ServerData | null>(null)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [previewTheme, setPreviewTheme] = useState<ThemeId | null>(null)
  const [dragEntry, setDragEntry] = useState<(DragIntent & { entry: LibraryEntry }) | null>(null)
  const [runtime, setRuntime] = useState<ThemeRuntime | null>(null)
  // 预览状态只存在于这个组件，不进 reducer，不写草稿
  const [previewState, setPreviewState] = useState<PreviewState>('real')
  const [historyOpen, setHistoryOpen] = useState(false)
  const [discardOpen, setDiscardOpen] = useState(false)
  const [pendingGrid, setPendingGrid] = useState<Grid | null>(null)
  const [saving, setSavingState] = useState(false)
  const setSaving = (v: boolean) => {
    savingRef.current = v
    setSavingState(v)
  }
  const [conflict, setConflict] = useState<ConflictInfo | null>(null)
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
  }, [reloadKey, dispatch])

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
  const changes = useMemo(() => changeList(state), [state])
  const dirty = changes.length > 0

  const resolved = useMemo(
    () => (screen ? resolveScreen(screen, { plugins: pluginList, instances }) : null),
    [screen, pluginList, instances],
  )
  const instanceIds = useMemo(() => referencedInstanceIds(resolved?.widgets ?? []), [resolved])
  const data = useCanvasData(instanceIds, instances)
  const canvasView = useMemo(() => (resolved ? applyPreviewState(resolved, data, previewState) : null), [resolved, data, previewState])

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
  }, [rejection, t, toast, dispatch])

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

  // 编辑器级快捷键：不要求焦点在画布上，输入控件里的按键不处理
  const selectedId = selected?.id ?? null
  const modalOpen = historyOpen || discardOpen || pendingGrid !== null || conflict !== null
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.altKey || editableTarget(e.target) || savingRef.current) return
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.key.toLowerCase() === 'z') {
        // 模态打开时不撤销背后的草稿
        if (modalOpen) return
        e.preventDefault()
        dispatch({ type: 'undoLast' })
        return
      }
      if (e.metaKey || e.ctrlKey) return
      if (e.key === 'Escape') {
        dispatch({ type: 'select', id: null })
        return
      }
      const sel = selectedId ? rects.find((r) => r.id === selectedId) : undefined
      if (!sel || interactiveTarget(e.target)) return
      if (e.key === 'Delete' || e.key === 'Backspace') {
        e.preventDefault()
        dispatch({ type: 'remove', id: sel.id })
        return
      }
      if (!e.key.startsWith('Arrow') || ownsArrows(e.target)) return
      e.preventDefault()
      const next = moveByKey(e.key, grid, rects, sel)
      if (next) return dispatch({ type: 'move', id: sel.id, ...next })
      // 走不动时把想去的格交给状态层，给出越界或冲突的反馈
      const d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key]
      if (d) dispatch({ type: 'move', id: sel.id, col: sel.col + d[0], row: sel.row + d[1] })
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [selectedId, rects, grid, modalOpen, dispatch])

  const onGridChange = (value: string) => {
    const [cols, rows] = value.split('x').map(Number)
    if (savingRef.current) return
    const next = { cols, rows }
    // 有小组件放不进新网格：交给越界对话框处理，不再直接拒绝
    if (gridShrinkConflicts(state.draft, next).length) setPendingGrid(next)
    else dispatch({ type: 'setGrid', grid: next })
  }

  const oobItems = (g: Grid): OutOfBoundsItem[] =>
    gridShrinkConflicts(state.draft, g).flatMap((c) => {
      const s = state.draft.screens.find((x) => x.id === c.screenId)
      const w = s?.widgets.find((x) => x.id === c.widgetId)
      return s && w ? [{ id: w.id, screen: `${s.id} ${s.name}`, label: widgetLabel(w), place: `c${w.col + 1} r${w.row + 1} · ${w.size.cols}×${w.size.rows}` }] : []
    })

  const addScreen = () => {
    const next = nextScreenId(state.draft)
    if (!next) return
    dispatch({ type: 'addScreen', id: next.id, name: t('layoutEd.screenTab.defaultName', { n: next.n }) })
    setEditingScreen(next.id)
  }

  const loadSaved = (st: LayoutState) => dispatch({ type: 'load', version: st.version, layout: st.layout })

  const save = async (baseVersion = state.baseVersion) => {
    setSaving(true)
    try {
      const st = await http.put<LayoutState>('/api/screens', { base_version: baseVersion, layout: state.draft })
      loadSaved(st)
      setConflict(null)
      toast.show(t('layoutEd.saved', { version: st.version }))
    } catch (e) {
      if (isApiError(e) && e.code === 'layout.conflict') {
        const raw = Number(e.details?.latest_version)
        setConflict((c) => {
          const latestVersion = Number.isFinite(raw) ? raw : (c?.latestVersion ?? baseVersion)
          return { latestVersion, theirs: null, busy: null, error: c ? t('layoutEd.conflict.again', { latest: latestVersion }) : null }
        })
      } else if (isApiError(e) && e.code === 'layout.invalid') {
        const problems = Array.isArray(e.details?.problems) ? e.details.problems.length : 0
        toast.show(t('layoutEd.saveInvalid', { n: problems }), 'warn')
        setConflict(null)
      } else {
        toast.show(translateErrorValue(i18n, e), 'warn')
        setConflict(null)
      }
    } finally {
      setSaving(false)
    }
  }

  // 去看对方改了什么：取最新版本，与我的基线比较
  const viewTheirs = async () => {
    setConflict((c) => c && { ...c, busy: 'view', error: null })
    try {
      const latest = await http.get<LayoutState>('/api/screens')
      setConflict((c) => c && { ...c, busy: null, latestVersion: latest.version, theirs: diffLayouts(state.base, latest.layout) })
    } catch (e) {
      setConflict((c) => c && { ...c, busy: null, error: translateErrorValue(i18n, e) })
    }
  }

  const overwrite = async () => {
    if (!conflict) return
    setConflict({ ...conflict, busy: 'overwrite', error: null })
    await save(conflict.latestVersion)
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
  if (!server || !screen || !resolved || !canvasView) {
    return <div role="status" className="p-6 text-[13px] text-muted-foreground">{t('layoutEd.loading')}</div>
  }

  const gridOptions = gridPresets.some((g) => g.cols === grid.cols && g.rows === grid.rows) ? gridPresets : [...gridPresets, grid]
  const shrinkHint = (g: Grid) => gridShrinkConflicts(state.draft, g).length

  const allowedSizes: WidgetSize[] = selected ? allowedSizesOf(selected, pluginList, catalog) : []
  const tpl = selected ? templateOf(selected, pluginList) || selected.template || '' : ''
  const status = server.status
  const occupied = occupiedCells(grid, rects)
  const stageInfo = `${runtime?.themeId ?? themeId} · ${viewport.w}×${viewport.h} · ${grid.cols}×${grid.rows} · ${t('layoutEd.stage.occupied', { used: occupied, total: grid.cols * grid.rows })}`

  return (
    <div data-testid="layout-editor" className="flex min-h-0 flex-1 flex-col">
      <UnsavedGuard dirty={dirty} textKey="layoutEd.leave" />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-card px-4 py-2">
        <div className="flex items-baseline gap-2.5">
          <NumberTag no="02" className="border-foreground text-foreground" />
          <h1 className="text-[15px] font-medium">{t('pages.layoutEditor')}</h1>
        </div>
        <ScreenTabs
          screens={state.draft.screens}
          activeId={screen.id}
          canAdd={state.draft.screens.length < MAX_SCREENS && !saving}
          editingId={editingScreen}
          onEditingChange={setEditingScreen}
          onSelect={(id) => dispatch({ type: 'selectScreen', id })}
          onAdd={addScreen}
          onRename={(id, name) => dispatch({ type: 'renameScreen', id, name })}
        />
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
          <span
            data-testid="dirty-chip"
            data-dirty={dirty ? 'true' : 'false'}
            className={cn('inline-flex items-center gap-1.5', dirty ? 'text-signal-text' : 'text-muted-foreground')}
          >
            <i className={cn('size-1.5 rounded-full', dirty ? 'bg-signal' : 'bg-status-ok')} />
            {dirty ? t('layoutEd.chg.dirty', { n: changes.length }) : t('layoutEd.chg.clean', { version: state.baseVersion })}
          </span>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Button variant="ghost" size="sm" className="rounded-[2px]" disabled={!dirty || saving} title={t('layoutEd.undo.title')} onClick={() => dispatch({ type: 'undoLast' })}>
            <Undo2 size={15} />
            {t('layoutEd.undo.label')}
          </Button>
          <Button variant="ghost" size="sm" className="rounded-[2px]" onClick={() => setHistoryOpen(true)}>
            <History size={15} />
            {t('layoutEd.bar.history')}
          </Button>
          <Button variant="outline" size="sm" className="rounded-[2px]" disabled={!dirty || saving} onClick={() => setDiscardOpen(true)}>
            {t('layoutEd.bar.discard')}
          </Button>
          <Button size="sm" className="rounded-[2px]" disabled={!dirty || saving} onClick={() => void save()}>
            <Upload size={15} />
            {saving ? t('layoutEd.bar.saving') : t('layoutEd.bar.save')}
          </Button>
        </div>
      </div>

      <div inert={saving} aria-busy={saving} className="grid min-h-0 flex-1 grid-cols-[220px_minmax(0,1fr)_260px] min-[1280px]:grid-cols-[260px_minmax(0,1fr)_300px]">
        <WidgetLibrary
          entries={library}
          onAdd={(e) => addEntry(e)}
          onDragStart={(entry) => setDragEntry({ entry, size: entry.sizes[0] })}
          onDragEnd={() => setDragEntry(null)}
        />
        <div className="flex min-h-0 min-w-0 flex-col">
          <div className="flex min-w-0 flex-col gap-1.5 border-b border-border bg-card px-3.5 py-1.5 text-[12px]">
            <div className="flex min-w-0 items-center gap-3">
              <span className="max-w-[40%] shrink-0 truncate">
                <b className="font-medium">{screen.id}</b> {screen.name}
              </span>
              <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" data-testid="stage-info" title={stageInfo}>
                {stageInfo}
              </span>
            </div>
            <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5">
              <div className="flex items-center gap-1.5">
                <span className="text-muted-foreground">{t('layoutEd.stage.previewState')}</span>
                <Segmented
                  ariaLabel={t('layoutEd.stage.previewState')}
                  options={previewStates.map((p) => ({ value: p, label: t(`layoutEd.stage.state.${p}`), title: t(`layoutEd.stage.stateTitle.${p}`) }))}
                  value={previewState}
                  onChange={setPreviewState}
                />
              </div>
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
          </div>
          <EditorCanvas
            screen={canvasView.screen}
            grid={grid}
            data={canvasView.data}
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
            onThemeRuntime={setRuntime}
          />
          <section aria-label={t('layoutEd.chg.title')} data-testid="changes-panel" className="max-h-[172px] shrink-0 overflow-y-auto border-t border-border bg-card">
            <div className="sticky top-0 flex items-baseline gap-2.5 border-b border-border bg-card px-3.5 py-1.5">
              <NumberTag no="02.2" />
              <h2 className="text-[13px] font-medium">
                {t('layoutEd.chg.title')}
                <span className="ml-1.5 font-mono" data-testid="changes-count">{changes.length}</span>
              </h2>
              <span className="ml-auto truncate text-[12px] text-muted-foreground">
                {dirty ? t('layoutEd.chg.meta', { base: state.baseVersion, next: state.baseVersion + 1 }) : t('layoutEd.chg.clean', { version: state.baseVersion })}
              </span>
            </div>
            <ChangeList changes={changes} onUndo={(key) => dispatch({ type: 'undoChange', key })} empty={t('layoutEd.chg.none')} testId="changes-list" />
          </section>
          <div className="flex flex-wrap gap-x-4 gap-y-1 border-t border-border bg-card px-3.5 py-1.5 text-[11.5px] text-muted-foreground">
            <span><kbd className="font-mono">{t('layoutEd.keys.dragKey')}</kbd> {t('layoutEd.keys.drag')}</span>
            <span><kbd className="font-mono">← ↑ → ↓</kbd> {t('layoutEd.keys.move')}</span>
            <span><kbd className="font-mono">del</kbd> {t('layoutEd.keys.delete')}</span>
            <span><kbd className="font-mono">⌘Z</kbd> {t('layoutEd.keys.undo')}</span>
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
              key={selected?.id ?? 'screen'}
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
      <OutOfBoundsDialog
        open={pendingGrid !== null}
        grid={pendingGrid ?? grid}
        items={pendingGrid ? oobItems(pendingGrid) : []}
        onCancel={() => setPendingGrid(null)}
        onConfirm={() => {
          if (pendingGrid) dispatch({ type: 'setGrid', grid: pendingGrid, removeOutOfBounds: true })
          setPendingGrid(null)
        }}
      />
      <ConflictDialog
        open={conflict !== null}
        baseVersion={state.baseVersion}
        latestVersion={conflict?.latestVersion ?? state.baseVersion}
        theirs={conflict?.theirs ?? null}
        busy={conflict?.busy ?? null}
        error={conflict?.error ?? null}
        onView={() => void viewTheirs()}
        onOverwrite={() => void overwrite()}
        onClose={() => setConflict(null)}
      />
      <HistorySheet
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        currentVersion={state.baseVersion}
        current={state.base}
        dirtyCount={changes.length}
        onRolledBack={loadSaved}
        onNotify={toast.show}
      />
      <Dialog open={discardOpen} onOpenChange={setDiscardOpen}>
        {discardOpen && (
          <DialogContent
            tag="!"
            tagTone="crit"
            title={t('layoutEd.discardDlg.title')}
            footer={
              <>
                <Button variant="outline" className="rounded-[2px]" onClick={() => setDiscardOpen(false)}>
                  {t('layoutEd.discardDlg.cancel')}
                </Button>
                <Button
                  variant="destructive"
                  className="rounded-[2px]"
                  onClick={() => {
                    dispatch({ type: 'discard' })
                    setDiscardOpen(false)
                  }}
                >
                  {t('layoutEd.discardDlg.confirm')}
                </Button>
              </>
            }
          >
            <p>{t('layoutEd.discardDlg.body', { version: state.baseVersion, n: changes.length })}</p>
          </DialogContent>
        )}
      </Dialog>
    </div>
  )
}
