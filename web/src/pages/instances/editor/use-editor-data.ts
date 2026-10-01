import { useCallback, useEffect, useState } from 'react'
import { http } from '@/api/client'
import type { LookupCandidate } from '@/forms/context'
import type { InstanceDetail, PluginLookupResponse, Proxy } from '@/types/generated'

// 全局代理列表，进入页面时取一次；失败时按空列表（只能选直连）处理并标记
export function useProxyList(): { proxies: Proxy[] | null; failed: boolean } {
  const [state, setState] = useState<{ proxies: Proxy[] | null; failed: boolean }>({ proxies: null, failed: false })
  useEffect(() => {
    let alive = true
    http.get<Proxy[]>('/api/proxies').then(
      (list) => alive && setState({ proxies: list, failed: false }),
      () => alive && setState({ proxies: [], failed: true }),
    )
    return () => {
      alive = false
    }
  }, [])
  return state
}

// 编辑页载入实例详情（配置已脱敏，密钥只带「已设置」标记）
export type DetailState = { kind: 'loading' } | { kind: 'ready'; detail: InstanceDetail } | { kind: 'error'; error: unknown }

export function useInstanceDetail(id: string | undefined): [DetailState, (d: InstanceDetail) => void] {
  const [state, setState] = useState<DetailState>({ kind: 'loading' })
  useEffect(() => {
    if (!id) return
    let alive = true
    http.get<InstanceDetail>(`/api/instances/${id}`).then(
      (detail) => alive && setState({ kind: 'ready', detail }),
      (error: unknown) => alive && setState({ kind: 'error', error }),
    )
    return () => {
      alive = false
    }
  }, [id])
  const set = useCallback((detail: InstanceDetail) => setState({ kind: 'ready', detail }), [])
  return [state, set]
}

// 调用插件的 lookup 接口取候选；语言跟随界面语言
export function lookupRequest(pluginId: string, lang: 'zh' | 'en') {
  return async (key: string, query: string, signal: AbortSignal): Promise<LookupCandidate[]> => {
    const res = await http.post<PluginLookupResponse>(
      `/api/plugins/${encodeURIComponent(pluginId)}/lookup/${encodeURIComponent(key)}`,
      { query },
      { query: { lang }, signal },
    )
    return res.candidates
  }
}
