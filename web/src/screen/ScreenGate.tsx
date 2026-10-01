import { useEffect, useState } from 'react'
import { useSession } from '@/app/session'
import { registerScreenServiceWorker } from './offline'
import { ScreenApp } from './ScreenApp'
import { AdminPreviewPage, HubDownPage, ShellFrame, TokenInvalidPage } from './ScreenNotices'
import { SetupCodePage } from './SetupCodePage'
import { defaultSnapshotCache, type SnapshotCache, type StoredScreenData } from './snapshot-cache'

export interface ScreenGateProps {
  cache?: SnapshotCache
  /** 设置码页与 hub 不可达时的轮询间隔，默认 5 秒 */
  pollMs?: number
  wakeLock?: boolean
}

/**
 * /screen 的守卫（不走管理端的 RequireSession，否则 kiosk 会被带到登录页）：
 * - 没有会话或会话失效（含 WebSocket 握手被拒、会话被吊销）→ 令牌失效页；
 * - 管理员会话 → 仅预览提示（不连接中枢、不被当作显示器）；
 * - 还没有管理员 → 设置码页，设置完成后自动进入屏幕；
 * - 会话查询本身失败（hub 不可达）→ 有本地存档就带着存档进入屏幕端应用（显示断线角标），没有就显示「hub 未运行」页，并轮询等待恢复。
 */
export function ScreenGate({ cache = defaultSnapshotCache, pollMs = 5000, wakeLock }: ScreenGateProps) {
  const { status, info, refresh } = useSession()
  // undefined 表示还在读取本地存档
  const [stored, setStored] = useState<StoredScreenData | null | undefined>(undefined)

  useEffect(() => {
    let cancelled = false
    void cache.load().then((s) => {
      if (!cancelled) setStored(s)
    })
    return () => {
      cancelled = true
    }
  }, [cache])

  useEffect(() => {
    // 开发服务器的模块是动态的，缓存外壳只会带来困惑
    if (!import.meta.env.DEV) void registerScreenServiceWorker()
  }, [])

  // 令牌失效后不再保留本地存档：旧数据不该被一个已失效的屏幕继续展示
  const tokenInvalid = status === 'ready' && info !== null && !info.authenticated
  useEffect(() => {
    if (tokenInvalid) void cache.clear()
  }, [tokenInvalid, cache])

  useEffect(() => {
    if (status !== 'error') return
    const id = setInterval(() => void refresh(), pollMs)
    return () => clearInterval(id)
  }, [status, refresh, pollMs])

  if (stored === undefined || status === 'loading') return <ShellFrame />
  if (status === 'error' || !info) return stored ? <ScreenApp initial={stored} cache={cache} wakeLock={wakeLock} /> : <HubDownPage />
  if (!info.authenticated) return <TokenInvalidPage />
  if (info.kind === 'admin') return <AdminPreviewPage />
  if (info.needs_setup) return <SetupCodePage pollMs={pollMs} />
  return <ScreenApp initial={stored} cache={cache} wakeLock={wakeLock} />
}
