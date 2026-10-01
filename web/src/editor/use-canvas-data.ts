import { useEffect, useMemo, useState } from 'react'
import { http } from '@/api/client'
import type { InstanceDataMap } from '@/templates'
import type { Instance, InstanceDetail } from '@/types/generated'
import { toInstanceData } from './resolve-draft'

interface Fetched {
  key: string
  detail: InstanceDetail | null
}

/**
 * 画布取数：对草稿引用到的实例各取一次详情（报告里的数据项）；
 * 实例有新的成功采集（last_success_at 变化）时重取，展示状态随实时连接的实例列表更新。
 * 取不到时 items 为空，由模板显示为未知，不当作零。
 */
export function useCanvasData(instanceIds: readonly string[], instances: readonly Instance[]): InstanceDataMap {
  const [fetched, setFetched] = useState<Record<string, Fetched>>({})
  const idsKey = instanceIds.join(',')
  const keyOf = (id: string) => `${id}@${instances.find((i) => i.id === id)?.last_success_at ?? ''}`
  const needKey = instanceIds.map(keyOf).join('|')

  useEffect(() => {
    const ctrl = new AbortController()
    for (const id of idsKey ? idsKey.split(',') : []) {
      const key = keyOf(id)
      if (fetched[id]?.key === key) continue
      http
        .get<InstanceDetail>(`/api/instances/${encodeURIComponent(id)}`, { signal: ctrl.signal })
        .then((detail) => setFetched((m) => ({ ...m, [id]: { key, detail } })))
        .catch(() => {
          if (!ctrl.signal.aborted) setFetched((m) => ({ ...m, [id]: { key, detail: null } }))
        })
    }
    return () => ctrl.abort()
    // keyOf 与 fetched 由 needKey 间接覆盖：只在引用集合或最近成功时间变化时重取
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idsKey, needKey])

  return useMemo(() => {
    const out: Record<string, ReturnType<typeof toInstanceData>> = {}
    for (const id of idsKey ? idsKey.split(',') : []) {
      const inst = instances.find((i) => i.id === id)
      if (inst) out[id] = toInstanceData(inst, fetched[id]?.detail ?? undefined)
    }
    return out
  }, [idsKey, instances, fetched])
}
