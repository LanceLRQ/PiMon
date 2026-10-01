import { WifiOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatStamp } from '@/templates/format'
import { screenNow, useScreenStore, type ScreenStore } from './screen-store'
import { useGrace } from './use-grace'

export interface DisconnectBadgeProps {
  store: ScreenStore
  /** 连接中断持续多久才显示，默认 3 秒（页面刚加载时连接还没建立，不该闪一下） */
  graceMs?: number
}

/**
 * 断线角标：连接中断时在右上角显示「连接中断，显示 hh:mm 的数据」，不清空屏幕。
 * 时间是屏幕上数据对应的服务端时间，按屏幕设置的时区显示；重连收到 snapshot 后连接恢复，角标消失。
 * 没有任何数据时不显示（由「hub 未运行」页接管）；关屏时保持纯黑，不显示。
 */
export function DisconnectBadge({ store, graceMs = 3000 }: DisconnectBadgeProps) {
  const { t } = useTranslation()
  const connected = useScreenStore((s) => s.connected, store)
  const hasLayout = useScreenStore((s) => s.layout !== null, store)
  const off = useScreenStore((s) => s.screenState?.mode === 'off', store)
  const lastDataAt = useScreenStore((s) => s.lastDataAt, store)
  const settings = useScreenStore((s) => s.settings, store)
  const visible = useGrace(!connected && hasLayout && !off, graceMs)
  if (!visible) return null
  const time =
    lastDataAt === null ? '--:--' : formatStamp(lastDataAt, screenNow(store), settings?.timezone ?? 'UTC', settings?.language === 'en' ? 'en' : 'zh')
  return (
    <div
      role="status"
      data-disconnect-badge
      className="pointer-events-none fixed top-1 right-1 z-40 flex items-center gap-1.5 rounded-md border border-s-warning bg-s-card px-2 py-0.5 text-xs text-s-fg"
    >
      <WifiOff size={12} className="text-s-warning" aria-hidden />
      <span>{t('screenApp.disconnected', { time })}</span>
    </div>
  )
}
