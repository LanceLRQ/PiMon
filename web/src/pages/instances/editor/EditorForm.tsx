import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { buildConfig, initialValues, visibleKeys } from '@/forms/build'
import { formatDurationSeconds, parseDurationSeconds } from '@/forms/duration'
import { clearErrors, type ErrorMap, type FormValues } from '@/forms/model'
import { SchemaFields } from '@/forms/SchemaFields'
import { translateErrorValue } from '@/i18n/errors'
import { cn } from '@/lib/utils'
import type { InstanceDetail, InstanceInput, InstanceRunResult, PluginInfo } from '@/types/generated'
import { Input } from '@/ui/input'
import { Note } from '@/ui/note'
import { NumberTag } from '@/ui/numbered-label'
import { Segmented } from '@/ui/segmented'
import { useToast } from '@/ui/toast'
import { runTimeoutMs } from '../actions'
import { PluginIcon } from './PluginIcon'
import { Pane } from './Pane'
import { SavePanel } from './SavePanel'
import type { TestOutcome } from './types'

// 服务端对实例刷新间隔的全局范围（秒）
const minIntervalFloor = 5
const maxIntervalSeconds = 86400
const metaKeys = new Set(['name', 'interval_seconds', 'plugin_id', '_config'])

interface EditorFormProps {
  mode: 'new' | 'edit'
  plugin: PluginInfo
  // 编辑时的现有实例
  detail: InstanceDetail | null
  outcome: TestOutcome | null
  onOutcome: (o: TestOutcome | null) => void
  // 编辑保存成功后用服务端返回的详情重置表单
  onDetail: (d: InstanceDetail) => void
}

interface IntervalState {
  override: boolean
  text: string
}

function fieldProblems(detail: InstanceDetail | null): ErrorMap {
  const out: ErrorMap = {}
  for (const [k, v] of Object.entries(detail?.problems ?? {})) if (k !== '_config') out[k] = v
  return out
}

function topKey(path: string): string {
  return path.split(/[.[]/)[0]
}

// 配置表单（中栏）与保存区（右栏）。状态在这里；换插件或保存后由上层通过 key 重建。
export function EditorForm({ mode, plugin, detail, outcome, onOutcome, onDetail }: EditorFormProps) {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const fields = plugin.config_schema
  const refill = detail?.problems?.['_config'] !== undefined
  const floor = Math.max(minIntervalFloor, plugin.min_interval_seconds)
  const defaultInterval = formatDurationSeconds(plugin.interval_seconds)

  const [name, setName] = useState(detail?.name ?? '')
  const [values, setValues] = useState<FormValues>(() => initialValues(fields, detail?.config, { refill }))
  const [interval, setIntervalState] = useState<IntervalState>(() =>
    detail && detail.interval_seconds > 0
      ? { override: true, text: formatDurationSeconds(detail.interval_seconds) }
      : { override: false, text: defaultInterval },
  )
  const [serverErrors, setServerErrors] = useState<ErrorMap>(() => fieldProblems(detail))
  const [submitted, setSubmitted] = useState(false)
  const [busy, setBusy] = useState<'save' | 'test' | null>(null)
  const resultRef = useRef<HTMLDivElement>(null)

  // 窄屏下各栏纵向排列，测试有了结果要把结果带到视野里
  const phase = outcome?.phase
  useEffect(() => {
    if (phase && phase !== 'running' && typeof matchMedia === 'function' && !matchMedia('(min-width: 1100px)').matches) {
      resultRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
    }
  }, [phase])

  // 即时校验：配置字段按 schema，名称与刷新间隔按服务端同一套规则
  const built = useMemo(() => buildConfig(fields, values), [fields, values])
  const metaErrors = useMemo(() => {
    const e: ErrorMap = {}
    if (name.trim() === '') e.name = 'required'
    else if ([...name.trim()].length > 100) e.name = 'out_of_range'
    if (interval.override) {
      const sec = parseDurationSeconds(interval.text)
      if (sec === null || !Number.isInteger(sec)) e.interval_seconds = 'invalid'
      else if (sec < floor || sec > maxIntervalSeconds) e.interval_seconds = 'out_of_range'
    }
    return e
  }, [name, interval, floor])

  const clientErrors: ErrorMap = { ...built.errors, ...metaErrors }
  const errors: ErrorMap = submitted ? { ...clientErrors, ...serverErrors } : serverErrors
  const errorCount = Object.keys(clientErrors).length

  const shown = visibleKeys(fields, values)
  const unmapped = Object.entries(serverErrors).filter(([k]) => !metaKeys.has(topKey(k)) && !shown.has(topKey(k)))

  const edit = (path: string) => setServerErrors((e) => clearErrors(e, path))
  const intervalSeconds = interval.override ? (parseDurationSeconds(interval.text) ?? 0) : 0
  const intervalLabel = interval.override
    ? t('editor.save.intervalCustom', { value: interval.text.trim() || '—' })
    : t('editor.save.intervalDefault', { value: defaultInterval })
  const agentOnly = !plugin.runs_on.includes('hub')

  const describe = (err: unknown): string => {
    let text = translateErrorValue(i18n, err)
    if (isApiError(err) && typeof err.details.message === 'string' && (err.code === 'run.failed' || err.code === 'run.timeout')) {
      text += `：${err.details.message}`
    }
    return text
  }

  const submit = async (test: boolean) => {
    setSubmitted(true)
    if (errorCount > 0) {
      toast.show(t('editor.save.fixFirst', { count: errorCount }), 'warn')
      // 把焦点带到第一个出问题的控件
      requestAnimationFrame(() => document.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus())
      return
    }
    const body: InstanceInput = { name: name.trim(), config: built.config, interval_seconds: intervalSeconds }
    if (mode === 'new') body.plugin_id = plugin.id
    setBusy(test ? 'test' : 'save')
    let saved: InstanceDetail
    try {
      saved =
        mode === 'new'
          ? await http.post<InstanceDetail>('/api/instances', body)
          : await http.put<InstanceDetail>(`/api/instances/${detail?.id}`, body)
    } catch (err) {
      setBusy(null)
      if (isApiError(err) && err.code === 'validation.failed' && typeof err.details.fields === 'object' && err.details.fields) {
        setServerErrors(err.details.fields as ErrorMap)
        toast.show(translateErrorValue(i18n, err), 'warn')
      } else {
        toast.show(translateErrorValue(i18n, err), 'warn')
      }
      return
    }
    setServerErrors({})
    if (!test) {
      setBusy(null)
      toast.show(t('editor.save.saved', { name: saved.name }))
      if (mode === 'new') navigate('/instances')
      else onDetail(saved)
      return
    }
    onOutcome({ phase: 'running' })
    const started = Date.now()
    let result: TestOutcome
    try {
      const res = await http.post<InstanceRunResult>(`/api/instances/${saved.id}/run`, undefined, { timeoutMs: runTimeoutMs(plugin) })
      result = { phase: 'ok', instance: res.instance, report: res.report, at: Date.now(), wallMs: Date.now() - started }
      toast.show(t('editor.save.savedTested', { name: saved.name }))
    } catch (err) {
      result = { phase: 'failed', code: isApiError(err) ? err.code : 'internal', message: describe(err), saved: true, at: Date.now() }
      toast.show(t('editor.save.saved', { name: saved.name }))
    }
    setBusy(null)
    if (mode === 'new') {
      // 新建后切到编辑页，结果随路由带过去
      navigate(`/instances/${saved.id}/edit`, { replace: true, state: { outcome: result } })
    } else {
      onOutcome(result)
      onDetail(saved)
    }
  }

  const requiredCount = fields.filter((f) => f.required).length

  return (
    <>
      <Pane no="02" title={t('editor.config.title')} meta={t('editor.config.meta', { id: plugin.id, version: plugin.version })} className="min-[1100px]:border-r">
        <div className="min-h-0 flex-1 bg-background p-4 min-[1100px]:overflow-y-auto mobile:p-3">
          <div className="space-y-3">
            {refill && (
              <Note tone="crit" role="alert">
                {t('editor.config.refill')}
              </Note>
            )}
            {!refill && detail && Object.keys(fieldProblems(detail)).length > 0 && (
              <Note tone="warn" role="status">
                {t('editor.config.problems')}
              </Note>
            )}
            {agentOnly && (
              <Note tone="warn" role="status">
                {t('editor.config.agentOnly')}
              </Note>
            )}
            {unmapped.length > 0 && (
              <Note tone="crit" role="alert">
                <div className="font-medium">{t('editor.config.unmapped')}</div>
                <ul className="mt-0.5 font-mono text-[12px]">
                  {unmapped.map(([k, v]) => (
                    <li key={k}>
                      {k}：{t(`form.errors.${v}`, { defaultValue: v })}
                    </li>
                  ))}
                </ul>
              </Note>
            )}

            <section className="border border-border bg-card">
              <div className="flex items-center gap-3 p-4">
                <span className="grid size-10 place-items-center rounded-[3px] border border-line-strong bg-panel-2 text-ink-2">
                  <PluginIcon plugin={plugin} size={18} />
                </span>
                <div className="min-w-0">
                  <div className="text-base leading-tight font-medium">{plugin.name}</div>
                  <div className="font-mono text-[11.5px] text-muted-foreground">
                    {plugin.id} · {plugin.origin} · runs_on: {plugin.runs_on.join(', ')}
                  </div>
                </div>
              </div>
              {plugin.id === 'weather' && (
                <p className="border-t border-border px-4 py-2 text-[12px] text-muted-foreground">
                  {t('detail.weatherCredit')}{' '}
                  <a href="https://open-meteo.com/" target="_blank" rel="noreferrer noopener" className="underline underline-offset-2">
                    Weather data by Open-Meteo.com
                  </a>
                </p>
              )}
              <div className="flex h-10 items-center gap-2 border-t border-border px-4">
                <NumberTag no="02.1" />
                <h3 className="text-[13px] font-medium">{t('editor.config.pluginFields')}</h3>
                <span className="ml-auto font-mono text-[11px] text-muted-foreground">
                  {t('editor.config.fieldCount', { count: fields.length, required: requiredCount })}
                </span>
              </div>
              <NameRow name={name} onChange={(v) => { setName(v); edit('name') }} error={errors.name} example={plugin.name} />
              <SchemaFields fields={fields} values={values} onChange={setValues} errors={errors} onEdit={edit} />
            </section>

            <section className="border border-border bg-card">
              <div className="flex h-10 items-center gap-2 border-b border-border px-4">
                <NumberTag no="02.2" />
                <h3 className="text-[13px] font-medium">{t('editor.config.common')}</h3>
                <span className="ml-auto font-mono text-[11px] text-muted-foreground">{t('editor.config.commonMeta')}</span>
              </div>
              <RunsOnRow />
              <IntervalRow
                state={interval}
                onChange={(s) => {
                  setIntervalState(s)
                  edit('interval_seconds')
                }}
                defaultText={defaultInterval}
                floorText={formatDurationSeconds(floor)}
                error={errors.interval_seconds}
              />
            </section>
          </div>
        </div>
      </Pane>
      <Pane no="03" title={t('editor.save.title')} meta={<SaveBadge pass={errorCount === 0} />}>
        <SavePanel
          plugin={plugin}
          name={name}
          intervalLabel={intervalLabel}
          errorCount={errorCount}
          busy={busy}
          disabled={agentOnly}
          outcome={outcome}
          resultRef={resultRef}
          onSave={() => void submit(false)}
          onSaveTest={() => void submit(true)}
        />
      </Pane>
    </>
  )
}

function SaveBadge({ pass }: { pass: boolean }) {
  const { t } = useTranslation()
  return <span className={pass ? 'text-status-ok' : 'text-status-crit'}>{pass ? t('editor.save.valid') : t('editor.save.invalid')}</span>
}

interface RowShellProps {
  label: string
  keyName: string
  children: React.ReactNode
  help?: string
  error?: string
  htmlFor?: string
  required?: boolean
}

function RowShell({ label, keyName, children, help, error, htmlFor, required }: RowShellProps) {
  const { t } = useTranslation()
  return (
    <div className={cn('grid grid-cols-[200px_minmax(0,1fr)] gap-x-4 gap-y-1.5 border-t border-border px-4 py-3 first:border-t-0 mobile:grid-cols-1')}>
      <div className="pt-1.5 text-[13px]">
        <label htmlFor={htmlFor} className="font-medium">
          {label}
          {required && (
            <span className="ml-0.5 text-status-crit" aria-hidden>
              *
            </span>
          )}
        </label>
        {required && <span className="sr-only"> {t('form.required')}</span>}
        <div className="mt-0.5 font-mono text-[10.5px] text-muted-foreground">{keyName}</div>
      </div>
      <div className="min-w-0">
        {children}
        {help && <p className="mt-1.5 text-[12px] leading-[1.55] text-muted-foreground">{help}</p>}
        {error && (
          <p role="alert" className="mt-1.5 text-[12px] text-status-crit">
            {t(`form.errors.${error}`, { defaultValue: error })}
          </p>
        )}
      </div>
    </div>
  )
}

function NameRow({ name, onChange, error, example }: { name: string; onChange: (v: string) => void; error: string | undefined; example: string }) {
  const { t } = useTranslation()
  return (
    <RowShell label={t('editor.config.name')} required keyName="name" help={t('editor.config.nameHelp')} error={error} htmlFor="f-instance-name">
      <Input
        id="f-instance-name"
        value={name}
        invalid={!!error}
        maxLength={200}
        placeholder={t('editor.config.namePlaceholder', { example })}
        onChange={(e) => onChange(e.target.value)}
        className="max-w-[420px]"
      />
    </RowShell>
  )
}

function RunsOnRow() {
  const { t } = useTranslation()
  return (
    <RowShell label={t('editor.config.runsOn')} keyName="runs_on" help={t('editor.config.runsOnHelp')}>
      <Segmented
        ariaLabel={t('editor.config.runsOn')}
        value="hub"
        onChange={() => {}}
        options={[
          { value: 'hub', label: t('editor.config.runsHub'), title: t('editor.config.runsHub') },
        ]}
      />
      <span className="ml-2 text-[11.5px] text-muted-foreground">{t('editor.config.agentLater')}</span>
    </RowShell>
  )
}

function IntervalRow({
  state,
  onChange,
  defaultText,
  floorText,
  error,
}: {
  state: IntervalState
  onChange: (s: IntervalState) => void
  defaultText: string
  floorText: string
  error: string | undefined
}) {
  const { t } = useTranslation()
  return (
    <RowShell label={t('editor.config.interval')} keyName="interval" help={t('editor.config.intervalHelp', { min: floorText })} error={error} htmlFor="f-instance-interval">
      <div className="flex flex-wrap items-center gap-3">
        <Input
          id="f-instance-interval"
          value={state.override ? state.text : defaultText}
          readOnly={!state.override}
          invalid={!!error}
          spellCheck={false}
          onChange={(e) => onChange({ ...state, text: e.target.value })}
          className="w-[120px] font-mono mobile:w-full"
        />
        <label className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={state.override}
            onChange={(e) => onChange({ override: e.target.checked, text: state.text || defaultText })}
            className="size-4 accent-[var(--signal)]"
          />
          {t('editor.config.intervalOverride', { value: defaultText })}
        </label>
      </div>
    </RowShell>
  )
}
