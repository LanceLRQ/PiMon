import { CircleArrowUp, Info, KeyRound, Trash2, TriangleAlert } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { setUnsavedGuardBypass, UnsavedGuard } from '@/app/unsaved-guard'
import { translateErrorValue } from '@/i18n/errors'
import { formatAgo, parseTime, useNow } from '@/lib/time'
import { formatUptime } from '@/lib/format'
import { selectInstances, useLiveStore } from '@/store/live-store'
import type { Device, Instance, Proxy } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogClose, DialogContent } from '@/ui/dialog'
import { Input } from '@/ui/input'
import { Note } from '@/ui/note'
import { FieldError, FieldHelp, FormRow } from '@/ui/section'
import { Select } from '@/ui/select'
import { Sheet, SheetContent } from '@/ui/sheet'
import { StatusShape } from '@/ui/status-shape'
import { Switch } from '@/ui/switch'
import { useToast } from '@/ui/toast'
import { CommandBlock } from '../add/CommandBlock'
import { deviceRoles, hubUsableProxies, nameTooLong } from '../add/pairing'
import { incompatibleText, needsUpgrade, presenceView } from '../model'
import { DeviceLogs } from './DeviceLogs'
import { InstanceRows } from './InstanceRows'
import { DrawerSection, Kv, type KvRow } from './parts'
import { RepairSection } from './RepairSection'
import { upgradeCommand } from './upgrade'

interface Fields {
  name: string
  role: string
  allow_offline: boolean
  maintenance: boolean
  // 拨出设备的地址（原样，规范化交服务端）与代理，拨入设备恒为空串
  address: string
  proxy_id: string
}

function fieldsOf(d: Device): Fields {
  const out = d.direction === 'dial_out'
  return {
    name: d.name,
    role: d.role,
    allow_offline: d.allow_offline,
    maintenance: d.maintenance,
    address: out ? d.address : '',
    proxy_id: out ? d.proxy_id : '',
  }
}

const editableKeys = (d: Device): (keyof Fields)[] =>
  d.direction === 'dial_out' ? ['name', 'role', 'allow_offline', 'maintenance', 'address', 'proxy_id'] : ['name', 'role', 'allow_offline', 'maintenance']

interface DeviceDetailProps {
  device: Device
  hubVersion: string | null
  onClose(): void
  onRevoke(device: Device): void
  // 发起重新配对前通知页面（旧设备被接替消失时不当作「不存在」）
  onRepairStart(deviceId: string, direction: 'dial_in' | 'dial_out'): void
  // 拨出重新配对成功，参数为接替它的新设备
  onRepaired(device: Device): void
  onViewDetail(id: string): void
  // 从列表菜单进入时直接滚到重新配对分区
  focusRepair?: boolean
}

// 设备详情：属性可编辑（保存才生效，有未保存改动时关闭会先确认）、连接信息、版本与升级命令、该设备上的实例与日志。
export function DeviceDetail({ device: live, hubVersion, onClose, onRevoke, onRepairStart, onRepaired, onViewDetail, focusRepair }: DeviceDetailProps) {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const now = useNow()
  const allInstances = useLiveStore(selectInstances)
  // 保存成功后实时推送可能还没到：取更新时间较新的一份
  const [saved, setSaved] = useState<Device | null>(null)
  const dev = saved && Date.parse(saved.updated_at) > Date.parse(live.updated_at) ? saved : live
  const [draft, setDraft] = useState<Partial<Fields>>({})
  const [busy, setBusy] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [mismatch, setMismatch] = useState<string[] | null>(null)
  const [otherError, setOtherError] = useState<string | null>(null)
  const [confirmClose, setConfirmClose] = useState(false)
  const [proxies, setProxies] = useState<Proxy[]>([])
  const dialOut = dev.direction === 'dial_out'
  const repairRef = useRef<HTMLDivElement>(null)
  const scrollToRepair = () => repairRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
  useEffect(() => {
    if (focusRepair) repairRef.current?.scrollIntoView?.({ block: 'start' })
  }, [focusRepair])

  useEffect(() => {
    if (!dialOut) return
    let alive = true
    http.get<Proxy[]>('/api/proxies').then(
      (list) => alive && setProxies(list),
      () => {},
    )
    return () => {
      alive = false
    }
  }, [dialOut])

  const base = useMemo(() => fieldsOf(dev), [dev])
  const values: Fields = { ...base, ...draft }
  const changed = editableKeys(dev).filter((k) => values[k] !== base[k])
  const dirty = changed.length > 0
  const isChanged = (k: keyof Fields) => changed.includes(k)

  function set<K extends keyof Fields>(k: K, v: Fields[K]) {
    setDraft((prev) => {
      const next = { ...prev, [k]: v }
      if (v === base[k]) delete next[k]
      return next
    })
    setFieldErrors((e) => {
      if (!(k in e)) return e
      const n = { ...e }
      delete n[k]
      return n
    })
    setMismatch(null)
    setOtherError(null)
  }

  // 关闭：干净状态直接关；有未保存改动先确认。放弃后的跳转要绕开离开保护，不再弹第二次。
  function close() {
    setUnsavedGuardBypass(true)
    try {
      onClose()
    } finally {
      setUnsavedGuardBypass(false)
    }
  }
  function requestClose() {
    if (dirty) setConfirmClose(true)
    else close()
  }

  const nameEmpty = values.name.trim() === ''
  const nameLong = nameTooLong(values.name)
  const nameError = nameEmpty ? t('devices.detail.errName') : nameLong ? t('devices.add.fieldError.name.out_of_range') : undefined
  const canSave = dirty && !busy && !nameError

  async function save() {
    if (!canSave) return
    setBusy(true)
    setFieldErrors({})
    setMismatch(null)
    setOtherError(null)
    try {
      const body = {
        name: values.name.trim(),
        role: values.role,
        allow_offline: values.allow_offline,
        maintenance: values.maintenance,
        // 只提交改动过的连接字段：代理已被删除时，只改名称不应被「代理不存在」拒绝
        ...(dialOut && isChanged('address') ? { address: values.address.trim() } : {}),
        ...(dialOut && isChanged('proxy_id') ? { proxy_id: values.proxy_id } : {}),
      }
      const next = await http.put<Device>(`/api/devices/${dev.id}`, body)
      setSaved(next)
      setDraft({})
      toast.show(t('devices.detail.saved', { name: next.name }))
    } catch (e) {
      if (isApiError(e) && e.code === 'validation.failed') {
        setFieldErrors((e.details.fields ?? {}) as Record<string, string>)
      } else if (isApiError(e) && e.code === 'proxy.location_mismatch' && Array.isArray(e.details.instances)) {
        setMismatch((e.details.instances as { name?: string; id?: string }[]).map((i) => i.name || i.id || ''))
      } else {
        setOtherError(translateErrorValue(i18n, e))
      }
    } finally {
      setBusy(false)
    }
  }
  const roleLabel = (r: string) => (i18n.exists(`devices.list.role.${r}`) ? t(`devices.list.role.${r}`) : r)
  const roleOptions = (deviceRoles as readonly string[]).includes(values.role) ? [...deviceRoles] : [values.role, ...deviceRoles]
  const errorText = (field: string): string | undefined => {
    const c = fieldErrors[field]
    if (!c) return undefined
    for (const key of [`devices.add.fieldError.${field}.${c}`, `devices.add.fieldError.${c}`]) {
      if (i18n.exists(key)) return t(key)
    }
    return t('devices.add.fieldError.invalid')
  }

  const presence = presenceView(dev)
  const seen = parseTime(dev.last_seen_at)
  const online = dev.presence === 'online'
  const upgrade = needsUpgrade(dev, hubVersion)
  const hubProxies = hubUsableProxies(proxies)
  const proxyOptions = hubProxies.some((p) => p.id === values.proxy_id) || values.proxy_id === '' ? hubProxies : [...hubProxies, { id: values.proxy_id, name: t('devices.detail.proxyMissing', { id: values.proxy_id }) } as Proxy]
  const currentProxy = proxies.find((p) => p.id === dev.proxy_id) ?? null
  const instances: Instance[] = allInstances.filter((i) => i.runs_on === dev.id)
  const cmd = upgradeCommand(dev, hubVersion, currentProxy)
  const dash = '—'

  const connRows: KvRow[] = [
    {
      label: t('devices.detail.conn.status'),
      value: (
        <span className="inline-flex items-center gap-1.5 font-sans text-[12.5px]">
          <StatusShape state={presence.shape} />
          {t(presence.labelKey)}
        </span>
      ),
    },
    { label: t('devices.detail.conn.direction'), value: t(dialOut ? 'devices.detail.conn.dirOut' : 'devices.detail.conn.dirIn') },
    {
      label: t('devices.detail.conn.address'),
      value: dev.address ? (dialOut ? dev.address : `${dev.address} ${t('devices.detail.conn.addressIn')}`) : dash,
    },
    { label: t('devices.detail.conn.hostname'), value: dev.hostname || dash },
    { label: t('devices.detail.conn.platform'), value: dev.os ? `${dev.os}/${dev.arch}` : dash },
    online && seen !== null
      ? { label: t('devices.detail.conn.connectedFor'), value: formatUptime(t, (now - seen) / 1000) }
      : { label: t('devices.detail.conn.lastSeen'), value: seen === null ? t('devices.list.neverSeen') : formatAgo(t, now, seen) },
    {
      label: t('devices.detail.conn.skew'),
      value: dev.clock_skew_ms === null ? dash : `${dev.clock_skew_ms > 0 ? '+' : ''}${dev.clock_skew_ms} ms`,
    },
    { label: t('devices.detail.conn.protocol'), value: dev.protocol_version > 0 ? String(dev.protocol_version) : dash },
  ]
  if (dialOut) {
    connRows.push({
      label: t('devices.detail.conn.proxy'),
      value: dev.proxy_id ? (currentProxy?.name ?? dev.proxy_id) : t('devices.list.dir.direct'),
    })
  }

  let upgradeBody
  if (dev.incompatible?.reason === 'version_too_new') {
    upgradeBody = (
      <>
        <Kv rows={[{ label: t('devices.detail.up.agentVersion'), value: dev.agent_version || dash }, { label: t('devices.detail.up.hubVersion'), value: hubVersion ?? dash }]} />
        <div className="px-4 pb-3"><Note icon={<Info size={16} />}>{t('devices.detail.up.tooNew')}</Note></div>
      </>
    )
  } else if (!dev.incompatible && !dev.agent_version) {
    upgradeBody = <div className="px-4 pb-3 text-[12.5px] text-muted-foreground">{t('devices.detail.up.pending')}</div>
  } else {
    upgradeBody = (
      <>
        <Kv
          rows={[
            {
              label: t('devices.detail.up.agentVersion'),
              value: (
                <span className="inline-flex flex-wrap items-center justify-end gap-1.5">
                  <span>{dev.agent_version || dash}</span>
                  {upgrade ? (
                    <span className="inline-flex items-center gap-1 font-sans text-[12px] text-status-warn">
                      <CircleArrowUp size={12} aria-hidden="true" />
                      {t(upgrade.level === 'required' ? `devices.upgrade.${upgrade.target}` : 'devices.upgrade.differs')}
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 font-sans text-[12px] text-status-ok">
                      <StatusShape state="ok" size={12} />
                      {t('devices.detail.up.same')}
                    </span>
                  )}
                </span>
              ),
            },
            { label: t('devices.detail.up.hubVersion'), value: hubVersion ?? dash },
          ]}
        />
        <div className="px-4 pb-3">
          {cmd.kind === 'command' ? (
            <>
              <CommandBlock title={t(dialOut ? 'devices.detail.up.cmdOut' : 'devices.detail.up.cmdIn')} text={cmd.text} />
              {dialOut && <FieldHelp>{t('devices.detail.up.outHelp', { version: hubVersion })}</FieldHelp>}
              {cmd.proxyHint && <FieldHelp>{t('devices.detail.up.proxyHint', { url: cmd.proxyHint })}</FieldHelp>}
            </>
          ) : (
            <Note tone="warn" icon={<TriangleAlert size={16} />}>{t('devices.detail.up.noRelease')}</Note>
          )}
        </div>
      </>
    )
  }

  const prop = (key: keyof Fields) => ({ changed: isChanged(key), changedLabel: t('devices.detail.changed') })

  return (
    <Sheet open onOpenChange={(o) => !o && requestClose()}>
      <UnsavedGuard dirty={dirty} textKey="devices.detail.unsaved" />
      <SheetContent
        className="w-[560px] mobile:w-full"
        srTitle={t('devices.detail.srTitle', { name: dev.name })}
        header={
          <div className="flex items-center gap-2.5">
            <StatusShape state={presence.shape} size={18} />
            <h2 className="min-w-0 truncate text-[15px] font-medium">{t('devices.detail.title', { name: dev.name })}</h2>
            {dirty && (
              <span className="ml-auto inline-flex shrink-0 items-center gap-1.5 font-mono text-[11px] text-signal-text">
                <span aria-hidden className="size-1.5 rounded-full bg-signal" />
                {t('devices.detail.dirty', { count: changed.length })}
              </span>
            )}
          </div>
        }
        footer={
          <>
            <Button
              variant="outline"
              size="sm"
              className="mr-auto rounded-[2px] text-status-crit"
              onClick={() => onRevoke(dev)}
            >
              <Trash2 /> {t('devices.detail.revoke')}
            </Button>
            {dirty && (
              <Button variant="ghost" size="sm" className="rounded-[2px]" disabled={busy} onClick={() => { setDraft({}); setFieldErrors({}); setMismatch(null); setOtherError(null) }}>
                {t('devices.detail.revert')}
              </Button>
            )}
            <Button variant="outline" size="sm" className="rounded-[2px]" onClick={requestClose}>
              {t('common.cancel')}
            </Button>
            <Button size="sm" className="rounded-[2px]" disabled={!canSave} onClick={() => void save()}>
              {busy ? t('common.saving') : t('common.save')}
            </Button>
          </>
        }
      >
        {dev.incompatible && (
          <div className="px-4 pt-3">
            <Note tone="crit" role="alert" icon={<TriangleAlert size={16} />}>
              <b>{t(`devices.upgrade.${dev.incompatible.reason === 'version_too_new' ? 'hub' : 'agent'}`)}</b>
              {'：'}
              {incompatibleText(i18n, dev.incompatible.reason)}
              {t('devices.detail.incompatibleMin', { min: dev.incompatible.required_min })}
            </Note>
          </div>
        )}

        <DrawerSection no="01" title={t('devices.detail.props')} meta={t('devices.detail.editable')}>
          <FormRow label={t('devices.add.in.name')} fieldKey="name" htmlFor="dd-name" {...prop('name')}>
            <Input id="dd-name" value={values.name} invalid={!!nameError || !!errorText('name')} onChange={(e) => set('name', e.target.value)} />
            {(nameError ?? errorText('name')) && <FieldError>{nameError ?? errorText('name')}</FieldError>}
          </FormRow>
          <FormRow label={t('devices.add.in.role')} fieldKey="role" htmlFor="dd-role" {...prop('role')}>
            <Select id="dd-role" className="max-w-[260px]" value={values.role} invalid={!!errorText('role')} onChange={(e) => set('role', e.target.value)}>
              {roleOptions.map((r) => (
                <option key={r} value={r}>{t('devices.add.roleOption', { label: roleLabel(r), key: r })}</option>
              ))}
            </Select>
            {errorText('role') && <FieldError>{errorText('role')}</FieldError>}
          </FormRow>
          <FormRow label={t('devices.add.in.allowOffline')} fieldKey="allow_offline" {...prop('allow_offline')}>
            <div className="flex items-center justify-between gap-3">
              <span className="text-[12px] text-muted-foreground">{t('devices.detail.allowOfflineHint')}</span>
              <Switch checked={values.allow_offline} ariaLabel={t('devices.add.in.allowOffline')} onChange={(v) => set('allow_offline', v)} />
            </div>
          </FormRow>
          <FormRow label={t('devices.detail.maintenance')} fieldKey="maintenance" {...prop('maintenance')}>
            <div className="flex items-center justify-between gap-3">
              <span className="text-[12px] text-muted-foreground">{t('devices.detail.maintenanceHint')}</span>
              <Switch checked={values.maintenance} ariaLabel={t('devices.detail.maintenance')} onChange={(v) => set('maintenance', v)} />
            </div>
          </FormRow>
          {dialOut && (
            <>
              <FormRow label={t('devices.add.out.address')} fieldKey="address" htmlFor="dd-address" {...prop('address')}>
                <Input
                  id="dd-address"
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={t('devices.detail.addressPlaceholder')}
                  value={values.address}
                  invalid={!!errorText('address')}
                  onChange={(e) => set('address', e.target.value)}
                />
                {errorText('address') ? <FieldError>{errorText('address')}</FieldError> : <FieldHelp>{t('devices.detail.addressHelp')}</FieldHelp>}
              </FormRow>
              <FormRow label={t('devices.add.out.proxy')} fieldKey="proxy_id" htmlFor="dd-proxy" {...prop('proxy_id')}>
                <Select id="dd-proxy" className="max-w-[260px]" value={values.proxy_id} invalid={!!errorText('proxy_id')} onChange={(e) => set('proxy_id', e.target.value)}>
                  <option value="">{t('devices.add.out.proxyDirect')}</option>
                  {proxyOptions.map((p) => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </Select>
                {errorText('proxy_id') && <FieldError>{errorText('proxy_id')}</FieldError>}
              </FormRow>
            </>
          )}
          {(mismatch || otherError) && (
            <div className="border-t border-border px-4 py-3">
              <Note tone="crit" role="alert" icon={<TriangleAlert size={16} />}>
                {mismatch ? t('devices.detail.mismatch', { names: mismatch.join('、') }) : otherError}
              </Note>
            </div>
          )}
        </DrawerSection>

        <DrawerSection no="02" title={t('devices.detail.connection')} meta={dev.presence === 'pending' ? t('devices.detail.notHandshaken') : undefined}>
          <Kv rows={connRows} />
          {presence.needsRepair && (
            <div className="flex flex-wrap items-center gap-2 px-4 pb-3">
              <Note tone="warn" icon={<TriangleAlert size={16} />} className="flex-1">{t('devices.needsRepair')}</Note>
            </div>
          )}
          <div className="px-4 pb-3">
            <Button variant="outline" size="sm" className="rounded-[2px]" onClick={scrollToRepair}>
              <KeyRound /> {t('devices.detail.repair')}
            </Button>
          </div>
        </DrawerSection>

        <DrawerSection no="03" title={t('devices.detail.version')} meta={t(dialOut ? 'devices.detail.dialOutMeta' : 'devices.detail.dialInMeta')}>
          {upgradeBody}
        </DrawerSection>

        <DrawerSection
          no="04"
          title={t('devices.detail.instances')}
          meta={
            <>
              {t('devices.detail.instanceCount', { count: instances.length })} ·{' '}
              <Link to="/instances" className="underline underline-offset-2">{t('devices.detail.viewInstances')}</Link>
            </>
          }
        >
          <InstanceRows instances={instances} pendingHint={dev.presence === 'pending'} />
        </DrawerSection>

        <DrawerSection no="05" title={t('devices.detail.logs')} meta={t('devices.detail.logsMeta')}>
          <DeviceLogs device={dev} />
        </DrawerSection>

        <div ref={repairRef}>
          <RepairSection device={dev} onStart={onRepairStart} onPaired={onRepaired} onViewDetail={onViewDetail} />
        </div>
      </SheetContent>

      <Dialog open={confirmClose} onOpenChange={setConfirmClose}>
        <DialogContent
          tag="!"
          tagTone="crit"
          title={t('devices.detail.unsaved.title')}
          footer={
            <>
              <DialogClose asChild>
                <Button variant="outline" className="rounded-[2px]">{t('devices.detail.unsaved.stay')}</Button>
              </DialogClose>
              <Button variant="destructive" className="rounded-[2px]" onClick={() => { setConfirmClose(false); close() }}>
                {t('devices.detail.unsaved.leave')}
              </Button>
            </>
          }
        >
          <p>{t('devices.detail.unsaved.body')}</p>
        </DialogContent>
      </Dialog>
    </Sheet>
  )
}
