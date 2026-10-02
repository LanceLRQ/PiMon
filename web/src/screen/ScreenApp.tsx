import { useEffect, useLayoutEffect, useState } from 'react'
// 三套主题 token 与屏幕端样式只在屏幕根模块引入，不进 main.tsx（管理端与屏幕端共用 index.html，全局引入会串色）
import '@/themes/index.css'
import './screen.css'
import { useSession } from '@/app/session'
import { DisconnectBadge } from './DisconnectBadge'
import { HubDownPage } from './ScreenNotices'
import { ScreenView } from './ScreenView'
import { screenStore, useScreenStore } from './screen-store'
import { createScreenSession } from './session'
import { defaultSnapshotCache, startSnapshotPersistence, type SnapshotCache, type StoredScreenData } from './snapshot-cache'
import { ScreenNavigator } from './state-machine'
import { useGrace } from './use-grace'
import { WakeLockKeeper } from './wake-lock'

export interface ScreenAppProps {
  /** 本地存档的上一份数据：hub 暂时连不上时先用它渲染 */
  initial?: StoredScreenData | null
  cache?: SnapshotCache
  /** 是否持有 Wake Lock，默认持有（测试里关掉） */
  wakeLock?: boolean
  /** 断线角标与「hub 未运行」页的宽限，默认 3 秒 */
  badgeGraceMs?: number
}

// 从没收到过数据、连接也没建立：宽限后显示「hub 未运行」页，而不是一块空屏
function NoDataNotice({ graceMs }: { graceMs: number }) {
  const noData = useScreenStore((s) => s.layout === null && !s.connected)
  return useGrace(noData, graceMs) ? <HubDownPage /> : null
}

/**
 * 屏幕端应用：连接中枢、驱动状态机并渲染屏幕。
 * 连接中断时保留屏幕内容并显示断线角标；最后一份数据落到本地存档，页面重载或 hub 重启期间可用它恢复显示；
 * 持有 Wake Lock 防止息屏；WebSocket 握手被拒（会话被吊销）时重新查询会话，由守卫切换到令牌失效页。
 */
export function ScreenApp({ initial = null, cache = defaultSnapshotCache, wakeLock = true, badgeGraceMs = 3000 }: ScreenAppProps) {
  const { refresh } = useSession()
  const [nav] = useState(() => new ScreenNavigator())
  const [session] = useState(() =>
    createScreenSession({ store: screenStore, nav, socket: { onHandshakeFailed: () => void refresh() } }),
  )

  // 首帧前还原本地存档（严格模式下重复挂载也会重新还原）
  useLayoutEffect(() => {
    if (initial) screenStore.restore(initial)
    return () => screenStore.reset()
  }, [initial])

  useEffect(() => {
    session.start()
    const stopPersist = startSnapshotPersistence(screenStore, cache)
    const keeper = wakeLock ? new WakeLockKeeper() : null
    keeper?.start()
    return () => {
      keeper?.stop()
      stopPersist()
      session.stop()
      nav.dispose()
    }
  }, [session, nav, cache, wakeLock])

  return (
    <>
      <ScreenView store={screenStore} nav={nav} reporter={session.reporter} />
      <DisconnectBadge store={screenStore} graceMs={badgeGraceMs} />
      <NoDataNotice graceMs={badgeGraceMs} />
    </>
  )
}
