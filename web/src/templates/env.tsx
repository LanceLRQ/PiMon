import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import type { ScreenEnv } from './types'

const defaultEnv: ScreenEnv = { now: () => Date.now(), timezone: 'UTC', lang: 'zh' }

export const ScreenEnvContext = createContext<ScreenEnv>(defaultEnv)

interface ProviderProps extends ScreenEnv {
  children: ReactNode
}

/** 屏幕根提供：服务器校正后的 now()、ScreenSettings.timezone、界面语言 */
export function ScreenEnvProvider({ now, timezone, lang, children }: ProviderProps) {
  const value = useMemo(() => ({ now, timezone, lang }), [now, timezone, lang])
  return <ScreenEnvContext.Provider value={value}>{children}</ScreenEnvContext.Provider>
}

export function useScreenEnv(): ScreenEnv {
  return useContext(ScreenEnvContext)
}

/** 按间隔重渲染并返回服务器「现在」（毫秒） */
export function useScreenNow(intervalMs = 1000): number {
  const { now } = useScreenEnv()
  const [, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now()
}
