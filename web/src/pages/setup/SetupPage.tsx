import { AlertTriangle, Check, ChevronLeft, ChevronRight, Info, LayoutDashboard, Lock } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { useSession } from '@/app/session'
import { translateErrorValue } from '@/i18n/errors'
import { cn } from '@/lib/utils'
import { serverNow } from '@/store/live-store'
import { Button } from '@/ui/button'
import { Input, PasswordInput } from '@/ui/input'
import { Note } from '@/ui/note'
import { Segmented } from '@/ui/segmented'
import { AuthShell, hostLabel } from '../auth/AuthShell'
import { formatClockTime, formatRemaining, lockInfoOf, makeLock, useCountdown, type Lock as LockState } from '../auth/lock'
import { minPasswordLength, passwordStrength } from '../auth/password-strength'
import { isSetupCodeComplete, joinSetupCode, splitSetupCode } from '../auth/setup-code'
import { CodeInput } from './CodeInput'
import { browserTimezone, timezoneOptions } from './timezones'

type StepIndex = 0 | 1 | 2 | 3
type FormLanguage = 'zh' | 'en'

// 服务端字段错误对应的步骤
const fieldStep: Record<string, StepIndex> = {
  setup_code: 0,
  password: 1,
  language: 2,
  timezone: 2,
  access_url: 2,
}

type Failure = { kind: 'origin' } | { kind: 'other'; message: string }

function FormRow({ label, fkey, required, children }: { label: string; fkey?: string; required?: boolean; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[150px_minmax(0,1fr)] gap-4 border-b border-border px-4 py-3.5 mobile:grid-cols-1 mobile:gap-1.5">
      <div className="text-[13px]">
        {label}
        {required && <span className="ml-0.5 text-status-crit">*</span>}
        {fkey && <span className="mt-[3px] block font-mono text-[11px] text-muted-foreground">{fkey}</span>}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function StepHead({ no, title, meta }: { no: string; title: string; meta: string }) {
  return (
    <div className="flex items-center gap-2.5 border-b border-border px-4 py-2.5">
      <span className="rounded-[2px] border border-border px-1 font-mono text-[10.5px] leading-[15px] text-muted-foreground">{no}</span>
      <h2 className="text-[13.5px] font-medium">{title}</h2>
      <span className="ml-auto text-xs text-muted-foreground">{meta}</span>
    </div>
  )
}

// 首次设置：设置码 → 管理员密码 → 基础设置，最后一步一次性提交
export function SetupPage() {
  const { t, i18n } = useTranslation()
  const { refresh } = useSession()
  const navigate = useNavigate()

  const defaultTz = useMemo(() => browserTimezone(), [])
  const tzList = useMemo(() => timezoneOptions(defaultTz), [defaultTz])

  const [step, setStep] = useState<StepIndex>(0)
  const [groups, setGroups] = useState<string[]>(() => splitSetupCode(''))
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [language, setLanguage] = useState<FormLanguage>(i18n.language.startsWith('zh') ? 'zh' : 'en')
  const [timezone, setTimezone] = useState(defaultTz)
  const [accessUrl, setAccessUrl] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [codeRemaining, setCodeRemaining] = useState<number | null>(null)
  const [codeWrong, setCodeWrong] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [lock, setLock] = useState<LockState | null>(null)
  const [busy, setBusy] = useState(false)
  const [finishedStrength, setFinishedStrength] = useState(0)

  const onExpire = useCallback(() => setLock(null), [])
  const remainingMs = useCountdown(lock, onExpire)
  const locked = lock !== null

  const strength = passwordStrength(password)
  const passwordLen = Array.from(password).length
  const matches = password !== '' && password === confirm

  const stepsMeta = [t('setup.steps.code'), t('setup.steps.password'), t('setup.steps.basics'), t('setup.steps.done')]
  const stepsWithError = useMemo(() => {
    const s = new Set<number>()
    if (codeWrong || locked) s.add(0)
    for (const f of Object.keys(fieldErrors)) s.add(fieldStep[f] ?? 2)
    return s
  }, [codeWrong, locked, fieldErrors])

  function fieldErrorText(field: string, code: string): string {
    const specific = `setup.fieldError.${field}.${code}`
    if (i18n.exists(specific)) return t(specific)
    const generic = `setup.fieldError.${code}`
    return i18n.exists(generic) ? t(generic) : t('setup.fieldError.invalid')
  }

  function clearFieldError(field: string) {
    setFieldErrors((prev) => {
      if (!(field in prev)) return prev
      const next = { ...prev }
      delete next[field]
      return next
    })
  }

  function goNext() {
    setLocalError(null)
    if (step === 0) {
      if (!isSetupCodeComplete(groups)) {
        setLocalError(t('setup.code.incomplete'))
        return
      }
      setStep(1)
    } else if (step === 1) {
      if (passwordLen < minPasswordLength) {
        setLocalError(t('setup.password.tooShort', { min: minPasswordLength }))
        return
      }
      if (!matches) {
        setLocalError(t('setup.password.mismatch'))
        return
      }
      setStep(2)
    } else if (step === 2) {
      void submit()
    }
  }

  async function submit() {
    if (busy) return
    setBusy(true)
    setFailure(null)
    setFieldErrors({})
    setCodeWrong(false)
    const body: Record<string, string> = { setup_code: joinSetupCode(groups), password, language, timezone }
    if (accessUrl.trim() !== '') body.access_url = accessUrl.trim()
    try {
      await http.post('/api/setup', body)
      setFinishedStrength(strength)
      setStep(3)
    } catch (err) {
      const info = lockInfoOf(err, serverNow())
      if (info) {
        setLock(makeLock(info, serverNow()))
        setStep(0)
      } else if (isApiError(err) && err.code === 'setup.invalid_code') {
        setCodeWrong(true)
        setCodeRemaining(typeof err.details.remaining === 'number' ? err.details.remaining : null)
        setStep(0)
      } else if (isApiError(err) && err.code === 'validation.failed') {
        const fields = (err.details.fields ?? {}) as Record<string, string>
        setFieldErrors(fields)
        const steps = Object.keys(fields).map((f) => fieldStep[f] ?? 2)
        setStep((steps.length > 0 ? Math.min(...steps) : 2) as StepIndex)
      } else if (isApiError(err) && err.code === 'origin.mismatch') {
        setFailure({ kind: 'origin' })
      } else if (isApiError(err) && err.code === 'setup.already_done') {
        // 已有管理员：刷新会话，由守卫送到登录页
        await refresh()
      } else {
        setFailure({ kind: 'other', message: translateErrorValue(i18n, err) })
      }
    } finally {
      setBusy(false)
    }
  }

  async function enter() {
    await refresh()
    navigate('/', { replace: true })
  }

  const passwordErr = fieldErrors.password
  const strengthLabel = t(`setup.strength.${strength}`)

  return (
    <AuthShell
      topLabel={t('setup.topLabel')}
      title={t('setup.title')}
      subtitle={`${hostLabel()} · ${t('setup.noAdmin')}`}
      widthClass="max-w-[720px]"
    >
      <ol className="mb-3 grid grid-cols-4 border border-border bg-card mobile:grid-cols-2" aria-label={t('setup.title')}>
        {stepsMeta.map((label, i) => {
          const cur = i === step
          const done = i < step
          const hasError = stepsWithError.has(i)
          const badge = (
              <span
                className={cn(
                  'grid size-[22px] flex-none place-items-center rounded-[2px] border font-mono text-[11px]',
                  hasError
                    ? 'border-status-crit text-status-crit'
                    : cur
                      ? 'border-signal bg-signal text-primary-foreground'
                      : done
                        ? 'border-foreground bg-inv-bg text-inv-ink'
                        : 'border-line-strong',
                )}
              >
                {`0${i + 1}`}
            </span>
          )
          return (
            <li
              key={i}
              aria-current={cur ? 'step' : undefined}
              data-error={hasError ? 'true' : undefined}
              className={cn(
                'flex min-w-0 items-center gap-2.5 border-l border-border px-3.5 py-2.5 text-[13px] first:border-l-0 mobile:nth-[3]:border-l-0 mobile:nth-[n+3]:border-t',
                cur ? 'text-foreground shadow-[inset_0_-2px_0_var(--signal)]' : done ? 'text-ink-2' : 'text-muted-foreground',
                hasError && 'text-status-crit',
              )}
            >
              {step < 3 && done ? (
                <button
                  type="button"
                  className="flex min-w-0 items-center gap-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={() => setStep(i as StepIndex)}
                >
                  {badge}
                  <span className="truncate">{label}</span>
                </button>
              ) : (
                <>
                  {badge}
                  <span className="truncate">{label}</span>
                </>
              )}
            </li>
          )
        })}
      </ol>

      {failure?.kind === 'origin' && (
        <Note tone="crit" role="alert" icon={<AlertTriangle size={16} />} className="mb-3">
          {t('setup.originMismatch')}
        </Note>
      )}

      <section className="border border-border bg-card">
        {step === 0 && (
          <div className="min-h-[300px]">
            <StepHead no="S1" title={t('setup.steps.code')} meta={t('setup.code.meta')} />
            <FormRow label={t('setup.code.label')} fkey="setup_code" required>
              {lock && (
                <Note tone="crit" role="alert" icon={<Lock size={16} />} className="mb-3">
                  <b className="font-medium">{t('login.locked.title')}</b>
                  <br />
                  {lock.info.clientIp
                    ? t('login.locked.untilWithIp', { time: formatClockTime(lock.info.untilMs, i18n.language), ip: lock.info.clientIp })
                    : t('login.locked.until', { time: formatClockTime(lock.info.untilMs, i18n.language) })}
                  <br />
                  <span className="font-mono" data-testid="lock-countdown">
                    {t('login.locked.remaining', { remaining: formatRemaining(remainingMs) })}
                  </span>
                </Note>
              )}
              <CodeInput
                groups={groups}
                invalid={codeWrong}
                disabled={locked}
                onChange={(g) => {
                  setGroups(g)
                  setCodeWrong(false)
                  setLocalError(null)
                }}
              />
              <p className="mt-1.5 text-xs text-muted-foreground">{t('setup.code.help')}</p>
              {codeWrong && (
                <p role="alert" className="mt-1.5 text-xs text-status-crit">
                  {codeRemaining !== null ? t('setup.code.wrongRemaining', { remaining: codeRemaining }) : t('errors.setup.invalid_code')}
                </p>
              )}
              {localError && step === 0 && (
                <p role="alert" className="mt-1.5 text-xs text-status-crit">
                  {localError}
                </p>
              )}
            </FormRow>
            <FormRow label={t('setup.code.where')}>
              <Note icon={<Info size={16} />}>
                {t('setup.code.whereBody')} <code className="font-mono">pimon-hub setup-code</code>
              </Note>
            </FormRow>
          </div>
        )}

        {step === 1 && (
          <div className="min-h-[300px]">
            <StepHead no="S2" title={t('setup.password.title')} meta={t('setup.password.meta')} />
            <FormRow label={t('setup.password.label')} fkey="password" required>
              <div className="max-w-[420px]">
                <PasswordInput
                  id="setup-password"
                  aria-label={t('setup.password.label')}
                  showLabel={t('common.show')}
                  hideLabel={t('common.hide')}
                  autoComplete="new-password"
                  value={password}
                  invalid={passwordErr !== undefined}
                  onChange={(e) => {
                    setPassword(e.target.value)
                    clearFieldError('password')
                    setLocalError(null)
                  }}
                />
                <div
                  role="meter"
                  aria-label={t('setup.strength.label')}
                  aria-valuemin={0}
                  aria-valuemax={4}
                  aria-valuenow={strength}
                  aria-valuetext={strengthLabel}
                  className="mt-2 grid grid-cols-4 gap-[3px]"
                >
                  {[0, 1, 2, 3].map((i) => (
                    <i
                      key={i}
                      className={cn(
                        'h-1.5 border',
                        i < strength
                          ? strength === 4
                            ? 'border-status-ok bg-status-ok'
                            : 'border-foreground bg-foreground'
                          : 'border-border bg-panel-2',
                      )}
                    />
                  ))}
                </div>
                <div className="mt-1.5 flex justify-between text-xs text-muted-foreground">
                  <span>
                    {t('setup.strength.label')} <b className="font-medium text-foreground">{strengthLabel}</b> · {t('setup.password.length', { n: passwordLen })}
                  </span>
                  <span>{t('setup.password.min', { min: minPasswordLength })}</span>
                </div>
                {passwordErr !== undefined && (
                  <p role="alert" className="mt-1.5 text-xs text-status-crit">
                    {fieldErrorText('password', passwordErr)}
                  </p>
                )}
              </div>
            </FormRow>
            <FormRow label={t('setup.password.confirm')} fkey="confirm" required>
              <div className="max-w-[420px]">
                <PasswordInput
                  id="setup-confirm"
                  aria-label={t('setup.password.confirm')}
                  showLabel={t('common.show')}
                  hideLabel={t('common.hide')}
                  autoComplete="new-password"
                  value={confirm}
                  onChange={(e) => {
                    setConfirm(e.target.value)
                    setLocalError(null)
                  }}
                />
                {confirm !== '' && (
                  <p className={cn('mt-1.5 text-xs', matches ? 'text-status-ok' : 'text-status-crit')}>
                    {matches ? t('setup.password.match') : t('setup.password.mismatch')}
                  </p>
                )}
                {localError && step === 1 && (
                  <p role="alert" className="mt-1.5 text-xs text-status-crit">
                    {localError}
                  </p>
                )}
              </div>
            </FormRow>
          </div>
        )}

        {step === 2 && (
          <div className="min-h-[300px]">
            <StepHead no="S3" title={t('setup.steps.basics')} meta={t('setup.basics.meta')} />
            <FormRow label={t('setup.basics.language')} fkey="language">
              <Segmented<FormLanguage>
                ariaLabel={t('setup.basics.language')}
                value={language}
                onChange={(v) => {
                  setLanguage(v)
                  clearFieldError('language')
                }}
                options={[
                  { value: 'zh', label: '中文', title: '中文' },
                  { value: 'en', label: 'English', title: 'English' },
                ]}
              />
              {fieldErrors.language !== undefined && (
                <p role="alert" className="mt-1.5 text-xs text-status-crit">
                  {fieldErrorText('language', fieldErrors.language)}
                </p>
              )}
            </FormRow>
            <FormRow label={t('setup.basics.timezone')} fkey="timezone">
              <div className="max-w-[360px]">
                <select
                  aria-label={t('setup.basics.timezone')}
                  aria-invalid={fieldErrors.timezone !== undefined || undefined}
                  value={timezone}
                  onChange={(e) => {
                    setTimezone(e.target.value)
                    clearFieldError('timezone')
                  }}
                  className={cn(
                    'h-8 w-full cursor-pointer rounded-[2px] border border-line-strong bg-card px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
                    fieldErrors.timezone !== undefined && 'border-status-crit shadow-[inset_0_0_0_1px_var(--status-crit)]',
                  )}
                >
                  {tzList.map((z) => (
                    <option key={z} value={z}>
                      {z === defaultTz ? `${z}（${t('setup.basics.fromBrowser')}）` : z}
                    </option>
                  ))}
                </select>
                {fieldErrors.timezone !== undefined ? (
                  <p role="alert" className="mt-1.5 text-xs text-status-crit">
                    {fieldErrorText('timezone', fieldErrors.timezone)}
                  </p>
                ) : (
                  <p className="mt-1.5 text-xs text-muted-foreground">{t('setup.basics.timezoneHelp')}</p>
                )}
              </div>
            </FormRow>
            <FormRow label={t('setup.basics.accessUrl')} fkey={`access_url · ${t('setup.basics.optional')}`}>
              <div className="max-w-[420px]">
                <Input
                  aria-label={t('setup.basics.accessUrl')}
                  className="font-mono text-[12.5px]"
                  placeholder="https://pimon.home.arpa"
                  value={accessUrl}
                  invalid={fieldErrors.access_url !== undefined}
                  onChange={(e) => {
                    setAccessUrl(e.target.value)
                    clearFieldError('access_url')
                  }}
                />
                {fieldErrors.access_url !== undefined ? (
                  <p role="alert" className="mt-1.5 text-xs text-status-crit">
                    {fieldErrorText('access_url', fieldErrors.access_url)}
                  </p>
                ) : (
                  <p className="mt-1.5 text-xs text-muted-foreground">{t('setup.basics.accessUrlHelp')}</p>
                )}
              </div>
            </FormRow>
            {Object.entries(fieldErrors)
              .filter(([f]) => !(f in fieldStep))
              .map(([f, code]) => (
                <p key={f} role="alert" className="px-4 py-3 text-xs text-status-crit">
                  <span className="font-mono">{f}</span>：{fieldErrorText(f, code)}
                </p>
              ))}
            {failure?.kind === 'other' && (
              <p role="alert" className="px-4 py-3 text-xs text-status-crit">
                {failure.message}
              </p>
            )}
          </div>
        )}

        {step === 3 && (
          <div className="px-6 py-9 text-center">
            <div className="mx-auto mb-3.5 grid size-14 place-items-center rounded-full border-2 border-status-ok text-status-ok">
              <Check size={26} />
            </div>
            <h2 className="text-xl font-medium">{t('setup.done.title')}</h2>
            <p className="mt-1.5 text-ink-2">{t('setup.done.body')}</p>
            <div className="mt-[22px] mb-5 grid grid-cols-3 border border-border text-left mobile:grid-cols-1">
              <div className="px-3 py-2.5">
                <div className="text-[11.5px] text-muted-foreground">{t('setup.done.admin')}</div>
                <div className="mt-0.5 font-mono text-[13px]">{t('setup.done.adminValue', { strength: t(`setup.strength.${finishedStrength}`) })}</div>
              </div>
              <div className="border-l border-border px-3 py-2.5 mobile:border-t mobile:border-l-0">
                <div className="text-[11.5px] text-muted-foreground">{t('setup.done.langTz')}</div>
                <div className="mt-0.5 font-mono text-[13px]">
                  {language === 'zh' ? '中文' : 'English'} · {timezone}
                </div>
              </div>
              <div className="border-l border-border px-3 py-2.5 mobile:border-t mobile:border-l-0">
                <div className="text-[11.5px] text-muted-foreground">{t('setup.done.code')}</div>
                <div className="mt-0.5 font-mono text-[13px]">{t('setup.done.codeValue')}</div>
              </div>
            </div>
            <Button className="h-[38px] rounded-[2px] px-[18px]" onClick={() => void enter()}>
              <LayoutDashboard size={15} />
              {t('setup.done.enter')}
            </Button>
          </div>
        )}

        {step < 3 && (
          <div className="flex items-center gap-2 border-t border-border bg-panel-2 px-4 py-3">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="rounded-[2px]"
              disabled={step === 0 || busy}
              onClick={() => {
                setLocalError(null)
                setStep((step - 1) as StepIndex)
              }}
            >
              <ChevronLeft size={15} />
              {t('setup.prev')}
            </Button>
            <span className="font-mono text-[11px] text-muted-foreground">{t('setup.position', { n: step + 1 })}</span>
            <span className="flex-1" />
            <Button type="button" size="sm" className="rounded-[2px]" disabled={busy || (step === 0 && locked)} onClick={goNext}>
              {step === 2 ? (
                <>
                  {t('setup.finish')}
                  <Check size={15} />
                </>
              ) : (
                <>
                  {t('setup.next')}
                  <ChevronRight size={15} />
                </>
              )}
            </Button>
          </div>
        )}
      </section>
    </AuthShell>
  )
}
