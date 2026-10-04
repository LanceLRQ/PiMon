import { Cable, Cloud, Info, KeyRound } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { parseTime, useNow } from '@/lib/time'
import { cn } from '@/lib/utils'
import { selectDevices, serverNow, useLiveStore } from '@/store/live-store'
import type { AgentInfo, PairingRequest, PairingResult } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Input } from '@/ui/input'
import { Note } from '@/ui/note'
import { FieldError, FieldHelp, FormRow } from '@/ui/section'
import { Select } from '@/ui/select'
import { Switch } from '@/ui/switch'
import { CommandBlock } from './CommandBlock'
import { DialOutForm } from './DialOutForm'
import { PairingCommandPanel, SectionHead } from './PairingCommandPanel'
import {
  agentListenPort, defaultAllowOffline, deviceRoles, findPairedDevice, listenInstallCommand, nameTooLong, pairingCodeTtlMs, releaseDownloadCommand,
} from './pairing'

interface AddDeviceDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  // 配对成功后「查看设备详情」：对话框已关闭，由调用方定位到该设备
  onViewDetail: (id: string) => void
}

type Tab = 'in' | 'out'

export function AddDeviceDialog({ open, onOpenChange, onViewDetail }: AddDeviceDialogProps) {
  const { t } = useTranslation()
  const viewDetail = (id: string) => {
    onOpenChange(false)
    onViewDetail(id)
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        title={t('devices.list.addTitle')}
        tag="07.2"
        description={t('devices.add.description')}
        className="w-[min(760px,calc(100vw-32px))] mobile:top-0 mobile:left-0 mobile:h-dvh mobile:max-h-none mobile:w-screen mobile:translate-x-0 mobile:translate-y-0 mobile:rounded-none"
      >
        <AddDeviceBody onViewDetail={viewDetail} />
      </DialogContent>
    </Dialog>
  )
}

// 对话框内容在打开时才挂载：中枢接入信息此时才读取
function AddDeviceBody({ onViewDetail }: { onViewDetail: (id: string) => void }) {
  const { t } = useTranslation()
  const agent = useAgentInfo()
  const [tab, setTab] = useState<Tab>('in')
  const tabs: { id: Tab; icon: typeof Cable; label: string }[] = [
    { id: 'in', icon: Cable, label: t('devices.add.tabIn') },
    { id: 'out', icon: Cloud, label: t('devices.add.tabOut') },
  ]
  return (
    <div className="-mx-4 -my-3.5">
      <div role="tablist" aria-label={t('devices.add.tabsLabel')} className="flex border-b border-border">
        {tabs.map((x) => (
          <button
            key={x.id}
            type="button"
            role="tab"
            aria-selected={tab === x.id}
            onClick={() => setTab(x.id)}
            className={cn(
              'flex min-w-0 flex-1 items-center justify-center gap-1.5 px-3 py-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
              tab === x.id ? 'border-b-2 border-foreground font-medium' : 'text-muted-foreground hover:bg-panel-2',
            )}
          >
            <x.icon size={15} aria-hidden="true" className="shrink-0" />
            <span className="truncate">{x.label}</span>
          </button>
        ))}
      </div>
      {/* 两个标签都保持挂载，切换不丢失已填内容与已生成的配对码 */}
      <div role="tabpanel" hidden={tab !== 'in'}><DialInPane info={agent.info} onViewDetail={onViewDetail} /></div>
      <div role="tabpanel" hidden={tab !== 'out'}><DialOutPane info={agent.info} error={agent.error} onViewDetail={onViewDetail} /></div>
    </div>
  )
}

// 读取中枢接入信息（内嵌平台、Release 下载模板）；取不到时命令面板仍可用，只是不提示内嵌情况
function useAgentInfo(): { info: AgentInfo | null; error: string | null } {
  const { i18n } = useTranslation()
  const [state, setState] = useState<{ info: AgentInfo | null; error: string | null }>({ info: null, error: null })
  useEffect(() => {
    let live = true
    http.get<AgentInfo>('/api/agent/info').then(
      (info) => live && setState({ info, error: null }),
      (e) => live && setState({ info: null, error: translateErrorValue(i18n, e) }),
    )
    return () => {
      live = false
    }
  }, [i18n])
  return state
}

interface Issued {
  result: PairingResult
  role: string
  name: string
  allowOffline: boolean
  auto: boolean
  baseline: Set<string>
  // 配对码签发时间（服务端时钟）
  issuedAt: number
}

function DialInPane({ info, onViewDetail }: { info: AgentInfo | null; onViewDetail: (id: string) => void }) {
  const { t, i18n } = useTranslation()
  const now = useNow()
  const devices = useLiveStore(selectDevices)
  const [name, setName] = useState('')
  const [role, setRole] = useState('server')
  // 用户动过开关后不再随角色变化；null 表示跟随角色默认
  const [offlineChoice, setOfflineChoice] = useState<boolean | null>(null)
  const [auto, setAuto] = useState(true)
  const [nameError, setNameError] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [issued, setIssued] = useState<Issued | null>(null)
  const allowOffline = offlineChoice ?? defaultAllowOffline(role)
  const roleLabel = (r: string) => (i18n.exists(`devices.list.role.${r}`) ? t(`devices.list.role.${r}`) : r)

  async function generate() {
    setFailure(null)
    if (nameTooLong(name)) {
      setNameError(true)
      return
    }
    setNameError(false)
    const body: PairingRequest = { name: name.trim(), role, allow_offline: allowOffline, auto_host_metrics: auto }
    // 判定「本次配对的设备」要用点击时已有的设备集合作基线
    const baseline = new Set((devices ?? []).map((d) => d.id))
    setBusy(true)
    try {
      const result = await http.post<PairingResult>('/api/devices/pairing', body)
      const exp = parseTime(result.expires_at)
      setIssued({
        result, role, name: name.trim(), allowOffline, auto, baseline,
        issuedAt: (exp ?? serverNow() + pairingCodeTtlMs) - pairingCodeTtlMs,
      })
    } catch (e) {
      setFailure(translateErrorValue(i18n, e))
    } finally {
      setBusy(false)
    }
  }

  const paired = useMemo(
    () => (issued ? findPairedDevice(devices, issued.baseline, issued.issuedAt, issued.name) : null),
    [devices, issued],
  )
  const connected = paired && paired.presence === 'online' && paired.confirmed ? paired : null

  if (issued) {
    return (
      <div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
          <span className="min-w-0 text-[13px] font-medium break-all">
            {t('devices.add.in.summary', {
              name: issued.name || t('devices.add.in.nameFallback'),
              role: roleLabel(issued.role),
              offline: t(issued.allowOffline ? 'devices.add.in.offlineOn' : 'devices.add.in.offlineOff'),
              auto: t(issued.auto ? 'devices.add.in.autoOn' : 'devices.add.in.autoOff'),
            })}
          </span>
          <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => setIssued(null)}>{t('devices.add.back')}</Button>
        </div>
        <PairingCommandPanel result={issued.result} role={issued.role} info={info} now={now} connected={connected} onViewDetail={onViewDetail} />
      </div>
    )
  }

  return (
    <form
      aria-label={t('devices.add.tabIn')}
      onSubmit={(e) => {
        e.preventDefault()
        void generate()
      }}
    >
      <Note tone="info" icon={<Info size={15} />} className="mx-4 mt-3.5">{t('devices.add.in.intro')}</Note>
      <FormRow label={t('devices.add.in.name')} fieldKey="name" htmlFor="in-name">
        <Input id="in-name" placeholder={t('devices.add.in.namePlaceholder')} value={name} invalid={nameError} onChange={(e) => { setName(e.target.value); setNameError(false) }} />
        {nameError ? <FieldError>{t('devices.add.fieldError.name.out_of_range')}</FieldError> : <FieldHelp>{t('devices.add.in.nameHelp')}</FieldHelp>}
      </FormRow>
      <FormRow label={t('devices.add.in.role')} fieldKey="role" htmlFor="in-role" required>
        <Select id="in-role" className="max-w-[260px]" value={role} onChange={(e) => setRole(e.target.value)}>
          {deviceRoles.map((r) => <option key={r} value={r}>{t('devices.add.roleOption', { label: roleLabel(r), key: r })}</option>)}
        </Select>
        <FieldHelp>{t('devices.add.in.roleHelp')}</FieldHelp>
      </FormRow>
      <FormRow label={t('devices.add.in.allowOffline')} fieldKey="allow_offline">
        <div className="flex items-center justify-between gap-3">
          <span className="text-[12.5px] text-muted-foreground">{t(allowOffline ? 'devices.add.in.allowOfflineOn' : 'devices.add.in.allowOfflineOff')}</span>
          <Switch checked={allowOffline} onChange={setOfflineChoice} ariaLabel={t('devices.add.in.allowOffline')} />
        </div>
        <FieldHelp>{t('devices.add.in.allowOfflineHelp')}</FieldHelp>
      </FormRow>
      <FormRow label={t('devices.add.in.autoMetrics')} fieldKey="auto_host_metrics">
        <div className="flex items-center justify-between gap-3">
          <span className="text-[12.5px] text-muted-foreground">{t('devices.add.in.autoMetricsHint')}</span>
          <Switch checked={auto} onChange={setAuto} ariaLabel={t('devices.add.in.autoMetrics')} />
        </div>
      </FormRow>
      {failure && <Note tone="crit" role="alert" className="mx-4 mb-3">{failure}</Note>}
      <div className="sticky bottom-0 flex justify-end gap-2 border-t border-border bg-panel-2 px-4 py-3">
        <Button type="submit" size="sm" className="rounded-[2px]" disabled={busy}>
          <KeyRound /> {busy ? t('devices.add.in.generating') : t('devices.add.in.generate')}
        </Button>
      </div>
    </form>
  )
}

function DialOutPane({ info, error, onViewDetail }: { info: AgentInfo | null; error: string | null; onViewDetail: (id: string) => void }) {
  const { t } = useTranslation()
  const [platform, setPlatform] = useState('linux-amd64')
  const download = info ? releaseDownloadCommand(info, platform) : null
  return (
    <div>
      <SectionHead no="01" title={t('devices.add.out.step1')} meta={t('devices.add.out.step1Meta')} />
      <div className="px-4 py-3">
        <FieldHelp className="mt-0 mb-2.5">{t('devices.add.out.step1Body')}</FieldHelp>
        {error && <Note tone="warn" className="mb-2.5">{t('common.withDetail', { summary: t('devices.add.agentInfoFailed'), detail: error })}</Note>}
        {info && !download && <Note tone="warn" icon={<Info size={15} />} className="mb-2.5">{t('devices.add.out.noRelease')}</Note>}
        {download && (
          <>
            <div className="mb-2 flex flex-wrap items-center gap-2 text-[12.5px]">
              <label htmlFor="out-platform" className="text-muted-foreground">{t('devices.add.out.platform')}</label>
              <Select id="out-platform" className="w-[170px] font-mono" value={platform} onChange={(e) => setPlatform(e.target.value)}>
                <option value="linux-amd64">linux-amd64</option>
                <option value="linux-arm64">linux-arm64</option>
              </Select>
            </div>
            <CommandBlock title={t('devices.add.out.downloadTitle')} text={download} />
          </>
        )}
        <CommandBlock title={t('devices.add.out.installTitle')} text={listenInstallCommand(download !== null)} />
        <ul className="m-0 mt-1 list-disc space-y-1 pl-5 text-[12.5px] leading-[1.55] text-muted-foreground">
          <li>{t('devices.add.out.tipFirewall', { port: agentListenPort })}</li>
          <li>{t('devices.add.out.tipWindow')}</li>
        </ul>
      </div>
      <SectionHead no="02" title={t('devices.add.out.step2')} />
      <DialOutForm mode="add" onViewDetail={onViewDetail} />
    </div>
  )
}
