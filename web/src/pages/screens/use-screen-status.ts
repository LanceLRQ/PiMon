import { useCallback, useEffect, useState } from 'react'
import { http } from '@/api/client'
import type { ScreenStatus } from '@/types/generated'

export interface ScreenStatusState {
  status: ScreenStatus | null
  /** 最近一次读取是否失败（失败时保留上次成功的状态） */
  failed: boolean
  reload(): Promise<void>
}

// 显示器状态（在线、视口、触摸能力、当前 screen）没有实时推送：进入页面取一次，之后定时刷新
export function useScreenStatus(refreshMs = 10_000): ScreenStatusState {
  const [status, setStatus] = useState<ScreenStatus | null>(null)
  const [failed, setFailed] = useState(false)

  const reload = useCallback(async () => {
    try {
      setStatus(await http.get<ScreenStatus>('/api/screen/status'))
      setFailed(false)
    } catch {
      setFailed(true)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次远端数据
    void reload()
    const id = setInterval(() => void reload(), refreshMs)
    return () => clearInterval(id)
  }, [reload, refreshMs])

  return { status, failed, reload }
}
