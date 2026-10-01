import { useCallback, useEffect, useState } from 'react'
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

// 插件列表（含 exec 插件目录与加载问题）：进入页面时取一次，随界面语言重取
export function usePlugins(): PluginsState {
  const { i18n } = useTranslation()
  const lang = i18n.language === 'en' ? 'en' : 'zh'
  const [list, setList] = useState<PluginList | null>(null)
  const [error, setError] = useState<unknown>(null)

  const reload = useCallback(async () => {
    try {
      setList(await http.get<PluginList>('/api/plugins', { query: { lang } }))
      setError(null)
    } catch (e) {
      setError(e)
    }
  }, [lang])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面与切换语言时同步远端数据
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
