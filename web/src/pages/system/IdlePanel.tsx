import { useTranslation } from 'react-i18next'
import { formatDateTime, parseTime } from '@/lib/time'
import type { KioskIdleCheck } from '@/types/generated'
import { Section } from '@/ui/section'
import { StatusShape } from '@/ui/status-shape'
import type { ScreenStatusState } from '@/pages/screens/use-screen-status'

const files = [
  { key: 'user', path: '~/.config/labwc/autostart' },
  { key: 'greeter', path: '/etc/xdg/labwc-greeter/autostart' },
  { key: 'system', path: '/etc/xdg/labwc/autostart' },
] as const

function Row({ hit, children, path }: { hit: boolean; children: string; path?: string }) {
  return (
    <li className="flex items-start gap-2 border-t border-border px-4 py-2 text-[13px] first:border-t-0">
      <span className="mt-[3px]">
        <StatusShape state={hit ? 'warning' : 'ok'} size={12} />
      </span>
      <span className="min-w-0">
        <span>{children}</span>
        {path && <code className="mt-0.5 block font-mono text-[11.5px] break-all text-muted-foreground">{path}</code>}
      </span>
    </li>
  )
}

/** 06.4 系统空闲息屏检查：展示 kiosk 上报的 swayidle 检查结果，异常时给手工处理步骤 */
export function IdlePanel({ state }: { state: ScreenStatusState }) {
  const { t, i18n } = useTranslation()
  const check: KioskIdleCheck | undefined = state.status?.kiosk?.idle_check
  const found = !!check && (check.user || check.greeter || check.system || check.swayidle_running)
  const checkedMs = parseTime(check?.checked_at)

  return (
    <Section no="06.4" title={t('system.idle.title')} meta={t('system.idle.meta')}>
      {!check ? (
        <div className="px-4 py-5 text-[12.5px] leading-[1.6] text-muted-foreground">{t('system.idle.notReported')}</div>
      ) : (
        <>
          <div className="flex items-center gap-2 border-b border-border px-4 py-3 text-[13.5px] font-medium">
            <StatusShape state={found ? 'warning' : 'ok'} size={16} />
            {found ? t('system.idle.found') : t('system.idle.allClear')}
          </div>
          <ul>
            {files.map((f) => (
              <Row key={f.key} hit={check[f.key]} path={f.path}>
                {t(check[f.key] ? 'system.idle.fileHit' : 'system.idle.fileClean', { name: t(`system.idle.${f.key}`) })}
              </Row>
            ))}
            <Row hit={check.swayidle_running}>{t(check.swayidle_running ? 'system.idle.processRunning' : 'system.idle.processIdle')}</Row>
          </ul>
          {found && <div className="border-t border-border px-4 py-2.5 text-[12.5px] leading-[1.6]">{t('system.idle.fix')}</div>}
          {checkedMs !== null && (
            <div className="border-t border-border px-4 py-1.5 text-[11.5px] text-muted-foreground">
              {t('system.idle.checkedAt', { time: formatDateTime(checkedMs, i18n.language) })}
            </div>
          )}
        </>
      )}
    </Section>
  )
}
