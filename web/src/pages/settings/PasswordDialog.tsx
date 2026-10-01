import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import { minPasswordLength } from '@/pages/auth/password-strength'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { PasswordInput } from '@/ui/input'
import { FieldError, FieldHelp } from '@/ui/section'

interface Props {
  open: boolean
  onClose(): void
  onChanged(): void
}

export function PasswordDialog({ open, onClose, onChanged }: Props) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      {open && <PasswordBody onClose={onClose} onChanged={onChanged} />}
    </Dialog>
  )
}

function PasswordBody({ onClose, onChanged }: Pick<Props, 'onClose' | 'onChanged'>) {
  const { t, i18n } = useTranslation()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [errors, setErrors] = useState<{ current?: string; next?: string; confirm?: string; general?: string }>({})

  async function submit() {
    const e: typeof errors = {}
    if (current === '') e.current = t('settings.password.currentRequired')
    if (Array.from(next).length < minPasswordLength) e.next = t('settings.password.tooShort', { min: minPasswordLength })
    else if (next !== confirm) e.confirm = t('settings.password.mismatch')
    setErrors(e)
    if (Object.keys(e).length > 0) return
    setBusy(true)
    try {
      await http.put('/api/admin/password', { current_password: current, new_password: next })
      onChanged()
    } catch (err) {
      if (isApiError(err) && err.code === 'auth.invalid_password') {
        const remaining = typeof err.details.remaining === 'number' ? err.details.remaining : null
        setErrors({ current: remaining === null ? translateErrorValue(i18n, err) : t('settings.password.wrong', { remaining }) })
      } else if (isApiError(err) && err.code === 'validation.failed') {
        setErrors({ next: t('settings.password.tooShort', { min: minPasswordLength }) })
      } else {
        setErrors({ general: translateErrorValue(i18n, err) })
      }
    } finally {
      setBusy(false)
    }
  }

  const common = { showLabel: t('common.show'), hideLabel: t('common.hide') }
  return (
    <DialogContent
      title={t('settings.password.title')}
      footer={
        <>
          <Button variant="outline" className="rounded-[2px]" disabled={busy} onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button className="rounded-[2px]" disabled={busy} onClick={() => void submit()}>
            {t('settings.password.submit')}
          </Button>
        </>
      }
    >
      <form
        className="space-y-3.5"
        onSubmit={(ev) => {
          ev.preventDefault()
          void submit()
        }}
      >
        <div>
          <label htmlFor="pw-current" className="mb-1 block text-[13px] font-medium">
            {t('settings.password.current')}
          </label>
          <PasswordInput id="pw-current" autoComplete="current-password" value={current} invalid={!!errors.current} onChange={(e) => setCurrent(e.target.value)} {...common} />
          {errors.current && <FieldError>{errors.current}</FieldError>}
        </div>
        <div>
          <label htmlFor="pw-new" className="mb-1 block text-[13px] font-medium">
            {t('settings.password.new')}
          </label>
          <PasswordInput id="pw-new" autoComplete="new-password" value={next} invalid={!!errors.next} onChange={(e) => setNext(e.target.value)} {...common} />
          {errors.next ? <FieldError>{errors.next}</FieldError> : <FieldHelp>{t('settings.password.help', { min: minPasswordLength })}</FieldHelp>}
        </div>
        <div>
          <label htmlFor="pw-confirm" className="mb-1 block text-[13px] font-medium">
            {t('settings.password.confirm')}
          </label>
          <PasswordInput id="pw-confirm" autoComplete="new-password" value={confirm} invalid={!!errors.confirm} onChange={(e) => setConfirm(e.target.value)} {...common} />
          {errors.confirm && <FieldError>{errors.confirm}</FieldError>}
        </div>
        {errors.general && <FieldError>{errors.general}</FieldError>}
        <button type="submit" hidden />
      </form>
    </DialogContent>
  )
}
