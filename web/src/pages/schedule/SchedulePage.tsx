import { Check, Plus, Power, Save, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { UnsavedGuard } from '@/app/unsaved-guard'
import { translateErrorValue } from '@/i18n/errors'
import { formatInMinutes, parseTime, useNow } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useIsMobile } from '@/lib/use-mobile'
import { effectiveTouch } from '@/pages/screens/touch'
import { useScreenStatus } from '@/pages/screens/use-screen-status'
import { selectScreenState, selectSettings, useLiveStore } from '@/store/live-store'
import { getTheme, isThemeId, themes, type ThemeId } from '@/themes'
import type { Schedule, ScheduleProblem, Settings } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { PageHeader } from '@/ui/page-header'
import { Section } from '@/ui/section'
import { Segmented } from '@/ui/segmented'
import { Switch } from '@/ui/switch'
import { useToast } from '@/ui/toast'
import {
  currentAndNext,
  formatHM,
  fromServer,
  lengthOf,
  locate,
  MAX_PERIODS,
  minuteInZone,
  moveBoundary,
  OFF,
  removePeriod,
  sameSchedule,
  setEnd,
  setStart,
  setTheme,
  splitMid,
  THEME_ORDER,
  themeUsage,
  toServer,
  validate,
  type DraftPeriod,
} from './model'
import { OFF_STRIPES, ThemeMini, ThemeSwatch } from './ThemeMini'
import { TimeField } from './TimeField'
import { Timeline } from './Timeline'

type T = (key: string, opts?: Record<string, unknown>) => string

function problemText(t: T, p: ScheduleProblem): string {
  const base = t(`schedule.problems.${p.kind}`, { from: p.from, to: p.to, defaultValue: t('schedule.problems.format') })
  return p.period === undefined ? base : t('schedule.problems.period', { n: p.period + 1, msg: base })
}

function durationText(t: T, minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  if (h === 0) return t('schedule.list.minutes', { m })
  return m === 0 ? t('schedule.list.hours', { h }) : t('schedule.list.hoursMinutes', { h, m })
}

export function SchedulePage() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const mobile = useIsMobile()
  const liveSettings = useLiveStore(selectSettings)
  const liveState = useLiveStore(selectScreenState)
  const { status } = useScreenStatus()
  const nowMs = useNow(15_000)

  const [base, setBase] = useState<DraftPeriod[]>([])
  const [draft, setDraft] = useState<DraftPeriod[]>([])
  const [selKey, setSelKey] = useState<string | null>(null)
  const [settingsBase, setSettingsBase] = useState<Settings | null>(null)
  const [reduceDraft, setReduceDraft] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [saving, setSaving] = useState(false)
  const savingRef = useRef(false)
  const [backendProblems, setBackendProblems] = useState<ScheduleProblem[]>([])
  const [timeInvalid, setTimeInvalid] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    Promise.all([http.get<Schedule>('/api/schedule', { signal: ctrl.signal }), http.get<Settings>('/api/settings', { signal: ctrl.signal })])
      .then(([schedule, settings]) => {
        const ps = fromServer(schedule)
        setLoadError(null)
        setBase(ps)
        setDraft(ps)
        setSelKey((cur) => (cur && ps.some((p) => p.key === cur) ? cur : (ps[0]?.key ?? null)))
        setSettingsBase(settings)
        setReduceDraft(settings.reduce_effects)
        setLoaded(true)
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) setLoadError(e)
      })
    return () => ctrl.abort()
  }, [reloadKey])

  const tz = settingsBase?.timezone ?? liveSettings?.timezone ?? 'UTC'
  const nowMinute = minuteInZone(nowMs, tz)
  const localProblems = useMemo(() => validate(draft), [draft])
  const planDirty = !sameSchedule(draft, base)
  const reduceDirty = !!settingsBase && reduceDraft !== settingsBase.reduce_effects
  const dirty = planDirty || reduceDirty
  const dirtyCount = (planDirty ? 1 : 0) + (reduceDirty ? 1 : 0)
  const selected = draft.find((p) => p.key === selKey) ?? draft[0]
  const selIndex = selected ? draft.indexOf(selected) : -1
  const { current, next: localNext } = useMemo(
    () => (localProblems.length === 0 ? currentAndNext(draft, nowMinute, { nowMs, timeZone: tz }) : { current: null, next: null }),
    [draft, nowMinute, nowMs, tz, localProblems.length],
  )
  // 计划未改动且屏幕按计划运行时，倒计时直接用后端的 next_change（权威，含夏令时）；编辑预览才用本地推算
  const serverNext = parseTime(liveState?.reason === 'schedule' ? liveState.next_change : undefined)
  const next = localNext && !planDirty && serverNext !== null && serverNext > nowMs ? { ...localNext, inMinutes: Math.max(1, Math.round((serverNext - nowMs) / 60_000)) } : localNext
  const usage = useMemo(() => themeUsage(draft), [draft])

  const themeLabel = useCallback((id: string) => (id === OFF ? t('schedule.tl.off') : id), [t])
  const edit = useCallback((fn: (ps: readonly DraftPeriod[]) => DraftPeriod[]) => {
    if (savingRef.current) return
    setDraft((ps) => fn(ps))
    setBackendProblems([])
  }, [])

  const addPeriod = () => {
    if (!selected) return
    if (draft.length >= MAX_PERIODS) return void toast.show(t('schedule.list.full', { max: MAX_PERIODS }), 'warn')
    const r = splitMid(draft, selected.key)
    if (!r) return void toast.show(t('schedule.list.tooShort'), 'warn')
    edit(() => r.periods)
    setSelKey(r.newKey)
  }
  const deletePeriod = (p: DraftPeriod) => {
    const idx = draft.indexOf(p)
    const prev = draft[(idx - 1 + draft.length) % draft.length]
    edit((ps) => removePeriod(ps, p.key))
    if (selKey === p.key) setSelKey(prev.key)
    toast.show(t('schedule.list.deleted', { time: formatHM(p.end) }))
  }

  const save = async () => {
    if (savingRef.current) return
    savingRef.current = true
    setSaving(true)
    let planSaved = false
    try {
      if (planDirty) {
        try {
          const saved = await http.put<Schedule>('/api/schedule', toServer(draft))
          const ps = fromServer(saved)
          const at = selected ? ps[locate(ps, selected.start)] : undefined
          setBase(ps)
          setDraft(ps)
          setSelKey(at?.key ?? ps[0]?.key ?? null)
          setBackendProblems([])
          planSaved = true
        } catch (e) {
          if (isApiError(e) && e.code === 'schedule.invalid') {
            setBackendProblems(Array.isArray(e.details?.problems) ? (e.details.problems as ScheduleProblem[]) : [])
          } else {
            toast.show(translateErrorValue(i18n, e), 'warn')
          }
          return
        }
      }
      if (reduceDirty) {
        try {
          // 整份 PUT：先取最新设置，只改「降低特效」，避免覆盖设置页等处的并发修改
          const cur = await http.get<Settings>('/api/settings')
          const savedSettings = await http.put<Settings>('/api/settings', { ...cur, reduce_effects: reduceDraft })
          setSettingsBase(savedSettings)
          setReduceDraft(savedSettings.reduce_effects)
        } catch (e) {
          const detail = translateErrorValue(i18n, e)
          toast.show(planSaved ? t('schedule.reduce.settingsFailedAfterPlan', { error: detail }) : detail, 'warn')
          return
        }
      }
      toast.show(t('schedule.saved'))
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }
  const discard = () => {
    setDraft(base)
    setSelKey((cur) => (base.some((p) => p.key === cur) ? cur : (base[0]?.key ?? null)))
    setBackendProblems([])
    if (settingsBase) setReduceDraft(settingsBase.reduce_effects)
  }

  const header = (actions?: ReactNode) => <PageHeader no="02c" title={t('pages.schedule')} sub={t('schedule.sub')} actions={actions} />
  if (loadError) {
    return (
      <>
        {header()}
        <div className="p-6">
          <Note tone="crit" role="alert">
            {t('schedule.loadFailed', { error: translateErrorValue(i18n, loadError) })}
            <div className="mt-2">
              <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => setReloadKey((k) => k + 1)}>
                {t('schedule.retry')}
              </Button>
            </div>
          </Note>
        </div>
      </>
    )
  }
  if (!loaded) {
    return (
      <>
        {header()}
        <div role="status" className="p-6 text-[13px] text-muted-foreground">
          {t('schedule.loading')}
        </div>
      </>
    )
  }

  const touch = settingsBase ? effectiveTouch(settingsBase.screen.input_mode, status?.coarse_pointer, status?.kiosk?.touchscreen) : null
  const overridden = !!liveState && liveState.reason !== 'schedule'
  const selOff = selected?.theme === OFF
  const canSave = dirty && !saving && localProblems.length === 0 && !timeInvalid

  const tag = (text: string, tone: 'dim' | 'acc' = 'dim') => (
    <span className={cn('rounded-[2px] border px-1.5 text-[11px] leading-[16px]', tone === 'acc' ? 'border-signal text-signal-text' : 'border-border text-muted-foreground')}>{text}</span>
  )

  return (
    <>
      <UnsavedGuard dirty={dirty} textKey="schedule.leave" />
      {header(
        <div className="flex items-center gap-3">
          <div className="hidden items-center gap-3 font-mono text-[12px] whitespace-nowrap text-muted-foreground min-[1280px]:flex">
            <span>{t('schedule.readoutTz', { tz })}</span>
            <span>{t('schedule.readoutNow', { time: formatHM(nowMinute) })}</span>
          </div>
          <Button size="sm" className="rounded-[2px]" disabled={!canSave} onClick={() => void save()} aria-label={t('schedule.save')}>
            <Save size={15} />
            <span>{saving ? t('schedule.saving') : t('schedule.save')}</span>
          </Button>
        </div>,
      )}
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        {dirty && (
          <div data-testid="schedule-dirty-bar" className="flex flex-wrap items-center gap-3 border border-signal bg-card px-4 py-2">
            <span className="inline-flex items-center gap-1.5 text-[13px] text-signal-text">
              <i className="size-1.5 rounded-full bg-signal" />
              {t('schedule.dirty', { n: dirtyCount })}
            </span>
            <Button variant="outline" size="sm" className="ml-auto rounded-[2px]" disabled={saving} onClick={discard}>
              {t('schedule.discard')}
            </Button>
          </div>
        )}
        {overridden && (
          <Note tone="info" role="status">
            {t('schedule.override')}{' '}
            <Link to="/screens/remote" className="underline underline-offset-2">
              {t('schedule.overrideLink')}
            </Link>
          </Note>
        )}
        {localProblems.length > 0 && (
          <Note tone="crit" role="alert">
            <div data-testid="problems">
              <b className="font-medium">{t('schedule.problems.title')}</b>
              <ul className="mt-1 list-disc pl-5">
                {localProblems.map((p, i) => (
                  <li key={i}>{problemText(t, p)}</li>
                ))}
              </ul>
            </div>
          </Note>
        )}
        {backendProblems.length > 0 && (
          <Note tone="crit" role="alert">
            <div data-testid="problems-backend">
              <b className="font-medium">{t('schedule.problems.backend')}</b>
              <ul className="mt-1 list-disc pl-5">
                {backendProblems.map((p, i) => (
                  <li key={i}>{problemText(t, p)}</li>
                ))}
              </ul>
            </div>
          </Note>
        )}

        <Section no="02.1" title={t('schedule.tl.title')} meta={mobile ? undefined : t('schedule.tl.meta')}>
          <Timeline
            periods={draft}
            selectedKey={selected?.key ?? null}
            onSelect={setSelKey}
            onMoveBoundary={(key, delta) => edit((ps) => moveBoundary(ps, key, delta))}
            onDragBoundary={(origin, key, delta) => edit(() => moveBoundary(origin, key, delta))}
            disabled={saving}
            nowMinute={nowMinute}
            nowLabel={t('schedule.tl.now', { time: formatHM(nowMinute) })}
            mobile={mobile}
          />
          <div data-testid="timeline-foot" className="flex flex-wrap gap-x-5 gap-y-1 border-t border-border px-5 py-2.5 text-[12.5px] text-ink-2 mobile:px-3.5">
            {current ? (
              <>
                <span>
                  {t('schedule.tl.current', { theme: themeLabel(current.theme), from: formatHM(current.start), to: formatHM(current.end) })}
                </span>
                <span>
                  {next
                    ? t('schedule.tl.next', { at: formatHM(next.at), theme: themeLabel(next.theme), in: formatInMinutes(t, next.inMinutes) })
                    : t('schedule.tl.none')}
                </span>
              </>
            ) : null}
            <span>{t('schedule.tl.tz', { tz })}</span>
          </div>
        </Section>

        <Section no="02.2" title={t('schedule.list.title')} meta={t('schedule.list.meta')}>
          <div className={cn('grid grid-cols-[40px_minmax(0,1.2fr)_90px_minmax(0,1.5fr)_auto] gap-3 border-b border-border px-4 py-1.5 text-[11.5px] text-muted-foreground mobile:hidden')}>
            <span>{t('schedule.list.colNo')}</span>
            <span>{t('schedule.list.colRange')}</span>
            <span>{t('schedule.list.colLen')}</span>
            <span>{t('schedule.list.colTheme')}</span>
            <span />
          </div>
          <ul data-testid="period-list">
            {draft.map((p, i) => {
              const isNow = current?.key === p.key
              const sel = p.key === selected?.key
              return (
                <li
                  key={p.key}
                  data-testid={`period-row-${i}`}
                  data-range={`${formatHM(p.start)}-${formatHM(p.end)}`}
                  aria-current={sel ? 'true' : undefined}
                  onClick={() => setSelKey(p.key)}
                  className={cn(
                    'grid cursor-pointer grid-cols-[40px_minmax(0,1.2fr)_90px_minmax(0,1.5fr)_auto] items-center gap-3 border-b border-border px-4 py-2.5 text-[13px] last:border-b-0 mobile:grid-cols-[minmax(0,1fr)_auto] mobile:gap-y-1.5',
                    sel && 'bg-signal-soft shadow-[inset_3px_0_0_var(--signal)]',
                  )}
                >
                  <span className="font-mono text-[11.5px] text-muted-foreground mobile:hidden">{String(i + 1).padStart(2, '0')}</span>
                  <span className="flex flex-wrap items-center gap-2 font-mono text-[13px]">
                    {formatHM(p.start)} → {formatHM(p.end)}
                    {p.end <= p.start && draft.length > 1 && tag(t('schedule.list.overnight'))}
                  </span>
                  <span className="font-mono text-[12.5px] text-muted-foreground mobile:hidden">{durationText(t, lengthOf(p))}</span>
                  <span className="flex flex-wrap items-center gap-2 mobile:col-start-1 mobile:row-start-2">
                    {p.theme === OFF ? (
                      <span aria-hidden className="inline-flex h-3.5 w-10 rounded-[2px] border border-line-strong" style={OFF_STRIPES} />
                    ) : isThemeId(p.theme) ? (
                      <ThemeSwatch themeId={p.theme} />
                    ) : null}
                    <span className="font-mono">{themeLabel(p.theme)}</span>
                    {p.theme !== OFF && isThemeId(p.theme) && tag(t(`schedule.themes.${getTheme(p.theme).tone}`))}
                    {isNow && tag(t('schedule.list.current'), 'acc')}
                  </span>
                  <span className="flex items-center gap-1.5 mobile:col-start-2 mobile:row-span-2 mobile:row-start-1">
                    <Button variant="outline" size="sm" className="rounded-[2px]" onClick={(e) => { e.stopPropagation(); setSelKey(p.key) }}>
                      {t('schedule.list.edit')}
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t('schedule.list.delete', { n: i + 1 })}
                      disabled={draft.length <= 1 || saving}
                      onClick={(e) => { e.stopPropagation(); deletePeriod(p) }}
                    >
                      <Trash2 size={14} />
                    </Button>
                  </span>
                </li>
              )
            })}
          </ul>
          {selected && (
            <div data-testid="period-edit" className="flex flex-wrap items-center gap-x-4 gap-y-2.5 border-t border-border bg-panel-2 px-4 py-3 text-[13px] mobile:flex-col mobile:items-stretch">
              <span className="text-[12px] text-muted-foreground">{t('schedule.edit.title', { n: selIndex + 1 })}</span>
              <div className="flex items-center gap-4 mobile:grid mobile:grid-cols-2 mobile:gap-2">
                <label className="flex items-center gap-2">
                  {t('schedule.edit.start')}
                  <TimeField disabled={saving} value={selected.start} ariaLabel={t('schedule.edit.startAria')} onInvalid={setTimeInvalid} onCommit={(m) => edit((ps) => setStart(ps, selected.key, m))} />
                </label>
                <label className="flex items-center gap-2">
                  {t('schedule.edit.end')}
                  <TimeField disabled={saving} value={selected.end} ariaLabel={t('schedule.edit.endAria')} onInvalid={setTimeInvalid} onCommit={(m) => edit((ps) => setEnd(ps, selected.key, m))} />
                </label>
              </div>
              <Segmented
                disabled={saving}
                ariaLabel={t('schedule.edit.kind')}
                value={selOff ? 'off' : 'theme'}
                onChange={(v) => edit((ps) => setTheme(ps, selected.key, v === 'off' ? OFF : selOff ? THEME_ORDER[0] : selected.theme))}
                options={[
                  { value: 'theme', label: t('schedule.edit.kindTheme'), title: t('schedule.edit.kindTheme') },
                  { value: 'off', label: <><Power size={12} />{t('schedule.edit.kindOff')}</>, title: t('schedule.edit.kindOff') },
                ]}
              />
              <span className="ml-auto text-[12px] text-muted-foreground mobile:ml-0">{t(selOff ? 'schedule.edit.hintOff' : 'schedule.edit.hintTheme')}</span>
              {timeInvalid && <span role="alert" className="basis-full text-[12px] text-status-crit">{t('schedule.edit.invalidTime')}</span>}
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2.5 border-t border-border px-4 py-2.5">
            <Button variant="outline" size="sm" className="rounded-[2px]" disabled={saving} onClick={addPeriod}>
              <Plus size={13} />
              {t('schedule.list.add')}
            </Button>
            <span className="text-[12px] text-muted-foreground">{t('schedule.list.addHint')}</span>
            <span className="basis-full text-[11.5px] text-muted-foreground">{t('schedule.edit.linked')}</span>
          </div>
        </Section>

        <Section no="02.3" title={t('schedule.themes.title')} meta={selected ? (selOff ? t('schedule.themes.metaOff') : t('schedule.themes.metaPick', { from: formatHM(selected.start), to: formatHM(selected.end) })) : undefined}>
          <div className={cn('grid grid-cols-3 gap-3 p-4 mobile:grid-cols-1', selOff && 'opacity-50')}>
            {themes.map((th) => {
              const on = !selOff && selected?.theme === th.id
              const ranges = usage[th.id] ?? []
              return (
                <button
                  key={th.id}
                  type="button"
                  aria-pressed={on}
                  disabled={selOff || !selected || saving}
                  aria-label={t('schedule.themes.card', { name: th.name[i18n.language === 'en' ? 'en' : 'zh'], id: th.id })}
                  onClick={() => selected && edit((ps) => setTheme(ps, selected.key, th.id))}
                  className={cn(
                    'relative flex flex-col gap-2.5 rounded-[3px] border bg-card p-2.5 text-left outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring disabled:hover:bg-card',
                    on ? 'border-signal shadow-[inset_0_0_0_1px_var(--signal)]' : 'border-line-strong',
                  )}
                >
                  <ThemeMini themeId={th.id as ThemeId} reduceEffects={reduceDraft} />
                  {on && (
                    <span className="absolute top-4 right-4 inline-flex items-center gap-1 rounded-[2px] bg-signal px-[7px] py-0.5 text-[11.5px] font-medium text-primary-foreground">
                      <Check size={12} />
                      {t('schedule.themes.picked')}
                    </span>
                  )}
                  <span className="flex flex-wrap items-center gap-1.5">
                    <span className="mr-auto font-mono text-[14px] font-medium">{th.id}</span>
                    {tag(t(`schedule.themes.${th.tone}`))}
                    {th.id === 'ambient' && tag(t('schedule.themes.default'), 'acc')}
                  </span>
                  <span className="text-[12px] text-muted-foreground">{ranges.length ? t('schedule.themes.usedBy', { ranges: ranges.join('、') }) : t('schedule.themes.unused')}</span>
                </button>
              )
            })}
          </div>
        </Section>

        <div className="grid grid-cols-2 gap-4 mobile:grid-cols-1">
          <Section no="02.4" title={t('schedule.reduce.title')} meta="reduce_effects">
            <div className="flex items-start gap-3 p-4">
              <div className="min-w-0 flex-1 text-[13px]">
                <b className="font-medium">{t('schedule.reduce.title')}</b>
                <div className="mt-1 text-[12px] leading-[1.55] text-muted-foreground">{t('schedule.reduce.help')}</div>
              </div>
              <Switch checked={reduceDraft} onChange={(v) => !savingRef.current && setReduceDraft(v)} disabled={saving} ariaLabel={t('schedule.reduce.title')} className="mt-0.5" />
            </div>
          </Section>
          <Section no="02.5" title={t('schedule.wake.title')} meta={t('schedule.wake.meta')}>
            <ul data-testid="wake-rules" className="text-[13px]">
              {[
                { k: 'crit', name: t('schedule.wake.crit'), tagText: t('schedule.wake.critTag'), sub: t('schedule.wake.critSub') },
                { k: 'remote', name: t('schedule.wake.remote'), tagText: t('schedule.wake.remoteTag'), sub: t('schedule.wake.remoteSub') },
                {
                  k: 'touch',
                  name: t('schedule.wake.touch'),
                  tagText: touch === null ? t('schedule.wake.touchUnknown') : t(touch ? 'schedule.wake.touchYes' : 'schedule.wake.touchNo'),
                  sub: t('schedule.wake.touchSub'),
                },
              ].map((r) => (
                <li key={r.k} className="flex flex-wrap items-center gap-2.5 border-b border-border px-4 py-2.5 last:border-b-0">
                  <span>{r.name}</span>
                  {tag(r.tagText)}
                  <span className="ml-auto text-[12px] text-muted-foreground mobile:ml-0 mobile:basis-full">{r.sub}</span>
                </li>
              ))}
            </ul>
          </Section>
        </div>
      </div>
    </>
  )
}
