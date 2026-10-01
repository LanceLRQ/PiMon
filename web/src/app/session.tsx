import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { setUnauthorizedHandler } from '@/api/client'
import { setUnsavedGuardBypass } from './unsaved-guard'
import { fetchSession, type SessionInfo } from '@/api/session'

export type SessionStatus = 'loading' | 'ready' | 'error'

export interface SessionValue {
  status: SessionStatus
  info: SessionInfo | null
  // 重新向服务端查询会话（登录、首次设置完成、WebSocket 握手失败后调用）
  refresh: () => Promise<void>
  // 本地标记为未登录（退出登录、REST 返回 401 时调用），由守卫负责跳转
  markSignedOut: () => void
  // 跳到站内以外的页面（如屏幕会话访问管理页时跳 /screen）；可注入便于测试
  redirectExternal: (path: string) => void
}

const SessionContext = createContext<SessionValue | null>(null)

interface SessionProviderProps {
  children: ReactNode
  redirectExternal?: (path: string) => void
}

export function SessionProvider({ children, redirectExternal }: SessionProviderProps) {
  const [status, setStatus] = useState<SessionStatus>('loading')
  const [info, setInfo] = useState<SessionInfo | null>(null)

  const refresh = useCallback(async () => {
    try {
      const next = await fetchSession()
      setUnsavedGuardBypass(!next.authenticated)
      setInfo(next)
      setStatus('ready')
    } catch {
      // 已经有会话信息时（hub 重启、短暂不可达）保持原状，外壳与实时连接不能因此卸载；仅首次加载失败才报错
      setStatus((prev) => (prev === 'ready' ? 'ready' : 'error'))
    }
  }, [])

  const markSignedOut = useCallback(() => {
    setUnsavedGuardBypass(true)
    setInfo((prev) => (prev ? { ...prev, authenticated: false, kind: undefined } : prev))
  }, [])

  // 启动时查询一次会话
  useEffect(() => {
    let cancelled = false
    fetchSession()
      .then((i) => {
        if (cancelled) return
        setInfo(i)
        setStatus('ready')
      })
      .catch(() => {
        if (!cancelled) setStatus('error')
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    setUnauthorizedHandler(markSignedOut)
    return () => setUnauthorizedHandler(null)
  }, [markSignedOut])

  const value = useMemo<SessionValue>(
    () => ({
      status,
      info,
      refresh,
      markSignedOut,
      redirectExternal: redirectExternal ?? ((p) => window.location.assign(p)),
    }),
    [status, info, refresh, markSignedOut, redirectExternal],
  )
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession(): SessionValue {
  const v = useContext(SessionContext)
  if (!v) throw new Error('useSession 必须在 SessionProvider 内使用')
  return v
}
