import { AlertTriangle, Lock, LogIn } from 'lucide-react'
import { useCallback, useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useSearchParams } from 'react-router'
import { isApiError } from '@/api/errors'
import { http } from '@/api/client'
import { safeNext } from '@/app/guard'
import { useSession } from '@/app/session'
import { translateErrorValue } from '@/i18n/errors'
import { serverNow } from '@/store/live-store'
import { Button } from '@/ui/button'
import { PasswordInput } from '@/ui/input'
import { Note } from '@/ui/note'
import { AuthShell, hostLabel, isPlainHttp } from '../auth/AuthShell'
import { formatClockTime, formatRemaining, lockInfoOf, makeLock, useCountdown, type Lock as LockState } from '../auth/lock'

type Failure = { kind: 'password'; remaining: number | null } | { kind: 'origin' } | { kind: 'other'; message: string }

// 登录：单管理员只有密码框；错误按错误码给出剩余次数、锁定到期与来源 IP
export function LoginPage() {
  const { t, i18n } = useTranslation()
  const { refresh } = useSession()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [lock, setLock] = useState<LockState | null>(null)
  const errId = useId()

  const onExpire = useCallback(() => setLock(null), [])
  const remainingMs = useCountdown(lock, onExpire)
  const locked = lock !== null

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || locked) return
    setBusy(true)
    setFailure(null)
    try {
      await http.post('/api/login', { password })
      await refresh()
      navigate(safeNext(params.get('next')), { replace: true })
    } catch (err) {
      const info = lockInfoOf(err, serverNow())
      if (info) {
        setLock(makeLock(info, serverNow()))
        setPassword('')
      } else if (isApiError(err) && err.code === 'auth.invalid_password') {
        setFailure({ kind: 'password', remaining: typeof err.details.remaining === 'number' ? err.details.remaining : null })
      } else if (isApiError(err) && err.code === 'origin.mismatch') {
        setFailure({ kind: 'origin' })
      } else if (isApiError(err) && err.code === 'setup.required') {
        // 还没有管理员：刷新会话，由守卫送到首次设置页
        await refresh()
      } else {
        setFailure({ kind: 'other', message: translateErrorValue(i18n, err) })
      }
    } finally {
      setBusy(false)
    }
  }

  const passwordFailed = failure?.kind === 'password'

  return (
    <AuthShell topLabel="pimon-hub" title="PiMon" subtitle={hostLabel()} widthClass="max-w-[400px]">
      {isPlainHttp() && (
        <Note tone="warn" icon={<AlertTriangle size={16} />} className="mb-3">
          {t('login.httpWarning')}
        </Note>
      )}
      {failure?.kind === 'origin' && (
        <Note tone="crit" role="alert" icon={<AlertTriangle size={16} />} className="mb-3">
          {t('login.originMismatch')}
        </Note>
      )}
      <section className="border border-border bg-card">
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-2.5">
          <span className="rounded-[2px] border border-border px-1 font-mono text-[10.5px] leading-[15px] text-muted-foreground">L1</span>
          <h2 className="text-[13.5px] font-medium">{t('login.title')}</h2>
          <span className="ml-auto text-xs text-muted-foreground">{t('login.singleAdmin')}</span>
        </div>
        <form className="px-4 pt-[18px] pb-4" onSubmit={submit}>
          {lock && (
            <Note tone="crit" role="alert" icon={<Lock size={16} />} className="mb-3.5">
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
          <div className="mb-1.5 flex items-baseline gap-1.5">
            <label htmlFor="login-password" className="text-[13px]">
              {t('login.password')}
            </label>
            <span aria-hidden className="font-mono text-[11px] text-muted-foreground">
              password
            </span>
          </div>
          <PasswordInput
            id="login-password"
            className="h-[38px] text-sm"
            showLabel={t('common.show')}
            hideLabel={t('common.hide')}
            placeholder={t('login.passwordPlaceholder')}
            autoFocus
            autoComplete="current-password"
            value={password}
            disabled={locked}
            invalid={passwordFailed}
            aria-describedby={errId}
            onChange={(e) => setPassword(e.target.value)}
          />
          <div id={errId} role="alert" className="mt-1.5 text-xs text-status-crit">
            {failure?.kind === 'password' &&
              (failure.remaining !== null ? t('login.wrongPassword', { remaining: failure.remaining }) : t('errors.auth.invalid_password'))}
            {failure?.kind === 'other' && failure.message}
          </div>
          <Button type="submit" className="mt-3.5 h-[38px] w-full rounded-[2px]" disabled={busy || locked || password === ''}>
            <LogIn size={15} />
            {t('login.submit')}
          </Button>
          <div className="mt-3 flex justify-between font-mono text-[11px] text-muted-foreground">
            <span>{t('login.sessionHint')}</span>
            <span>{t('login.lockHint')}</span>
          </div>
        </form>
      </section>
      <p className="mt-[18px] text-center text-[12.5px] leading-[1.7] text-muted-foreground">
        {t('login.forgot')} <code className="font-mono text-foreground">pimon-hub reset-password</code>
      </p>
    </AuthShell>
  )
}
