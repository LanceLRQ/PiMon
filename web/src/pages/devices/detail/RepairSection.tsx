import { Info, KeyRound } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { useNow } from '@/lib/time'
import { selectDevices, useLiveStore } from '@/store/live-store'
import type { Device, PairingResult } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { DialOutForm, type DialOutBody } from '../add/DialOutForm'
import { PairingCommandPanel } from '../add/PairingCommandPanel'
import { useAgentInfo } from '../add/use-agent-info'
import { DrawerSection } from './parts'

interface RepairSectionProps {
  device: Device
  // 即将发起重新配对：页面据此在旧设备被接替消失时不把它当作「不存在」
  onStart(deviceId: string, direction: 'dial_in' | 'dial_out'): void
  // 拨出重新配对成功，参数为接替它的新设备
  onPaired(device: Device): void
  onViewDetail(id: string): void
}

// 抽屉第 06 分区：适用场景说明，拨入设备生成新配对码与命令，拨出设备填新地址、指纹与配对码。
export function RepairSection({ device, onStart, onPaired, onViewDetail }: RepairSectionProps) {
  const { t } = useTranslation()
  const dialOut = device.direction === 'dial_out'
  // 占位设备（未确认，含悬挂占位）还没有可替换的凭据，不提供重新配对
  const placeholder = !device.confirmed
  return (
    <DrawerSection no="06" title={t('devices.repair.title')} meta={t(dialOut ? 'devices.detail.dialOutMeta' : 'devices.detail.dialInMeta')}>
      <div className="px-4 pt-1 pb-2 text-[12.5px]">
        <div className="text-muted-foreground">{t('devices.repair.whyTitle')}</div>
        <ul className="m-0 mt-1 list-disc space-y-0.5 pl-5">
          <li>{t('devices.repair.why1')}</li>
          <li>{t('devices.repair.why2')}</li>
          <li>{t('devices.repair.why3')}</li>
        </ul>
      </div>
      {placeholder ? (
        <div className="px-4 pb-3"><Note icon={<Info size={16} />}>{t('devices.repair.placeholder')}</Note></div>
      ) : dialOut ? (
        <DialOutRepair device={device} onStart={onStart} onPaired={onPaired} onViewDetail={onViewDetail} />
      ) : (
        <DialInRepair device={device} onStart={onStart} onViewDetail={onViewDetail} />
      )}
    </DrawerSection>
  )
}

function DialInRepair({ device, onStart, onViewDetail }: Pick<RepairSectionProps, 'device' | 'onStart' | 'onViewDetail'>) {
  const { t, i18n } = useTranslation()
  const now = useNow()
  const agent = useAgentInfo()
  const devices = useLiveStore(selectDevices)
  const [result, setResult] = useState<PairingResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const cmd = `${device.os === 'darwin' ? '' : 'sudo '}pimon-agent repin --fp ${agent.info?.fingerprint || 'SHA256:…'}`

  async function generate() {
    setBusy(true)
    setFailure(null)
    onStart(device.id, 'dial_in')
    try {
      setResult(await http.post<PairingResult>(`/api/devices/${device.id}/repair`, {}))
    } catch (e) {
      setFailure(translateErrorValue(i18n, e))
    } finally {
      setBusy(false)
    }
  }

  // 重新配对产生的占位设备：replaces 指向本设备；握手成功后旧设备被接替，抽屉随之关闭
  const placeholder = useMemo(() => devices?.find((d) => d.replaces === device.id) ?? null, [devices, device.id])
  const connected = placeholder && placeholder.presence === 'online' && placeholder.confirmed ? placeholder : null

  return (
    <>
      <div className="px-4 pb-3">
        {!result && (
          <Button variant="outline" size="sm" className="rounded-[2px]" disabled={busy} onClick={() => void generate()}>
            <KeyRound /> {busy ? t('devices.repair.generating') : t('devices.repair.generate')}
          </Button>
        )}
        {failure && <Note tone="crit" role="alert" className="mt-2">{failure}</Note>}
      </div>
      {result && (
        <div className="border-t border-border">
          <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5">
            <span className="min-w-0 text-[13px] font-medium break-all">{t('devices.repair.issuedFor', { name: device.name })}</span>
            <Button variant="outline" size="sm" className="rounded-[2px]" disabled={busy} onClick={() => void generate()}>{t('devices.repair.regenerate')}</Button>
          </div>
          <PairingCommandPanel mode="repair" result={result} role={device.role} info={agent.info} now={now} connected={connected} found={placeholder} onViewDetail={onViewDetail} />
        </div>
      )}
      <div className="px-4 pb-3">
        <Note icon={<Info size={16} />}>{t('devices.repair.repinIn', { cmd })}{!agent.info?.fingerprint && ` ${t('devices.repair.repinNoFp')}`}</Note>
      </div>
    </>
  )
}

function DialOutRepair({ device, onStart, onPaired, onViewDetail }: RepairSectionProps) {
  const { t } = useTranslation()
  // 代理要改回直连必须明确写 direct，空串表示沿用旧设备的代理
  const submit = async (req: DialOutBody) => {
    onStart(device.id, 'dial_out')
    const body = { ...req, proxy_id: req.proxy_id === '' && device.proxy_id !== '' ? 'direct' : req.proxy_id }
    const next = await http.post<Device>(`/api/devices/${device.id}/repair`, body)
    onPaired(next)
    return next
  }
  return (
    <>
      <div className="px-4 pb-3">
        <Note icon={<Info size={16} />}>{t('devices.repair.outNote')}</Note>
      </div>
      <DialOutForm mode="repair" initial={{ address: device.address, proxy_id: device.proxy_id }} submit={submit} onViewDetail={onViewDetail} />
    </>
  )
}
