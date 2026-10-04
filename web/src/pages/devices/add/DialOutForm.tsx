import { CircleCheck, TriangleAlert } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import type { Device, DialOutRequest, Proxy } from '@/types/generated'
import { Button } from '@/ui/button'
import { Input } from '@/ui/input'
import { Note } from '@/ui/note'
import { FieldError, FieldHelp, FormRow } from '@/ui/section'
import { Select } from '@/ui/select'
import { Switch } from '@/ui/switch'
import { deviceRoles, failTipCounts, hubUsableProxies, nameTooLong } from './pairing'

interface DialOutFormProps {
  // add：添加设备，带名称、角色与自动建实例；repair：重新配对，沿用旧设备的这几项，只填连接信息
  mode: 'add' | 'repair'
  // 重新配对时预填的地址、代理等
  initial?: Partial<DialOutRequest>
  // 提交；缺省为 POST /api/devices/dial-out，重新配对由调用方换成对应接口
  submit?: (req: DialOutRequest) => Promise<Device>
  onViewDetail: (id: string) => void
}

type Failure = { reason: string; code: string; deviceId: string }

// 拨出（VPS）配对表单：地址、指纹、配对码、代理，提交后显示进度；失败按 pairing.failed 的原因给指引。
export function DialOutForm({ mode, initial, submit, onViewDetail }: DialOutFormProps) {
  const { t, i18n } = useTranslation()
  const [address, setAddress] = useState(initial?.address ?? '')
  const [fingerprint, setFingerprint] = useState(initial?.fingerprint ?? '')
  const [code, setCode] = useState('')
  const [proxyId, setProxyId] = useState(initial?.proxy_id ?? '')
  const [name, setName] = useState(initial?.name ?? '')
  const [role, setRole] = useState(initial?.role ?? 'vps')
  const [auto, setAuto] = useState(initial?.auto_host_metrics ?? true)
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [paired, setPaired] = useState<Device | null>(null)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [otherError, setOtherError] = useState<string | null>(null)
  const failureRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let live = true
    http.get<Proxy[]>('/api/proxies').then(
      (list) => live && setProxies(hubUsableProxies(list)),
      () => {},
    )
    return () => {
      live = false
    }
  }, [])

  // 失败提示在表单下方，长对话框里要滚到可见处
  useEffect(() => {
    if (failure) failureRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [failure])

  const roleLabel = (r: string) => (i18n.exists(`devices.list.role.${r}`) ? t(`devices.list.role.${r}`) : r)
  const errorText = (field: string): string | undefined => {
    const c = fieldErrors[field]
    if (!c) return undefined
    for (const key of [`devices.add.fieldError.${field}.${c}`, `devices.add.fieldError.${c}`]) {
      if (i18n.exists(key)) return t(key)
    }
    return t('devices.add.fieldError.invalid')
  }
  const clear = (...fields: string[]) =>
    setFieldErrors((e) => {
      if (!fields.some((f) => f in e)) return e
      const n = { ...e }
      for (const f of fields) delete n[f]
      return n
    })

  async function onSubmit() {
    const errs: Record<string, string> = {}
    if (!address.trim()) errs.address = 'required'
    if (!fingerprint.trim()) errs.fingerprint = 'required'
    if (!code.trim()) errs.code = 'required'
    if (mode === 'add' && nameTooLong(name)) errs.name = 'out_of_range'
    setFieldErrors(errs)
    setFailure(null)
    setOtherError(null)
    if (Object.keys(errs).length > 0) return
    const req: DialOutRequest = {
      address: address.trim(),
      fingerprint: fingerprint.trim(),
      code: code.trim(),
      proxy_id: proxyId,
      name: name.trim(),
      role,
      ...(mode === 'add' ? { auto_host_metrics: auto } : {}),
    }
    setBusy(true)
    try {
      const dev = submit ? await submit(req) : await http.post<Device>('/api/devices/dial-out', req)
      setPaired(dev)
    } catch (e) {
      if (isApiError(e) && e.code === 'validation.failed') {
        setFieldErrors((e.details.fields ?? {}) as Record<string, string>)
      } else if (isApiError(e) && e.code === 'pairing.failed') {
        setFailure({
          reason: String(e.details.reason ?? 'unknown'),
          code: e.code,
          deviceId: typeof e.details.device_id === 'string' ? e.details.device_id : '',
        })
      } else {
        setOtherError(translateErrorValue(i18n, e))
      }
    } finally {
      setBusy(false)
    }
  }

  if (paired) {
    return (
      <div className="mx-4 my-3 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-[2px] border border-status-ok px-3 py-2 text-[13px]">
        <span className="inline-flex items-center gap-1.5 font-medium text-status-ok"><CircleCheck size={14} aria-hidden="true" />{t('devices.add.out.success')}</span>
        <span className="min-w-0 flex-1 break-all">{t('devices.add.out.successBody', { name: paired.name })}</span>
        <Button size="sm" className="rounded-[2px]" onClick={() => onViewDetail(paired.id)}>{t('devices.add.out.view')}</Button>
      </div>
    )
  }

  const reason = failure?.reason ?? ''
  const tipReason = reason in failTipCounts ? reason : 'unknown'
  const proxyOptions = proxies.map((p) => (
    <option key={p.id} value={p.id}>{`${p.name} · ${p.scheme} ${p.address}`}</option>
  ))

  return (
    <form
      aria-label={t('devices.add.out.step2')}
      onSubmit={(e) => {
        e.preventDefault()
        void onSubmit()
      }}
    >
      <FormRow label={t('devices.add.out.address')} fieldKey="address" htmlFor="out-address" required>
        <Input
          id="out-address"
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          placeholder={t('devices.add.out.addressPlaceholder')}
          value={address}
          invalid={!!errorText('address')}
          disabled={busy}
          onChange={(e) => { setAddress(e.target.value); clear('address') }}
        />
        {errorText('address') ? <FieldError>{errorText('address')}</FieldError> : <FieldHelp>{t('devices.add.out.addressHelp')}</FieldHelp>}
      </FormRow>
      <FormRow label={t('devices.add.out.fingerprint')} fieldKey="fingerprint" htmlFor="out-fp" required>
        <Input
          id="out-fp"
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          placeholder="SHA256:…"
          value={fingerprint}
          invalid={!!errorText('fingerprint')}
          disabled={busy}
          onChange={(e) => { setFingerprint(e.target.value); clear('fingerprint') }}
        />
        {errorText('fingerprint') ? <FieldError>{errorText('fingerprint')}</FieldError> : <FieldHelp>{t('devices.add.out.fingerprintHelp')}</FieldHelp>}
      </FormRow>
      <FormRow label={t('devices.add.out.code')} fieldKey="code" htmlFor="out-code" required>
        <Input
          id="out-code"
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          value={code}
          invalid={!!errorText('code')}
          disabled={busy}
          onChange={(e) => { setCode(e.target.value); clear('code') }}
        />
        {errorText('code') ? <FieldError>{errorText('code')}</FieldError> : <FieldHelp>{t('devices.add.out.codeHelp')}</FieldHelp>}
      </FormRow>
      <FormRow label={t('devices.add.out.proxy')} fieldKey="proxy_id" htmlFor="out-proxy">
        <Select id="out-proxy" value={proxyId} disabled={busy} invalid={!!errorText('proxy_id')} onChange={(e) => { setProxyId(e.target.value); clear('proxy_id') }}>
          <option value="">{t('devices.add.out.proxyDirect')}</option>
          {proxyOptions}
        </Select>
        {errorText('proxy_id') ? <FieldError>{errorText('proxy_id')}</FieldError> : <FieldHelp>{t('devices.add.out.proxyHelp')}</FieldHelp>}
      </FormRow>
      {mode === 'add' && (
        <>
          <FormRow label={t('devices.add.out.name')} fieldKey="name" htmlFor="out-name">
            <Input
              id="out-name"
              placeholder={t('devices.add.out.namePlaceholder')}
              value={name}
              invalid={!!errorText('name')}
              disabled={busy}
              onChange={(e) => { setName(e.target.value); clear('name') }}
            />
            {errorText('name') ? <FieldError>{errorText('name')}</FieldError> : <FieldHelp>{t('devices.add.in.nameHelp')}</FieldHelp>}
          </FormRow>
          <FormRow label={t('devices.add.out.role')} fieldKey="role" htmlFor="out-role">
            <Select id="out-role" className="max-w-[260px]" value={role} disabled={busy} invalid={!!errorText('role')} onChange={(e) => { setRole(e.target.value); clear('role') }}>
              {deviceRoles.map((r) => <option key={r} value={r}>{t('devices.add.roleOption', { label: roleLabel(r), key: r })}</option>)}
            </Select>
            {errorText('role') && <FieldError>{errorText('role')}</FieldError>}
          </FormRow>
          <FormRow label={t('devices.add.out.autoMetrics')} fieldKey="auto_host_metrics">
            <div className="flex items-center justify-between gap-3">
              <span className="text-[12.5px] text-muted-foreground">{t('devices.add.out.autoMetricsHint')}</span>
              <Switch checked={auto} disabled={busy} onChange={setAuto} ariaLabel={t('devices.add.out.autoMetrics')} />
            </div>
          </FormRow>
        </>
      )}

      {busy && (
        <Note tone="info" role="status" className="mx-4 mb-3">
          <b>{t('devices.add.out.progress')}</b>
          <div className="mt-0.5 text-muted-foreground">{t('devices.add.out.progressSteps')}</div>
        </Note>
      )}
      {failure && (
        <div ref={failureRef}>
          <Note tone="crit" role="alert" icon={<TriangleAlert size={15} />} className="mx-4 mb-3">
            <b>{t('devices.add.fail.title')}</b>{' '}
            <span className="font-mono text-[12px] text-muted-foreground">{reason}</span>
            <div className="mt-0.5">{t(`errors.pairing.reason.${tipReason}`)}</div>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-[12.5px]">
              {Array.from({ length: failTipCounts[tipReason] }, (_, i) => (
                <li key={i}>{t(`devices.add.fail.${tipReason}.tip${i + 1}`)}</li>
              ))}
            </ul>
            {failure.deviceId && (
              <Button variant="outline" size="sm" className="mt-2 rounded-[2px]" onClick={() => onViewDetail(failure.deviceId)}>
                {t('devices.add.fail.view')}
              </Button>
            )}
          </Note>
        </div>
      )}
      {otherError && <Note tone="crit" role="alert" className="mx-4 mb-3">{otherError}</Note>}

      <div className="sticky bottom-0 flex justify-end gap-2 border-t border-border bg-panel-2 px-4 py-3">
        <Button type="submit" size="sm" className="rounded-[2px]" disabled={busy}>
          {busy ? t('devices.add.out.submitting') : t('devices.add.out.submit')}
        </Button>
      </div>
    </form>
  )
}
