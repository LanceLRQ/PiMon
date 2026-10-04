import { Info } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { formatUptime } from '@/lib/format'
import { useUptimeSeconds } from '@/lib/use-uptime'
import { useSystemInfo } from '@/pages/system/use-system'
import { selectInstances, useLiveStore } from '@/store/live-store'
import type { AgentInfo } from '@/types/generated'
import { Button } from '@/ui/button'
import { CopyButton } from '@/ui/copy-button'
import { Note } from '@/ui/note'
import { Sheet, SheetContent } from '@/ui/sheet'
import { StatusShape } from '@/ui/status-shape'
import { DrawerSection, Kv } from './parts'
import { InstanceRows } from './InstanceRows'

// 中枢本机详情：只读。中枢不是设备行，信息来自 /api/system 与 /api/agent/info。
export function HubDetail({ onClose }: { onClose(): void }) {
  const { t } = useTranslation()
  const { info } = useSystemInfo()
  const uptime = useUptimeSeconds(info?.uptime_seconds)
  const instances = useLiveStore(selectInstances).filter((i) => i.runs_on === 'hub')
  const [agent, setAgent] = useState<AgentInfo | null>(null)

  useEffect(() => {
    let live = true
    http.get<AgentInfo>('/api/agent/info').then(
      (v) => live && setAgent(v),
      () => {},
    )
    return () => {
      live = false
    }
  }, [])

  const dash = '—'
  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent
        className="w-[560px] mobile:w-full"
        srTitle={t('devices.detail.hubTitle')}
        header={
          <div className="flex items-center gap-2.5">
            <StatusShape state="ok" size={18} />
            <h2 className="truncate text-[15px] font-medium">{t('devices.detail.hubTitle')}</h2>
            <span className="ml-auto text-[12px] text-muted-foreground">{t('devices.detail.readOnly')}</span>
          </div>
        }
        footer={
          <Button variant="outline" size="sm" className="rounded-[2px]" onClick={onClose}>
            {t('devices.detail.close')}
          </Button>
        }
      >
        <DrawerSection no="01" title={t('devices.detail.hubOverview')} meta={t('devices.detail.readOnly')}>
          <Kv
            rows={[
              { label: t('devices.detail.conn.direction'), value: t('devices.list.dir.local') },
              { label: t('devices.detail.conn.address'), value: '127.0.0.1' },
              { label: t('devices.detail.conn.platform'), value: info ? `${info.os}/${info.arch}` : dash },
              { label: t('devices.detail.hubUptime'), value: uptime !== null ? formatUptime(t, uptime) : dash },
            ]}
          />
          <div className="px-4 pb-3">
            <Note icon={<Info size={16} />}>{t('devices.detail.hubNote')}</Note>
          </div>
        </DrawerSection>
        <DrawerSection no="02" title={t('devices.detail.hubChannel')} meta="/api/agent/info">
          {agent ? (
            <Kv
              rows={[
                { label: t('devices.detail.hubPort'), value: agent.agent_port > 0 ? String(agent.agent_port) : t('devices.detail.hubChannelOff') },
                {
                  label: t('devices.detail.hubFingerprint'),
                  value: (
                    <span className="inline-flex items-center gap-1.5">
                      <span>{agent.fingerprint || dash}</span>
                      {agent.fingerprint && <CopyButton text={agent.fingerprint} label={t('devices.add.in.copyFingerprint')} />}
                    </span>
                  ),
                },
                { label: t('devices.detail.hubVersion'), value: agent.hub_version || dash },
                { label: t('devices.detail.hubProtocol'), value: `${agent.protocol_version} / ${agent.min_compatible_version}` },
                { label: t('devices.detail.hubPlatforms'), value: agent.platforms.length > 0 ? agent.platforms.join(' · ') : t('devices.detail.hubNoEmbed') },
              ]}
            />
          ) : (
            <div className="px-4 pb-3 text-[12.5px] text-muted-foreground">{t('common.loading')}</div>
          )}
        </DrawerSection>
        <DrawerSection
          no="03"
          title={t('devices.detail.instances')}
          meta={
            <>
              {t('devices.detail.instanceCount', { count: instances.length })} ·{' '}
              <Link to="/instances" className="underline underline-offset-2">{t('devices.detail.viewInstances')}</Link>
            </>
          }
        >
          <InstanceRows instances={instances} pendingHint={false} />
        </DrawerSection>
      </SheetContent>
    </Sheet>
  )
}
