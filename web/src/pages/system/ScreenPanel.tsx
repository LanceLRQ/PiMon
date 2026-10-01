import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { effectiveTouch } from '@/pages/screens/touch'
import { useScreenStatus } from '@/pages/screens/use-screen-status'
import { formatDateTime, parseTime } from '@/lib/time'
import { selectLayout, selectSettings, useLiveStore } from '@/store/live-store'
import { Section } from '@/ui/section'
import { StatusShape } from '@/ui/status-shape'
import { KV } from './KV'

/** 系统页的屏幕区：显示器在线状态与上报的参数，输入方式与界面缩放在「屏幕管理」里调整 */
export function ScreenPanel() {
  const { t, i18n } = useTranslation()
  const { status, failed } = useScreenStatus()
  const settings = useLiveStore(selectSettings)
  const layout = useLiveStore(selectLayout)
  const unknown = <span className="font-sans text-muted-foreground">{t('system.screen.unknown')}</span>

  const never = status !== null && !status.online && !status.last_seen
  const meta = status ? (
    <span className="inline-flex items-center gap-1.5">
      <StatusShape state={status.online ? 'ok' : 'offline'} size={12} />
      {status.online ? t('system.screen.online') : never ? t('system.screen.never') : t('system.screen.offline')}
    </span>
  ) : (
    t('system.screen.meta')
  )

  const display = settings?.screen
  const touch = display ? effectiveTouch(display.input_mode, status?.coarse_pointer) : null
  const detected = status?.coarse_pointer === undefined ? null : t(status.coarse_pointer ? 'system.screen.touchYes' : 'system.screen.touchNo')
  const inputText = !display
    ? unknown
    : display.input_mode === 'auto'
      ? detected
        ? t('system.screen.inputAuto', { kind: detected })
        : t('system.screen.touchAuto')
      : t(touch ? 'system.screen.touchYes' : 'system.screen.touchNo')

  const lastSeenMs = status && !status.online ? parseTime(status.last_seen) : null
  const rows = [
    { k: t('system.screen.resolution'), v: status?.viewport ? `${status.viewport.w}×${status.viewport.h}` : unknown },
    { k: t('system.screen.grid'), v: layout ? `${layout.layout.grid.cols}×${layout.layout.grid.rows}` : unknown },
    { k: t('system.screen.input'), v: inputText },
    { k: t('system.screen.scale'), v: display ? display.ui_scale.toFixed(display.ui_scale === 1.25 ? 2 : 1) : unknown },
    // 离线后服务端不清空 current_screen：只在线时展示
    { k: t('system.screen.current'), v: status?.online && status.current_screen ? status.current_screen : unknown },
    ...(lastSeenMs !== null ? [{ k: t('system.screen.lastSeen'), v: formatDateTime(lastSeenMs, i18n.language) }] : []),
  ]

  return (
    <Section no="06.3" title={t('system.screen.title')} meta={meta}>
      {failed && !status ? (
        <div className="px-4 py-5 text-[12.5px] leading-[1.6] text-muted-foreground">{t('system.screen.unavailable')}</div>
      ) : (
        <KV rows={rows} />
      )}
      <div className="border-t border-border px-4 py-2 text-[12px]">
        <Link to="/screens" className="underline underline-offset-2">
          {t('system.screen.manage')}
        </Link>
      </div>
    </Section>
  )
}
