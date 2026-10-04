import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import type { PluginList } from '@/types/generated'

export interface PluginsState {
  list: PluginList | null
  error: unknown
  reload(): Promise<void>
  // 重新扫描 exec 插件目录并更新列表；失败时抛出
  rescan(): Promise<PluginList>
}

// 插件列表（含 exec 插件目录与加载问题）：进入页面时取一次，随界面语言与运行位置重取。
// machine 缺省或为 hub 时取中枢注册表，是设备 ID 时取该设备上报的插件，null 表示暂不取（位置还没确定）。
// 位置切换期间保留上一份列表，只采用最新一次请求的结果。
export function usePlugins(machine?: string | null): PluginsState {
  const { i18n } = useTranslation()
  const lang = i18n.language === 'en' ? 'en' : 'zh'
  const [list, setList] = useState<PluginList | null>(null)
  const [error, setError] = useState<unknown>(null)
  const seq = useRef(0)
  const skip = machine === null
  const device = machine && machine !== 'hub' ? machine : undefined

  const reload = useCallback(async () => {
    if (skip) return
    const mine = ++seq.current
    try {
      const next = await http.get<PluginList>('/api/plugins', { query: device ? { lang, machine: device } : { lang } })
      if (mine !== seq.current) return
      setList(next)
      setError(null)
    } catch (e) {
      if (mine !== seq.current) return
      setError(e)
    }
  }, [lang, device, skip])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面与切换语言、位置时同步远端数据
    void reload()
  }, [reload])

  const rescan = useCallback(async () => {
    const next = await http.post<PluginList>('/api/plugins/rescan', undefined, { query: { lang } })
    setList(next)
    setError(null)
    return next
  }, [lang])

  return { list, error, reload, rescan }
}
