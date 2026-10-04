import { CircleCheck, Info } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { parseTime } from '@/lib/time'
import type { AgentInfo, Device, PairingResult } from '@/types/generated'
import { Button } from '@/ui/button'
import { CopyButton } from '@/ui/copy-button'
import { Note } from '@/ui/note'
import { FieldHelp, FormRow } from '@/ui/section'
import { Select } from '@/ui/select'
import { StatusShape } from '@/ui/status-shape'
import { CommandBlock } from './CommandBlock'
import { formatCountdown, pairingCommands, wantsSudoersHint } from './pairing'

interface PairingCommandPanelProps {
  result: PairingResult
  role: string
  // 中枢接入信息；platforms 为空说明没有内嵌 agent
  info: AgentInfo | null
  // 服务端校正后的当前时间（毫秒）
  now: number
  // 本次配对对应的设备已上线时给出
  connected: Device | null
  onViewDetail: (id: string) => void
}

// 拨入配对命令面板：hub 地址、一行/分步命令、配对码与倒计时、指纹、等待上线；重新配对复用
export function PairingCommandPanel({ result, role, info, now, connected, onViewDetail }: PairingCommandPanelProps) {
  const { t } = useTranslation()
  const [hubIdx, setHubIdx] = useState(0)
  const [mode, setMode] = useState<'one' | 'steps'>('one')
  const hub = result.urls[hubIdx] ?? result.urls[0] ?? ''
  const cmds = pairingCommands(result, hub, role)
  const expiresAt = parseTime(result.expires_at)
  const remaining = expiresAt === null ? null : expiresAt - now
  const expired = remaining !== null && remaining <= 0
  const noEmbed = info !== null && info.platforms.length === 0
  const dev = role === 'dev'

  let wait
  if (connected) {
    wait = (
      <div className="mx-4 my-3 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-[2px] border border-status-ok px-3 py-2 text-[13px]">
        <span className="inline-flex items-center gap-1.5 font-medium text-status-ok"><CircleCheck size={14} aria-hidden="true" />{t('devices.add.in.connected')}</span>
        <span className="min-w-0 flex-1 break-all">
          {t('devices.add.in.connectedBody', { name: connected.name, address: connected.address || '—', version: connected.agent_version || '—' })}
        </span>
        <Button size="sm" className="rounded-[2px]" onClick={() => onViewDetail(connected.id)}>{t('devices.add.in.view')}</Button>
      </div>
    )
  } else if (expired) {
    wait = (
      <Note tone="warn" role="status" className="mx-4 my-3">
        <b>{t('devices.add.in.expired')}</b> {t('devices.add.in.expiredHint')}
      </Note>
    )
  } else {
    wait = (
      <div role="status" className="mx-4 my-3 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-[2px] border border-border bg-panel-2 px-3 py-2 text-[13px]">
        <span className="inline-flex items-center gap-1.5"><StatusShape state="pending" size={13} />{t('devices.add.in.waiting')}</span>
        {remaining !== null && (
          <span className="text-muted-foreground">{t('devices.add.in.remaining', { time: formatCountdown(remaining) })}</span>
        )}
      </div>
    )
  }

  return (
    <div>
      {wait}
      {noEmbed && (
        <Note tone="warn" icon={<Info size={15} />} className="mx-4 mb-3">{t('devices.add.in.noEmbed')}</Note>
      )}
      <SectionHead no="a" title={t('devices.add.in.hubSection')} meta={t('devices.add.in.hubMeta')} />
      <FormRow label={t('devices.add.in.hubLabel')} fieldKey="--hub" htmlFor="pairing-hub">
        <Select id="pairing-hub" className="font-mono" value={String(hubIdx)} onChange={(e) => setHubIdx(Number(e.target.value))}>
          {result.urls.map((u, i) => <option key={u} value={i}>{u}</option>)}
        </Select>
        <FieldHelp>{t('devices.add.in.hubHelp')}</FieldHelp>
      </FormRow>
      <SectionHead
        no="b"
        title={t('devices.add.in.runSection')}
        meta={
          <div role="radiogroup" aria-label={t('devices.add.in.modeLabel')} className="inline-flex overflow-hidden rounded-[2px] border border-line-strong bg-card text-xs">
            {(['one', 'steps'] as const).map((m, i) => (
              <button
                key={m}
                type="button"
                role="radio"
                aria-checked={mode === m}
                onClick={() => setMode(m)}
                className={`h-7 px-2.5 whitespace-nowrap ${i > 0 ? 'border-l border-border' : ''} ${mode === m ? 'bg-inv-bg text-inv-ink' : 'text-ink-2 hover:bg-panel-2'}`}
              >
                {t(m === 'one' ? 'devices.add.in.modeOne' : 'devices.add.in.modeSteps')}
              </button>
            ))}
          </div>
        }
      />
      <div className="px-4 py-3">
        {mode === 'one' ? (
          <>
            <CommandBlock title={t('devices.add.in.oneTitle')} text={cmds.oneLine} />
            <FieldHelp className="mt-0">{dev ? t('devices.add.in.oneNoteDev') : t('devices.add.in.oneNote')}</FieldHelp>
          </>
        ) : (
          <>
            {cmds.steps.map((s, i) => (
              <CommandBlock
                key={i}
                title={t(i === 3 ? (dev ? 'devices.add.in.step4Dev' : 'devices.add.in.step4') : `devices.add.in.step${i + 1}`)}
                text={s}
              />
            ))}
            <FieldHelp className="mt-0">{t('devices.add.in.stepsNote')}</FieldHelp>
          </>
        )}
        {wantsSudoersHint(role) && (
          <FieldHelp>{t('devices.add.in.sudoersHint', { flag: '--sudoers' })}</FieldHelp>
        )}
      </div>
      <FormRow label={t('devices.add.in.code')} fieldKey={t('devices.add.in.codeKey')}>
        <div className="flex items-center gap-2">
          <code className="font-mono text-[13px] tracking-wide break-all">{result.code}</code>
          <CopyButton text={result.code} label={t('devices.add.in.copyCode')} />
        </div>
        <FieldHelp>{t('devices.add.in.codeHelp')}</FieldHelp>
      </FormRow>
      <FormRow label={t('devices.add.in.fingerprint')} fieldKey="--fp">
        <div className="flex items-center gap-2">
          <code className="font-mono text-[12px] break-all">{result.fingerprint}</code>
          <CopyButton text={result.fingerprint} label={t('devices.add.in.copyFingerprint')} />
        </div>
        <FieldHelp>{t('devices.add.in.fingerprintHelp')}</FieldHelp>
      </FormRow>
      <FormRow label={t('devices.add.in.optional')}>
        <FieldHelp className="mt-0">{t('devices.add.in.optionalHelp')}</FieldHelp>
      </FormRow>
    </div>
  )
}

export function SectionHead({ no, title, meta }: { no: string; title: string; meta?: React.ReactNode }) {
  return (
    <div className="flex min-h-9 flex-wrap items-center gap-2.5 border-y border-border bg-panel-2/50 px-4 py-1">
      <span className="rounded-[2px] border border-foreground px-[5px] font-mono text-[11px] leading-[16px]">{no}</span>
      <h3 className="text-[13px] font-medium">{title}</h3>
      {meta && <span className="ml-auto min-w-0 text-[12px] text-muted-foreground">{meta}</span>}
    </div>
  )
}
