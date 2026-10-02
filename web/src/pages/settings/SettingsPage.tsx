import { Download, KeyRound, Monitor, Moon, Save, Sun } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { setThemeChoice, useThemeChoice, type ThemeChoice } from '@/admin-theme/theme'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { UnsavedGuard } from '@/app/unsaved-guard'
import { translateErrorValue } from '@/i18n/errors'
import { formatBytes } from '@/lib/format'
import { formatDateTime, parseTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import { timezoneOptions } from '@/pages/setup/timezones'
import type { BackupInfo, Settings } from '@/types/generated'
import { Button } from '@/ui/button'
import { Input } from '@/ui/input'
import { Note } from '@/ui/note'
import { NumberTag } from '@/ui/numbered-label'
import { PageHeader } from '@/ui/page-header'
import { FieldError, FieldHelp, FormRow, Section } from '@/ui/section'
import { Segmented } from '@/ui/segmented'
import { Select } from '@/ui/select'
import { Stepper } from '@/ui/stepper'
import { Switch } from '@/ui/switch'
import { useToast } from '@/ui/toast'
import { BackupDownloadDialog } from './BackupDownloadDialog'
import { dirtyKeys, normalizeForSave, trustedProxyErrors, type SettingsKey } from './model'
import { PasswordDialog } from './PasswordDialog'
import { TrustedProxies } from './TrustedProxies'

const anchors = [
  { id: 'sec-general', no: '05.1', key: 'general' },
  { id: 'sec-net', no: '05.2', key: 'net' },
  { id: 'sec-display', no: '05.3', key: 'display' },
  { id: 'sec-retention', no: '05.4', key: 'retention' },
  { id: 'sec-backup', no: '05.5', key: 'backup' },
] as const

// 数据保留的常用档位；当前值不在档位里时追加，避免下拉框显示成错的值
const rawHourPresets = [12, 24, 48, 72, 168]
const fiveMinDayPresets = [7, 30, 90, 180, 365]
const hourDayPresets = [90, 365, 730, 1095, 1825]

function sortedUnique(base: number[], current: number): number[] {
  return Array.from(new Set([...base, current])).sort((a, b) => a - b)
}

export function SettingsPage() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [base, setBase] = useState<Settings | null>(null)
  const [draft, setDraft] = useState<Settings | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [failure, setFailure] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [pwOpen, setPwOpen] = useState(false)
  const [backups, setBackups] = useState<BackupInfo[] | null>(null)
  const [downloading, setDownloading] = useState<BackupInfo | null>(null)
  const [backingUp, setBackingUp] = useState(false)

  const load = useCallback(async () => {
    try {
      const s = await http.get<Settings>('/api/settings')
      setBase(s)
      setDraft(structuredClone(s))
      setLoadError(null)
    } catch (e) {
      setLoadError(translateErrorValue(i18n, e))
    }
  }, [i18n])

  const loadBackups = useCallback(async () => {
    try {
      setBackups(await http.get<BackupInfo[]>('/api/backups'))
    } catch {
      setBackups(null)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次远端设置与备份
    void load()
    void loadBackups()
  }, [load, loadBackups])

  const dirty = useMemo(() => (base && draft ? dirtyKeys(draft, base) : []), [base, draft])
  const isDirty = dirty.length > 0
  const changed = (k: SettingsKey) => dirty.includes(k)
  const errorCount = Object.keys(fieldErrors).length

  const latest = useMemo(() => {
    if (!backups || backups.length === 0) return null
    return [...backups].sort((a, b) => (parseTime(b.created_at) ?? 0) - (parseTime(a.created_at) ?? 0))[0]
  }, [backups])

  function update(mutate: (s: Settings) => void, clearKeys: string[] = []) {
    setDraft((d) => {
      if (!d) return d
      const next = structuredClone(d)
      mutate(next)
      return next
    })
    if (clearKeys.length > 0) {
      setFieldErrors((prev) => {
        const n = { ...prev }
        for (const k of Object.keys(n)) if (clearKeys.some((c) => k === c || k.startsWith(`${c}[`))) delete n[k]
        return n
      })
    }
  }

  function errorText(key: string): string | null {
    const code = fieldErrors[key]
    if (!code) return null
    for (const k of [`settings.fieldError.${key}.${code}`, `settings.fieldError.${code}`]) {
      if (i18n.exists(k)) return t(k)
    }
    return t('settings.fieldError.invalid')
  }

  async function save() {
    if (!draft || !base || saving) return
    setSaving(true)
    setFailure(null)
    setFieldErrors({})
    try {
      const body = normalizeForSave(draft)
      const saved = await http.put<Settings>('/api/settings', body)
      const httpsChanged = saved.https_enabled !== base.https_enabled
      setBase(saved)
      setDraft(structuredClone(saved))
      toast.show(httpsChanged ? t('settings.savedRestart') : t('settings.saved'))
    } catch (e) {
      if (isApiError(e) && e.code === 'validation.failed') {
        setFieldErrors((e.details.fields ?? {}) as Record<string, string>)
      } else {
        setFailure(translateErrorValue(i18n, e))
      }
    } finally {
      setSaving(false)
    }
  }

  function discard() {
    if (!base) return
    setDraft(structuredClone(base))
    setFieldErrors({})
    setFailure(null)
    toast.show(t('settings.discarded'))
  }

  async function backupNow() {
    setBackingUp(true)
    try {
      const info = await http.post<BackupInfo>('/api/backups')
      toast.show(t('system.backups.created', { name: info.name }))
      await loadBackups()
    } catch (e) {
      toast.show(translateErrorValue(i18n, e), 'warn')
    } finally {
      setBackingUp(false)
    }
  }

  const header = <PageHeader no="05" title={t('pages.settings')} sub={t('settings.sub')} />
  if (loadError) {
    return (
      <>
        {header}
        <div className="p-6 mobile:p-3.5">
          <Note tone="crit" role="alert">
            {t('common.withDetail', { summary: t('settings.loadFailed'), detail: loadError })}{' '}
            <button type="button" className="underline underline-offset-2" onClick={() => void load()}>
              {t('common.retry')}
            </button>
          </Note>
        </div>
      </>
    )
  }
  if (!draft || !base) {
    return (
      <>
        {header}
        <div className="p-6 text-[13px] text-muted-foreground">{t('common.loading')}</div>
      </>
    )
  }

  const mark = (k: SettingsKey) => ({ changed: changed(k), changedLabel: t('settings.changed') })
  const tzOptions = timezoneOptions(draft.timezone)
  const proxyErrors = Object.fromEntries(
    Object.entries(trustedProxyErrors(fieldErrors)).map(([i, code]) => [
      i,
      i18n.exists(`settings.fieldError.trusted_proxies.${code}`) ? t(`settings.fieldError.trusted_proxies.${code}`) : t('settings.fieldError.invalid'),
    ]),
  )

  return (
    <>
      {header}
      <UnsavedGuard dirty={isDirty} textKey="settings.leave" />
      <div className="grid grid-cols-[208px_minmax(0,1fr)] items-start gap-5 p-6 pb-10 mobile:grid-cols-1 mobile:gap-3 mobile:p-3.5">
        <AnchorNav />
        <div className="flex min-w-0 flex-col gap-3">
          {failure && (
            <Note tone="crit" role="alert">
              {t('common.withDetail', { summary: t('settings.saveFailed'), detail: failure })}
            </Note>
          )}

          <Section id="sec-general" no="05.1" title={t('settings.general.title')} meta={t('settings.general.meta')}>
            <FormRow label={t('settings.general.language')} fieldKey="language" {...mark('language')}>
              <Segmented
                ariaLabel={t('settings.general.language')}
                value={draft.language === 'en' ? 'en' : 'zh'}
                onChange={(language) => update((s) => void (s.language = language), ['language'])}
                options={[
                  { value: 'zh', label: '中文', title: '中文' },
                  { value: 'en', label: 'English', title: 'English' },
                ]}
              />
              {errorText('language') && <FieldError>{errorText('language')}</FieldError>}
              <FieldHelp>{t('settings.general.languageHelp')}</FieldHelp>
            </FormRow>
            <FormRow label={t('settings.general.timezone')} fieldKey="timezone" htmlFor="set-tz" {...mark('timezone')}>
              <Select id="set-tz" className="max-w-[320px]" value={draft.timezone} invalid={!!errorText('timezone')} onChange={(e) => update((s) => void (s.timezone = e.target.value), ['timezone'])}>
                {tzOptions.map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </Select>
              {errorText('timezone') && <FieldError>{errorText('timezone')}</FieldError>}
              <FieldHelp>{t('settings.general.timezoneHelp')}</FieldHelp>
            </FormRow>
            <FormRow label={t('settings.general.accessUrl')} fieldKey="access_url" htmlFor="set-url" {...mark('access_url')}>
              <Input
                id="set-url"
                className="max-w-[440px] font-mono"
                placeholder={t('settings.general.accessUrlPlaceholder')}
                value={draft.access_url}
                invalid={!!errorText('access_url')}
                onChange={(e) => update((s) => void (s.access_url = e.target.value), ['access_url'])}
              />
              {errorText('access_url') && <FieldError>{errorText('access_url')}</FieldError>}
              <FieldHelp>{t('settings.general.accessUrlHelp')}</FieldHelp>
            </FormRow>
          </Section>

          <Section id="sec-net" no="05.2" title={t('settings.net.title')} meta={t('settings.net.meta')}>
            <FormRow label={t('settings.net.https')} fieldKey="https_enabled" {...mark('https_enabled')}>
              <div className="flex flex-wrap items-center gap-3">
                <Switch checked={draft.https_enabled} onChange={(v) => update((s) => void (s.https_enabled = v))} ariaLabel={t('settings.net.https')} />
                <span className="text-[12.5px] text-ink-2">
                  {t('settings.net.httpsNow', { scheme: window.location.protocol === 'https:' ? 'HTTPS' : 'HTTP', port: window.location.port || (window.location.protocol === 'https:' ? '443' : '80') })}
                </span>
                <span className={cn('rounded-[2px] border px-1.5 py-0.5 font-mono text-[10.5px]', changed('https_enabled') ? 'border-signal text-signal-text' : 'border-border text-muted-foreground')}>
                  {t('settings.net.httpsRestart')}
                </span>
              </div>
              <FieldHelp>{t('settings.net.httpsHelp')}</FieldHelp>
              <FieldHelp className="text-status-warn">{t('settings.net.httpsKiosk')}</FieldHelp>
            </FormRow>
            <FormRow label={t('settings.net.trusted')} fieldKey="trusted_proxies" {...mark('trusted_proxies')}>
              <TrustedProxies value={draft.trusted_proxies} rowErrors={proxyErrors} onChange={(next) => update((s) => void (s.trusted_proxies = next), ['trusted_proxies'])} />
              <FieldHelp>
                {t('settings.net.trustedHelp').split('X-Forwarded-*').map((part, i, arr) => (
                  <span key={i}>
                    {part}
                    {i < arr.length - 1 && <code className="rounded-[2px] border border-border px-1 font-mono text-[11px]">X-Forwarded-*</code>}
                  </span>
                ))}
              </FieldHelp>
            </FormRow>
            <FormRow label={t('settings.net.password')} fieldKey="admin.password">
              <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => setPwOpen(true)}>
                <KeyRound /> {t('settings.net.passwordChange')}
              </Button>
            </FormRow>
            <FormRow label={t('settings.net.lockout')} fieldKey="login.lockout">
              <div className="flex flex-wrap gap-1.5 font-mono text-[11.5px]">
                {(['lockoutTimes', 'lockoutFor', 'lockoutPerIp'] as const).map((k) => (
                  <span key={k} className="rounded-[2px] border border-border px-1.5 py-0.5">
                    {t(`settings.net.${k}`)}
                  </span>
                ))}
              </div>
              <FieldHelp>{t('settings.net.lockoutHelp')}</FieldHelp>
            </FormRow>
          </Section>

          <Section id="sec-display" no="05.3" title={t('settings.display.title')} meta={t('settings.display.meta')}>
            <FormRow label={t('settings.display.reduce')} fieldKey="reduce_effects" {...mark('reduce_effects')}>
              <div className="flex items-start gap-3">
                <Switch checked={draft.reduce_effects} onChange={(v) => update((s) => void (s.reduce_effects = v))} ariaLabel={t('settings.display.reduce')} className="mt-0.5" />
                <span className="text-[12.5px] leading-[1.55] text-ink-2">{t('settings.display.reduceHelp')}</span>
              </div>
            </FormRow>
            <FormRow label={t('settings.display.theme')} fieldKey="admin.theme">
              <ThemeSegmented />
              <FieldHelp>{t('settings.display.themeHelp')}</FieldHelp>
            </FormRow>
          </Section>

          <Section id="sec-retention" no="05.4" title={t('settings.retention.title')} meta={t('settings.retention.meta')}>
            <FormRow label={t('settings.retention.label')} fieldKey="retention">
              <div className="grid max-w-[560px] grid-cols-2 gap-x-4 gap-y-3 mobile:grid-cols-1">
                <RetentionField
                  id="ret-raw"
                  label={t('settings.retention.raw')}
                  value={draft.retention.raw_hours}
                  presets={sortedUnique(rawHourPresets, draft.retention.raw_hours)}
                  unit="hours"
                  changed={changed('retention.raw_hours')}
                  error={errorText('retention.raw_hours')}
                  onChange={(v) => update((s) => void (s.retention.raw_hours = v), ['retention.raw_hours'])}
                />
                <RetentionField
                  id="ret-5m"
                  label={t('settings.retention.fiveMin')}
                  value={draft.retention.five_min_days}
                  presets={sortedUnique(fiveMinDayPresets, draft.retention.five_min_days)}
                  unit="days"
                  changed={changed('retention.five_min_days')}
                  error={errorText('retention.five_min_days')}
                  onChange={(v) => update((s) => void (s.retention.five_min_days = v), ['retention.five_min_days'])}
                />
                <RetentionField
                  id="ret-1h"
                  label={t('settings.retention.hour')}
                  value={draft.retention.hour_days}
                  presets={sortedUnique(hourDayPresets, draft.retention.hour_days)}
                  unit="days"
                  changed={changed('retention.hour_days')}
                  error={errorText('retention.hour_days')}
                  onChange={(v) => update((s) => void (s.retention.hour_days = v), ['retention.hour_days'])}
                />
              </div>
              <FieldHelp>{t('settings.retention.help')}</FieldHelp>
            </FormRow>
          </Section>

          <Section
            id="sec-backup"
            no="05.5"
            title={t('settings.backup.title')}
            meta={
              latest
                ? t('settings.backup.last', { time: formatDateTime(parseTime(latest.created_at) ?? 0, i18n.language), reason: t(`settings.reason.${latest.reason}`, { defaultValue: latest.reason }) })
                : backups
                  ? t('settings.backup.none')
                  : undefined
            }
          >
            <FormRow label={t('settings.backup.daily')} fieldKey="backup.daily_at" htmlFor="set-backup-at" {...mark('backup.daily_at')}>
              <Input
                id="set-backup-at"
                type="time"
                className="w-[130px] font-mono"
                value={draft.backup.daily_at}
                invalid={!!errorText('backup.daily_at')}
                onChange={(e) => update((s) => void (s.backup.daily_at = e.target.value), ['backup.daily_at'])}
              />
              {errorText('backup.daily_at') && <FieldError>{errorText('backup.daily_at')}</FieldError>}
              <FieldHelp>{t('settings.backup.dailyHelp')}</FieldHelp>
            </FormRow>
            <FormRow label={t('settings.backup.keep')} fieldKey="backup.keep" {...mark('backup.keep')}>
              <Stepper
                value={draft.backup.keep}
                min={1}
                max={30}
                unit={t('settings.backup.keepUnit')}
                ariaLabel={t('settings.backup.keep')}
                decrementLabel="−"
                incrementLabel="+"
                onChange={(v) => update((s) => void (s.backup.keep = v), ['backup.keep'])}
              />
              {errorText('backup.keep') && <FieldError>{errorText('backup.keep')}</FieldError>}
              {changed('backup.keep') && <FieldHelp>{t('settings.backup.keepOriginal', { n: base.backup.keep })}</FieldHelp>}
            </FormRow>
            <FormRow label={t('settings.backup.download')} fieldKey="backup.download">
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" className="rounded-[2px]" disabled={!latest} onClick={() => setDownloading(latest)}>
                  <Download />
                  {latest ? t('settings.backup.downloadBtn', { name: latest.name }) : t('settings.backup.downloadNone')}
                </Button>
                {latest && <span className="font-mono text-[11.5px] text-muted-foreground">{formatBytes(latest.size, i18n.language)}</span>}
                <Button variant="outline" size="sm" className="rounded-[2px]" disabled={backingUp} onClick={() => void backupNow()}>
                  {t('settings.backup.backupNow')}
                </Button>
              </div>
            </FormRow>
            <FormRow label={<span className="inline-flex items-center gap-1.5">{t('settings.backup.nas')}<span className="rounded-[2px] border border-border px-1 font-mono text-[10px] text-muted-foreground">{t('settings.backup.nasBadge')}</span></span>} fieldKey="backup.remote" className="opacity-50">
              <div className="inline-flex overflow-hidden rounded-[2px] border border-line-strong">
                <button type="button" disabled className="h-7 cursor-not-allowed px-3 text-xs">SMB</button>
                <button type="button" disabled className="h-7 cursor-not-allowed border-l border-border px-3 text-xs">WebDAV</button>
              </div>
              <FieldHelp>{t('settings.backup.nasHelp')}</FieldHelp>
            </FormRow>
          </Section>
        </div>
      </div>

      {isDirty && (
        <div role="region" aria-label={t('settings.bar.dirty', { count: dirty.length })} className="sticky bottom-0 z-30 mt-auto flex flex-wrap items-center gap-3 border-t border-line-strong bg-card px-6 py-2.5 mobile:bottom-16 mobile:px-3.5">
          <span className="inline-flex items-center gap-2 text-[13px] font-medium">
            <span aria-hidden className="size-2 rounded-full bg-signal" />
            {t('settings.bar.dirty', { count: dirty.length })}
          </span>
          {errorCount > 0 && <span className="text-[12.5px] text-status-crit">{t('settings.bar.errors', { count: errorCount })}</span>}
          <span className="text-[12px] text-muted-foreground mobile:hidden">{t('settings.bar.hint')}</span>
          <span className="flex-1" />
          <Button variant="outline" size="sm" className="rounded-[2px]" disabled={saving} onClick={discard}>
            {t('settings.bar.discard')}
          </Button>
          <Button size="sm" className="rounded-[2px]" disabled={saving} onClick={() => void save()}>
            <Save /> {saving ? t('settings.bar.saving') : t('settings.bar.save')}
          </Button>
        </div>
      )}

      <PasswordDialog
        open={pwOpen}
        onClose={() => setPwOpen(false)}
        onChanged={() => {
          setPwOpen(false)
          toast.show(t('settings.password.done'))
        }}
      />
      <BackupDownloadDialog backup={downloading} onClose={() => setDownloading(null)} />
    </>
  )
}

function ThemeSegmented() {
  const { t } = useTranslation()
  const choice = useThemeChoice()
  const opt = (value: ThemeChoice, icon: ReactNode, label: string) => ({
    value,
    label: (
      <>
        {icon}
        {label}
      </>
    ),
    title: label,
  })
  return (
    <Segmented<ThemeChoice>
      ariaLabel={t('settings.display.theme')}
      value={choice}
      onChange={setThemeChoice}
      options={[
        opt('light', <Sun size={14} />, t('shell.theme.light')),
        opt('dark', <Moon size={14} />, t('shell.theme.dark')),
        opt('system', <Monitor size={14} />, t('shell.theme.system')),
      ]}
    />
  )
}

interface RetentionFieldProps {
  id: string
  label: string
  value: number
  presets: number[]
  unit: 'hours' | 'days'
  changed: boolean
  error: string | null
  onChange(v: number): void
}

function RetentionField({ id, label, value, presets, unit, changed, error, onChange }: RetentionFieldProps) {
  const { t } = useTranslation()
  return (
    <div>
      <label htmlFor={id} className="mb-1 flex items-center gap-1.5 text-[12px] text-muted-foreground">
        {label}
        {changed && <span aria-hidden className="size-1.5 rounded-full bg-signal" />}
      </label>
      <Select id={id} value={String(value)} invalid={!!error} onChange={(e) => onChange(Number(e.target.value))}>
        {presets.map((p) => (
          <option key={p} value={p}>
            {t(`settings.retention.${unit}`, { n: p })}
          </option>
        ))}
      </Select>
      {error && <FieldError>{error}</FieldError>}
    </div>
  )
}

// 左侧锚点导航：随滚动高亮当前分组；窄屏改为横向一排
function AnchorNav() {
  const { t } = useTranslation()
  const ref = useRef<HTMLElement>(null)
  const [active, setActive] = useState<string>(anchors[0].id)

  useEffect(() => {
    let scroller: HTMLElement | null = ref.current?.parentElement ?? null
    while (scroller && getComputedStyle(scroller).overflowY !== 'auto' && getComputedStyle(scroller).overflowY !== 'scroll') scroller = scroller.parentElement
    const target: HTMLElement | Window = scroller ?? window
    const spy = () => {
      const top = scroller ? scroller.getBoundingClientRect().top : 0
      let cur: string = anchors[0].id
      for (const a of anchors) {
        const el = document.getElementById(a.id)
        if (el && el.getBoundingClientRect().top - top < 120) cur = a.id
      }
      const atBottom = scroller ? scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 4 : false
      setActive(atBottom ? anchors[anchors.length - 1].id : cur)
    }
    target.addEventListener('scroll', spy, { passive: true })
    return () => target.removeEventListener('scroll', spy)
  }, [])

  return (
    <nav
      ref={ref}
      aria-label={t('settings.anchors')}
      className="sticky top-4 flex flex-col rounded-[2px] border border-line-strong bg-card mobile:static mobile:flex-row mobile:overflow-x-auto"
    >
      {anchors.map((a, i) => (
        <a
          key={a.id}
          href={`#${a.id}`}
          aria-current={active === a.id ? 'location' : undefined}
          onClick={(e) => {
            e.preventDefault()
            document.getElementById(a.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
            setActive(a.id)
          }}
          className={cn(
            'flex items-center gap-2 border-border px-3 py-2 text-[13px] whitespace-nowrap outline-none focus-visible:ring-2 focus-visible:ring-ring mobile:flex-none',
            i > 0 && 'border-t mobile:border-t-0 mobile:border-l',
            active === a.id ? 'bg-inv-bg text-inv-ink' : 'text-ink-2 hover:bg-panel-2',
          )}
        >
          <NumberTag no={a.no} className={active === a.id ? 'border-inv-ink/40 text-inv-ink' : undefined} />
          {t(`settings.${a.key}.title`)}
        </a>
      ))}
    </nav>
  )
}
