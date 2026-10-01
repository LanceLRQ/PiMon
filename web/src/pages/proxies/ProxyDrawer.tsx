import { Play } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import type { Proxy, ProxyTestResult } from '@/types/generated'
import { Button } from '@/ui/button'
import { Checkbox } from '@/ui/checkbox'
import { Input, PasswordInput } from '@/ui/input'
import { Note } from '@/ui/note'
import { NumberTag } from '@/ui/numbered-label'
import { FieldError, FieldHelp } from '@/ui/section'
import { Segmented } from '@/ui/segmented'
import { Sheet, SheetContent } from '@/ui/sheet'
import { StatusShape } from '@/ui/status-shape'
import { Switch } from '@/ui/switch'
import {
  buildInput,
  defaultTestUrl,
  emptyForm,
  formDirty,
  formFromProxy,
  locations,
  portValid,
  remoteDnsApplies,
  schemes,
  type ProxyForm,
} from './model'

interface Props {
  // null 关闭；'new' 新建；否则编辑该代理
  target: Proxy | 'new' | null
  onClose(): void
  onSaved(proxy: Proxy, created: boolean): void
  onDelete(proxy: Proxy): void
  // 抽屉里的测试结果同步给列表的「最近测试」
  onTested(proxy: Proxy, result: ProxyTestResult): void
}

export function ProxyDrawer({ target, onClose, onSaved, onDelete, onTested }: Props) {
  const { t } = useTranslation()
  const proxy = target && target !== 'new' ? target : null
  const title = proxy ? t('proxies.drawer.titleEdit', { name: proxy.name }) : t('proxies.drawer.titleNew')
  return (
    <Sheet open={target !== null} onOpenChange={(o) => !o && onClose()}>
      {target !== null && (
        <DrawerBody
          // 切换目标时重建表单状态
          key={proxy?.id ?? 'new'}
          proxy={proxy}
          title={title}
          onClose={onClose}
          onSaved={onSaved}
          onDelete={onDelete}
          onTested={onTested}
        />
      )}
    </Sheet>
  )
}

function Row({ label, fieldKey, htmlFor, required, children }: { label: string; fieldKey: string; htmlFor?: string; required?: boolean; children: React.ReactNode }) {
  return (
    <div className="border-t border-border px-4 py-3 first:border-t-0">
      <div className="mb-1.5 flex items-baseline gap-2 text-[13px]">
        <label htmlFor={htmlFor} className="font-medium">
          {label}
          {required && <span className="ml-0.5 text-status-crit">*</span>}
        </label>
        <span className="font-mono text-[10.5px] text-muted-foreground">{fieldKey}</span>
      </div>
      {children}
    </div>
  )
}

function DrawerBody({ proxy, title, onClose, onSaved, onDelete, onTested }: { proxy: Proxy | null; title: string } & Omit<Props, 'target'>) {
  const { t, i18n } = useTranslation()
  const initial = proxy ? formFromProxy(proxy) : emptyForm()
  const [form, setForm] = useState<ProxyForm>(initial)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [testUrl, setTestUrl] = useState(defaultTestUrl)
  const [testing, setTesting] = useState(false)
  const [testOut, setTestOut] = useState<{ result: ProxyTestResult } | { error: string } | null>(null)
  const dirty = formDirty(form, initial)
  const authSet = proxy?.auth.set ?? false

  function patch(p: Partial<ProxyForm>, clear: string[] = []) {
    setForm((f) => ({ ...f, ...p }))
    if (clear.length > 0) {
      setFieldErrors((e) => {
        const n = { ...e }
        for (const k of clear) delete n[k]
        return n
      })
    }
  }

  function errorText(field: string): string | null {
    const code = fieldErrors[field]
    if (!code) return null
    for (const key of [`proxies.fieldError.${field}.${code}`, `proxies.fieldError.${code}`]) {
      if (i18n.exists(key)) return t(key)
    }
    return t('proxies.fieldError.invalid')
  }

  async function save() {
    setFailure(null)
    if (!portValid(form.port)) {
      setFieldErrors({ address: 'port' })
      return
    }
    setBusy(true)
    setFieldErrors({})
    try {
      const body = buildInput(form)
      const saved = proxy ? await http.put<Proxy>(`/api/proxies/${proxy.id}`, body) : await http.post<Proxy>('/api/proxies', body)
      onSaved(saved, proxy === null)
    } catch (e) {
      if (isApiError(e) && e.code === 'validation.failed') {
        setFieldErrors((e.details.fields ?? {}) as Record<string, string>)
      } else {
        setFailure(translateErrorValue(i18n, e))
      }
    } finally {
      setBusy(false)
    }
  }

  async function runTest() {
    if (!proxy) return
    setTesting(true)
    setTestOut(null)
    try {
      const body = testUrl.trim() !== '' && testUrl.trim() !== defaultTestUrl ? { url: testUrl.trim() } : undefined
      const result = await http.post<ProxyTestResult>(`/api/proxies/${proxy.id}/test`, body, { timeoutMs: 20_000 })
      setTestOut({ result })
      onTested(proxy, result)
    } catch (e) {
      if (isApiError(e) && e.code === 'validation.failed') setFieldErrors((prev) => ({ ...prev, ...((e.details.fields ?? {}) as Record<string, string>) }))
      else setTestOut({ error: translateErrorValue(i18n, e) })
    } finally {
      setTesting(false)
    }
  }

  const addressError = fieldErrors.address === 'port' ? t('proxies.portInvalid') : errorText('address')
  const dnsApplies = remoteDnsApplies(form.scheme)

  return (
    <SheetContent
      srTitle={title}
      header={
        <div className="flex items-baseline gap-2.5">
          <NumberTag no="04.3" />
          <h2 className="truncate text-[15px] font-medium">{title}</h2>
        </div>
      }
      footer={
        <>
          {proxy && (
            <Button variant="outline" className="mr-auto rounded-[2px] text-destructive" onClick={() => onDelete(proxy)}>
              {t('common.delete')}
            </Button>
          )}
          <Button variant="outline" className="rounded-[2px]" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button className="rounded-[2px]" disabled={busy || (proxy !== null && !dirty)} onClick={() => void save()}>
            {busy ? t('common.saving') : t('common.save')}
          </Button>
        </>
      }
    >
      {failure && (
        <div className="p-4 pb-0">
          <Note tone="crit" role="alert">{failure}</Note>
        </div>
      )}
      <Row label={t('proxies.drawer.name')} fieldKey="name" htmlFor="px-name" required>
        <Input id="px-name" value={form.name} invalid={!!errorText('name')} onChange={(e) => patch({ name: e.target.value }, ['name'])} />
        {errorText('name') && <FieldError>{errorText('name')}</FieldError>}
      </Row>
      <Row label={t('proxies.drawer.scheme')} fieldKey="scheme">
        <Segmented
          ariaLabel={t('proxies.drawer.scheme')}
          className="font-mono"
          value={form.scheme}
          onChange={(scheme) => patch({ scheme }, ['scheme'])}
          options={schemes.map((s) => ({ value: s, label: s, title: s }))}
        />
        {errorText('scheme') && <FieldError>{errorText('scheme')}</FieldError>}
      </Row>
      <Row label={t('proxies.drawer.address')} fieldKey="host : port" htmlFor="px-host" required>
        <div className="grid grid-cols-[minmax(0,1fr)_96px] gap-1.5">
          <Input
            id="px-host"
            className="font-mono"
            aria-label={t('proxies.drawer.host')}
            placeholder={t('proxies.drawer.hostPlaceholder')}
            value={form.host}
            invalid={!!addressError}
            onChange={(e) => patch({ host: e.target.value }, ['address'])}
          />
          <Input
            className="font-mono"
            inputMode="numeric"
            aria-label={t('proxies.drawer.port')}
            placeholder={t('proxies.drawer.portPlaceholder')}
            value={form.port}
            invalid={!!addressError}
            onChange={(e) => patch({ port: e.target.value }, ['address'])}
          />
        </div>
        {addressError && <FieldError>{addressError}</FieldError>}
      </Row>
      <Row label={t('proxies.drawer.username')} fieldKey="username" htmlFor="px-user">
        <Input
          id="px-user"
          className="font-mono"
          autoComplete="off"
          value={form.username}
          disabled={form.clearAuth}
          invalid={!!errorText('auth.username')}
          onChange={(e) => patch({ username: e.target.value }, ['auth.username', 'clear_auth'])}
        />
        {errorText('auth.username') && <FieldError>{errorText('auth.username')}</FieldError>}
      </Row>
      <Row label={t('proxies.drawer.password')} fieldKey="password · secret" htmlFor="px-pass">
        <PasswordInput
          id="px-pass"
          className="font-mono"
          autoComplete="new-password"
          showLabel={t('common.show')}
          hideLabel={t('common.hide')}
          placeholder={authSet ? t('proxies.drawer.passwordSet') : t('proxies.drawer.passwordOptional')}
          value={form.password}
          disabled={form.clearAuth}
          onChange={(e) => patch({ password: e.target.value }, ['auth.username', 'clear_auth'])}
        />
        <FieldHelp>{t('proxies.drawer.passwordHelp')}</FieldHelp>
        {authSet && (
          <label className="mt-2 flex cursor-pointer items-center gap-2 text-[12.5px]">
            <Checkbox checked={form.clearAuth} onChange={(clearAuth) => patch({ clearAuth, ...(clearAuth ? { username: '', password: '' } : {}) }, ['clear_auth'])} ariaLabel={t('proxies.drawer.clearAuth')} />
            {t('proxies.drawer.clearAuth')}
          </label>
        )}
        {errorText('clear_auth') && <FieldError>{errorText('clear_auth')}</FieldError>}
      </Row>
      <Row label={t('proxies.drawer.remoteDns')} fieldKey="remote_dns">
        <div className="flex items-center gap-2.5">
          <Switch
            checked={dnsApplies ? (form.scheme === 'socks5h' ? true : form.remoteDns) : false}
            disabled={form.scheme !== 'socks5'}
            onChange={(remoteDns) => patch({ remoteDns })}
            ariaLabel={t('proxies.drawer.remoteDns')}
          />
          <span className="text-[12.5px] text-ink-2">{t('proxies.drawer.remoteDnsLabel')}</span>
        </div>
        <FieldHelp>{t('proxies.drawer.remoteDnsHelp')}</FieldHelp>
      </Row>
      <Row label={t('proxies.drawer.location')} fieldKey="location">
        <Segmented
          ariaLabel={t('proxies.drawer.location')}
          value={form.location}
          onChange={(location) => patch({ location }, ['location'])}
          options={locations.map((l) => ({ value: l, label: t(`proxies.location.${l}`), title: t(`proxies.location.${l}`) }))}
        />
        {errorText('location') && <FieldError>{errorText('location')}</FieldError>}
      </Row>
      <Row label={t('proxies.drawer.refs')} fieldKey="references">
        {proxy && proxy.referrers.length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {proxy.referrers.map((r) => (
              <span key={r.id} className="rounded-[2px] border border-border px-1.5 py-0.5 font-mono text-[11px]">
                {r.name}
              </span>
            ))}
          </div>
        ) : (
          <span className="text-[12.5px] text-muted-foreground">{t('proxies.drawer.noRefs')}</span>
        )}
      </Row>
      <Row label={t('proxies.drawer.test')} fieldKey="test" htmlFor="px-test-url">
        {proxy ? (
          <>
            <div className="flex">
              <Input
                id="px-test-url"
                className="rounded-r-none font-mono"
                aria-label={t('proxies.drawer.testTarget')}
                value={testUrl}
                invalid={!!errorText('url')}
                onChange={(e) => {
                  setTestUrl(e.target.value)
                  setFieldErrors((prev) => {
                    const n = { ...prev }
                    delete n.url
                    return n
                  })
                }}
              />
              <Button variant="outline" className="rounded-[2px] rounded-l-none border-l-0" disabled={testing} onClick={() => void runTest()}>
                <Play size={13} />
                {testing ? t('proxies.test.running') : t('common.test')}
              </Button>
            </div>
            {errorText('url') && <FieldError>{errorText('url')}</FieldError>}
            <FieldHelp>{t('proxies.drawer.testHelp')}</FieldHelp>
            {testOut && (
              <div role="status" className="mt-2 flex items-center gap-2 font-mono text-[12px]">
                {'result' in testOut ? (
                  testOut.result.ok ? (
                    <>
                      <StatusShape state="ok" size={14} />
                      {t('proxies.test.ok', { status: testOut.result.status, ms: testOut.result.latency_ms })}
                    </>
                  ) : (
                    <>
                      <StatusShape state="error" size={14} />
                      <span className="min-w-0 break-words">{testOut.result.error || t('proxies.test.failed')}</span>
                    </>
                  )
                ) : (
                  <>
                    <StatusShape state="error" size={14} />
                    <span className="min-w-0 break-words">{testOut.error}</span>
                  </>
                )}
              </div>
            )}
          </>
        ) : (
          <span className="text-[12.5px] text-muted-foreground">{t('proxies.drawer.testNew')}</span>
        )}
      </Row>
    </SheetContent>
  )
}
