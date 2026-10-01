import { Layers, Plus, Save } from 'lucide-react'
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { UnsavedGuard } from '@/app/unsaved-guard'
import { DEFAULT_VIEWPORT } from '@/editor/geometry'
import { MAX_GRID_COLS, MAX_GRID_ROWS, MAX_SCREENS, nextScreenId } from '@/editor/screen-rules'
import { ScreenThumb, useThumbScreens } from '@/editor/ScreenThumb'
import { gridShrinkConflicts, initialEditorState } from '@/editor/state'
import { widgetLabel } from '@/editor/changes'
import { translateErrorValue } from '@/i18n/errors'
import { formatDateTime, parseTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import { usePlugins } from '@/pages/instances/use-plugins'
import { computeGrid } from '@/screen/grid'
import { selectInstances, selectSettings, serverNow, useLiveStore } from '@/store/live-store'
import { DEFAULT_THEME_ID, isThemeId, type ThemeId } from '@/themes'
import type { Grid, LayoutState, LayoutVersionInfo, ScreenDisplaySettings, ScreenStatus, Settings } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'
import { OutOfBoundsDialog, type OutOfBoundsItem } from '@/ui/out-of-bounds-dialog'
import { PageHeader } from '@/ui/page-header'
import { FieldHelp, FormRow, Section } from '@/ui/section'
import { Segmented } from '@/ui/segmented'
import { Stepper } from '@/ui/stepper'
import { useToast } from '@/ui/toast'
import { screenChanges, screenReducer, type ScreenChange } from './draft'
import { ScreenTable } from './ScreenTable'
import { effectiveTouch } from './touch'

const gridPresets: Grid[] = [
  { cols: 6, rows: 4 },
  { cols: 8, rows: 5 },
  { cols: 10, rows: 6 },
]
const presetTitle: Record<string, string> = { '6x4': '800×480', '8x5': '1024×600', '10x6': '1280×720' }
const scales = [1, 1.25, 1.5, 2]
const THUMB_WIDTH = 170

interface ServerData {
  status: ScreenStatus | null
  versions: LayoutVersionInfo[]
}

function sameDisplay(a: ScreenDisplaySettings, b: ScreenDisplaySettings): boolean {
  return a.carousel_mode === b.carousel_mode && a.idle_home_seconds === b.idle_home_seconds && a.default_dwell_seconds === b.default_dwell_seconds && a.input_mode === b.input_mode && a.ui_scale === b.ui_scale
}

function changeText(t: (k: string, o?: Record<string, unknown>) => string, c: ScreenChange): string {
  const id = c.screenId
  switch (c.kind) {
    case 'grid':
      return t('screens.chg.grid', { from: c.from, to: c.to })
    case 'add':
      return t('screens.chg.add', { id, name: c.to })
    case 'rename':
      return t('screens.chg.rename', { id, from: c.from, to: c.to })
    case 'dwell':
      return t('screens.chg.dwell', { id, from: c.from || t('screens.chg.default'), to: c.to || t('screens.chg.default') })
    case 'rotation':
      return t(c.to === 'on' ? 'screens.chg.rotationOn' : 'screens.chg.rotationOff', { id })
    case 'remove':
      return t('screens.chg.remove', { id, name: c.from })
    case 'order':
      return t('screens.chg.order')
  }
}

export function ScreenManager() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const plugins = usePlugins()
  const instances = useLiveStore(selectInstances)
  const liveSettings = useLiveStore(selectSettings)
  const lang = i18n.language === 'en' ? 'en' : 'zh'

  const [state, dispatch] = useReducer(screenReducer, undefined, initialEditorState)
  const [server, setServer] = useState<ServerData | null>(null)
  const [settingsBase, setSettingsBase] = useState<Settings | null>(null)
  const [display, setDisplay] = useState<ScreenDisplaySettings | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [saving, setSaving] = useState(false)
  const savingRef = useRef(false)
  const [conflictLatest, setConflictLatest] = useState<number | null>(null)
  // 409 后重新加载只重置布局草稿，设置草稿保留（设置本身没有冲突）
  const keepDisplayRef = useRef(false)
  const [pendingGrid, setPendingGrid] = useState<Grid | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    Promise.all([
      http.get<LayoutState>('/api/screens', { signal: ctrl.signal }),
      http.get<Settings>('/api/settings', { signal: ctrl.signal }),
      // 显示器状态与版本记录只用于展示，取不到不影响编辑
      http.get<ScreenStatus>('/api/screen/status', { signal: ctrl.signal }).catch(() => null),
      http.get<LayoutVersionInfo[]>('/api/screens/versions', { signal: ctrl.signal }).catch(() => [] as LayoutVersionInfo[]),
    ])
      .then(([layout, settings, status, versions]) => {
        setLoadError(null)
        setServer({ status, versions })
        setSettingsBase(settings)
        if (!keepDisplayRef.current) setDisplay(settings.screen)
        keepDisplayRef.current = false
        dispatch({ type: 'load', version: layout.version, layout: layout.layout })
        setConflictLatest(null)
        setLoaded(true)
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) setLoadError(e)
      })
    return () => ctrl.abort()
  }, [reloadKey])

  const draft = state.draft
  const changes = useMemo(() => screenChanges(state.base, draft), [state.base, draft])
  const displayDirty = !!display && !!settingsBase && !sameDisplay(display, settingsBase.screen)
  const dirty = changes.length > 0 || displayDirty
  const dirtyCount = changes.length + (displayDirty ? 1 : 0)

  const status = server?.status ?? null
  const themeId: ThemeId = isThemeId(status?.state.theme_id) ? status!.state.theme_id as ThemeId : DEFAULT_THEME_ID
  const viewport = status?.viewport ? { w: status.viewport.w, h: status.viewport.h } : DEFAULT_VIEWPORT
  const now = useCallback(() => serverNow(), [])
  const { resolved, data } = useThumbScreens(draft.screens, plugins.list?.plugins ?? [], instances)
  const reduceEffects = liveSettings?.reduce_effects ?? false
  const timezone = liveSettings?.timezone ?? 'UTC'

  const thumbs = useMemo(() => {
    const out: Record<string, ReactNode> = {}
    for (const sc of resolved) {
      out[sc.id] = (
        <ScreenThumb
          screen={sc}
          grid={draft.grid}
          data={data}
          themeId={themeId}
          reduceEffects={reduceEffects}
          viewport={viewport}
          width={THUMB_WIDTH}
          lang={lang}
          timezone={timezone}
          now={now}
          label={t('screens.list.thumbLabel', { id: sc.id })}
        />
      )
    }
    return out
    // viewport 每次渲染是新对象：按宽高比较
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resolved, draft.grid, data, themeId, reduceEffects, viewport.w, viewport.h, lang, timezone, now, t])

  const modified = useMemo(() => {
    const out: Record<string, string> = {}
    for (const v of server?.versions ?? []) {
      for (const id of v.summary.changed_screens) {
        if (out[id]) continue
        const ms = parseTime(v.created_at)
        out[id] = `v${v.version}${ms === null ? '' : ` · ${formatDateTime(ms, i18n.language)}`}`
      }
    }
    return out
  }, [server?.versions, i18n.language])
  const unsavedIds = useMemo(() => new Set(changes.filter((c) => c.kind === 'add').map((c) => c.screenId!)), [changes])

  const lock = <A extends Parameters<typeof dispatch>[0]>(a: A) => {
    if (!savingRef.current) dispatch(a)
  }

  const requestGrid = (g: Grid) => {
    if (savingRef.current || (g.cols === draft.grid.cols && g.rows === draft.grid.rows)) return
    // 有小组件放不进新网格：交给共享的越界对话框确认，确认后连同它们一起移除
    if (gridShrinkConflicts(draft, g).length) setPendingGrid(g)
    else dispatch({ type: 'setGrid', grid: g })
  }
  const oobItems = (g: Grid): OutOfBoundsItem[] =>
    gridShrinkConflicts(draft, g).flatMap((c) => {
      const s = draft.screens.find((x) => x.id === c.screenId)
      const w = s?.widgets.find((x) => x.id === c.widgetId)
      return s && w ? [{ id: w.id, screen: `${s.id} ${s.name}`, label: widgetLabel(w), place: `c${w.col + 1} r${w.row + 1} · ${w.size.cols}×${w.size.rows}` }] : []
    })

  const addScreen = () => {
    const next = nextScreenId(draft)
    if (next) lock({ type: 'addScreen', id: next.id, name: t('layoutEd.screenTab.defaultName', { n: next.n }) })
  }

  const save = async () => {
    savingRef.current = true
    setSaving(true)
    let layoutVersion: number | null = null
    try {
      if (changes.length > 0) {
        try {
          const st = await http.put<LayoutState>('/api/screens', { base_version: state.baseVersion, layout: draft })
          layoutVersion = st.version
          dispatch({ type: 'load', version: st.version, layout: st.layout })
          http.get<LayoutVersionInfo[]>('/api/screens/versions').then((versions) => setServer((s) => s && { ...s, versions }), () => {})
          setConflictLatest(null)
        } catch (e) {
          if (isApiError(e) && e.code === 'layout.conflict') {
            const raw = Number(e.details?.latest_version)
            setConflictLatest(Number.isFinite(raw) ? raw : state.baseVersion)
          } else if (isApiError(e) && e.code === 'layout.invalid') {
            const n = Array.isArray(e.details?.problems) ? e.details.problems.length : 0
            toast.show(t('screens.saveInvalid', { n }), 'warn')
          } else {
            toast.show(translateErrorValue(i18n, e), 'warn')
          }
          return
        }
      }
      if (displayDirty && display) {
        try {
          // 整份 PUT：先取最新设置，只改屏幕显示参数，避免覆盖设置页的并发修改
          const cur = await http.get<Settings>('/api/settings')
          const saved = await http.put<Settings>('/api/settings', { ...cur, screen: display })
          setSettingsBase(saved)
          setDisplay(saved.screen)
        } catch (e) {
          const detail = translateErrorValue(i18n, e)
          toast.show(layoutVersion === null ? detail : t('screens.settingsFailedAfterLayout', { version: layoutVersion, error: detail }), 'warn')
          return
        }
      }
      toast.show(t('screens.saved'))
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }

  const discard = () => {
    dispatch({ type: 'discard' })
    if (settingsBase) setDisplay(settingsBase.screen)
  }

  if (loadError) {
    return (
      <>
        <PageHeader no="02a" title={t('pages.screens')} sub={t('screens.sub')} />
        <div className="p-6">
          <Note tone="crit" role="alert">
            {t('screens.loadFailed', { error: translateErrorValue(i18n, loadError) })}
            <div className="mt-2">
              <Button variant="outline" size="sm" onClick={() => setReloadKey((k) => k + 1)}>
                {t('screens.retry')}
              </Button>
            </div>
          </Note>
        </div>
      </>
    )
  }
  if (!loaded || !display) {
    return (
      <>
        <PageHeader no="02a" title={t('pages.screens')} sub={t('screens.sub')} />
        <div role="status" className="p-6 text-[13px] text-muted-foreground">
          {t('screens.loading')}
        </div>
      </>
    )
  }

  const grid = draft.grid
  const touch = effectiveTouch(display.input_mode, status?.coarse_pointer)
  const metrics = computeGrid(viewport.w, viewport.h, grid)
  const rec = status?.recommended_grid
  const gridKey = `${grid.cols}x${grid.rows}`
  const upd = (patch: Partial<ScreenDisplaySettings>) => !savingRef.current && setDisplay({ ...display, ...patch })
  const deleteTarget = deleting ? draft.screens.find((s) => s.id === deleting) : undefined

  return (
    <>
      <UnsavedGuard dirty={dirty} textKey="screens.leave" />
      <PageHeader
        no="02a"
        title={t('pages.screens')}
        sub={t('screens.sub')}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="rounded-[2px]"
              disabled={draft.screens.length >= MAX_SCREENS || saving}
              title={draft.screens.length >= MAX_SCREENS ? t('layoutEd.screenTab.full', { max: MAX_SCREENS }) : undefined}
              aria-label={t('screens.add')}
              onClick={addScreen}
            >
              <Plus size={15} />
              <span className="mobile:hidden">{t('screens.add')}</span>
            </Button>
            <Button asChild size="sm" className="rounded-[2px]">
              <Link to="/screens/editor" aria-label={t('screens.editLayout')}>
                <Layers size={15} />
                <span className="mobile:hidden">{t('screens.editLayout')}</span>
              </Link>
            </Button>
          </div>
        }
      />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        {dirty && (
          <div data-testid="screens-dirty-bar" className="flex flex-col gap-2 border border-signal bg-card px-4 py-2.5">
            <div className="flex flex-wrap items-center gap-3">
              <span className="inline-flex items-center gap-1.5 text-[13px] text-signal-text">
                <i className="size-1.5 rounded-full bg-signal" />
                {t('screens.dirty', { n: dirtyCount })}
              </span>
              <span className="text-[12px] text-muted-foreground">{t('screens.dirtyHint', { base: state.baseVersion, next: state.baseVersion + 1 })}</span>
              <div className="ml-auto flex gap-2">
                <Button variant="outline" size="sm" className="rounded-[2px]" disabled={saving} onClick={discard}>
                  {t('screens.discard')}
                </Button>
                <Button size="sm" className="rounded-[2px]" disabled={saving} onClick={() => void save()}>
                  <Save size={15} />
                  {saving ? t('screens.saving') : t('screens.save')}
                </Button>
              </div>
            </div>
            <ul data-testid="screens-changes" className="text-[12.5px] text-muted-foreground">
              {changes.map((c) => (
                <li key={c.key}>{changeText(t, c)}</li>
              ))}
              {displayDirty && <li>{t('screens.chg.display')}</li>}
            </ul>
          </div>
        )}
        {conflictLatest !== null && (
          <Note tone="crit" role="alert">
            {t('screens.conflict', { base: state.baseVersion, latest: conflictLatest })}
            <div className="mt-2">
              <Button
                variant="outline"
                size="sm"
                className="rounded-[2px]"
                onClick={() => {
                  keepDisplayRef.current = true
                  setReloadKey((k) => k + 1)
                }}
              >
                {t('screens.conflictReload')}
              </Button>
            </div>
          </Note>
        )}

        <div className="grid grid-cols-2 gap-4 mobile:grid-cols-1">
          <Section no="02.1" title={t('screens.rotation.title')} meta="rotation">
            <div className="grid grid-cols-2 gap-2 p-4 pb-3 mobile:grid-cols-1" role="radiogroup" aria-label={t('screens.rotation.title')}>
              {(['home_only', 'auto'] as const).map((mode) => {
                const on = display.carousel_mode === mode
                return (
                  <button
                    key={mode}
                    type="button"
                    role="radio"
                    aria-checked={on}
                    onClick={() => upd({ carousel_mode: mode })}
                    className={cn('flex items-start gap-2.5 rounded-[2px] border px-3 py-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring', on ? 'border-foreground bg-panel-2' : 'border-line-strong hover:bg-panel-2')}
                  >
                    <span aria-hidden className={cn('mt-1 size-2.5 shrink-0 rounded-full border border-foreground', on && 'bg-foreground')} />
                    <span className="text-[13px]">
                      <b className="font-medium">{t(mode === 'auto' ? 'screens.rotation.auto' : 'screens.rotation.homeOnly')}</b>
                      <span className="mt-0.5 block text-[12px] text-muted-foreground">{t(mode === 'auto' ? 'screens.rotation.autoDesc' : 'screens.rotation.homeOnlyDesc')}</span>
                    </span>
                  </button>
                )
              })}
            </div>
            {display.carousel_mode === 'home_only' && touch === false && (
              <div className="px-4 pb-3">
                <Note tone="warn" role="status">
                  {t('screens.rotation.noTouchWarn')}
                </Note>
              </div>
            )}
            <FormRow label={t('screens.rotation.dwell')} fieldKey="default_dwell_seconds">
              <Stepper
                value={display.default_dwell_seconds}
                min={3}
                max={3600}
                step={5}
                unit={t('screens.rotation.secondsUnit')}
                ariaLabel={t('screens.rotation.dwell')}
                decrementLabel={t('screens.rotation.dec')}
                incrementLabel={t('screens.rotation.inc')}
                onChange={(v) => upd({ default_dwell_seconds: v })}
              />
              <FieldHelp>{t('screens.rotation.dwellHelp')}</FieldHelp>
            </FormRow>
            <FormRow label={t('screens.rotation.idle')} fieldKey="idle_home_seconds">
              <Stepper
                value={display.idle_home_seconds}
                min={10}
                max={3600}
                step={10}
                unit={t('screens.rotation.secondsUnit')}
                ariaLabel={t('screens.rotation.idle')}
                decrementLabel={t('screens.rotation.dec')}
                incrementLabel={t('screens.rotation.inc')}
                onChange={(v) => upd({ idle_home_seconds: v })}
              />
              <FieldHelp>{t('screens.rotation.idleHelp')}</FieldHelp>
            </FormRow>
          </Section>

          <Section
            no="02.2"
            title={t('screens.grid.title')}
            meta={status?.viewport ? `${t('screens.grid.meta', { w: viewport.w, h: viewport.h })}${rec ? ` · ${t('screens.grid.recommend', { cols: rec.cols, rows: rec.rows })}` : ''}` : t('screens.grid.metaNone')}
          >
            <FormRow label={t('screens.grid.label')} fieldKey="grid">
              <div className="flex flex-wrap items-center gap-2">
                <Stepper
                  value={grid.cols}
                  min={1}
                  max={MAX_GRID_COLS}
                  className="w-[130px]"
                  unit={t('screens.grid.cols')}
                  ariaLabel={t('screens.grid.colsLabel')}
                  decrementLabel={t('screens.grid.lessCol')}
                  incrementLabel={t('screens.grid.moreCol')}
                  onChange={(cols) => requestGrid({ cols, rows: grid.rows })}
                />
                <span className="text-muted-foreground">×</span>
                <Stepper
                  value={grid.rows}
                  min={1}
                  max={MAX_GRID_ROWS}
                  className="w-[130px]"
                  unit={t('screens.grid.rows')}
                  ariaLabel={t('screens.grid.rowsLabel')}
                  decrementLabel={t('screens.grid.lessRow')}
                  incrementLabel={t('screens.grid.moreRow')}
                  onChange={(rows) => requestGrid({ cols: grid.cols, rows })}
                />
              </div>
              <div className="mt-2.5 flex flex-wrap items-center gap-2.5">
                <span className="text-[12px] text-muted-foreground">{t('screens.grid.presets')}</span>
                <Segmented
                  ariaLabel={t('screens.grid.presets')}
                  className="font-mono"
                  options={gridPresets.map((g) => {
                    const k = `${g.cols}x${g.rows}`
                    return {
                      value: k,
                      label: (
                        <>
                          {g.cols}×{g.rows}
                          {rec && rec.cols === g.cols && rec.rows === g.rows && <span className="ml-1 text-[10px]">★</span>}
                        </>
                      ),
                      title: `${g.cols}×${g.rows} · ${presetTitle[k]}`,
                    }
                  })}
                  value={gridKey}
                  onChange={(k) => {
                    const [cols, rows] = k.split('x').map(Number)
                    requestGrid({ cols, rows })
                  }}
                />
              </div>
              <FieldHelp>{t('screens.grid.help')}</FieldHelp>
            </FormRow>
            <FormRow label={t('screens.grid.cell')} fieldKey="cell">
              <div className="font-mono text-[12px] leading-[1.7] text-muted-foreground" data-testid="cell-info">
                {t('screens.grid.cellInfo', { w: Math.round(metrics.cellW), h: Math.round(metrics.cellH) })}
                <br />
                {t('screens.grid.cells', { n: grid.cols * grid.rows })}
              </div>
            </FormRow>
          </Section>
        </div>

        <Section no="02.3" title={t('screens.list.title')} meta={t('screens.list.meta')}>
          <ScreenTable
            screens={draft.screens}
            defaultDwell={display.default_dwell_seconds}
            modified={modified}
            unsavedIds={unsavedIds}
            thumbs={thumbs}
            disabled={saving}
            onDwell={(id, seconds) => lock({ type: 'setDwell', id, seconds })}
            onRotation={(id, on) => lock({ type: 'setRotation', id, on })}
            onRename={(id, name) => lock({ type: 'renameScreen', id, name })}
            onDelete={setDeleting}
            onMove={(id, to) => lock({ type: 'moveScreen', id, to })}
          />
        </Section>

        <div className="grid grid-cols-2 gap-4 mobile:grid-cols-1">
          <Section no="02.4" title={t('screens.input.title')} meta="input">
            <FormRow label={t('screens.input.label')} fieldKey="input_mode">
              <Segmented
                ariaLabel={t('screens.input.label')}
                options={(['auto', 'touch', 'none'] as const).map((m) => ({ value: m, label: t(`screens.input.${m}`), title: t(`screens.input.${m}`) }))}
                value={display.input_mode as 'auto' | 'touch' | 'none'}
                onChange={(m) => upd({ input_mode: m })}
              />
              <FieldHelp>
                {status?.coarse_pointer === undefined
                  ? t('screens.input.detectedNone')
                  : t('screens.input.detected', { kind: t(status.coarse_pointer ? 'screens.input.hasTouch' : 'screens.input.noTouch') })}{' '}
                {t('screens.input.help')}
              </FieldHelp>
            </FormRow>
          </Section>
          <Section no="02.5" title={t('screens.scale.title')} meta="ui_scale">
            <FormRow label={t('screens.scale.label')} fieldKey="ui_scale">
              <Segmented
                ariaLabel={t('screens.scale.label')}
                className="font-mono"
                options={scales.map((s) => ({ value: String(s), label: s.toFixed(s === 1.25 ? 2 : 1), title: `${s}×` }))}
                value={String(display.ui_scale)}
                onChange={(v) => upd({ ui_scale: Number(v) })}
              />
              <FieldHelp>{t('screens.scale.help')}</FieldHelp>
              <FieldHelp className="text-signal-text">{t('screens.scale.restart')}</FieldHelp>
            </FormRow>
          </Section>
        </div>
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
      <Dialog open={deleteTarget !== undefined} onOpenChange={(o) => !o && setDeleting(null)}>
        {deleteTarget && (
          <DialogContent
            tag="!"
            tagTone="crit"
            title={t('screens.delete.title', { id: deleteTarget.id })}
            footer={
              <>
                <Button variant="outline" className="rounded-[2px]" onClick={() => setDeleting(null)}>
                  {t('screens.delete.cancel')}
                </Button>
                <Button
                  variant="destructive"
                  className="rounded-[2px]"
                  onClick={() => {
                    dispatch({ type: 'removeScreen', id: deleteTarget.id })
                    setDeleting(null)
                  }}
                >
                  {t('screens.delete.confirm')}
                </Button>
              </>
            }
          >
            <p>{t('screens.delete.body', { name: deleteTarget.name, n: deleteTarget.widgets.length })}</p>
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}
