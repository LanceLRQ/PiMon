import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import type { SystemInfo } from '@/types/generated'

export interface SystemState {
  info: SystemInfo | null
  // 加载失败时的译文；成功后清空
  error: string | null
  reload(): Promise<void>
}

// 系统信息：进入页面时取一次，之后每 30 秒刷新（数据目录用量服务端缓存 60 秒）
export function useSystemInfo(refreshMs = 30_000): SystemState {
  const { i18n } = useTranslation()
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      setInfo(await http.get<SystemInfo>('/api/system'))
      setError(null)
    } catch (e) {
      setError(translateErrorValue(i18n, e))
    }
  }, [i18n])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次远端数据
    void reload()
    const id = setInterval(() => void reload(), refreshMs)
    return () => clearInterval(id)
  }, [reload, refreshMs])

  return { info, error, reload }
}
