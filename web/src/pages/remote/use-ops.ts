import { useCallback, useEffect, useState } from 'react'
import { http } from '@/api/client'
import type { ScreenOp } from '@/types/generated'

export const OPS_LIMIT = 5

/** 最近的远程操作记录：进入页面取一次，之后定时刷新；操作后调用 reload 立即刷新 */
export function useScreenOps(refreshMs = 10_000) {
  const [ops, setOps] = useState<ScreenOp[] | null>(null)
  const [failed, setFailed] = useState(false)
  const reload = useCallback(async () => {
    try {
      setOps(await http.get<ScreenOp[]>(`/api/screen/ops?limit=${OPS_LIMIT}`))
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
  return { ops, failed, reload }
}
