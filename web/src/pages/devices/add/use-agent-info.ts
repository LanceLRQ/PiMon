import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import type { AgentInfo } from '@/types/generated'

// 读取中枢接入信息（内嵌平台、Release 下载模板）；取不到时命令面板仍可用，只是不提示内嵌情况
export function useAgentInfo(): { info: AgentInfo | null; error: string | null } {
  const { i18n } = useTranslation()
  const [state, setState] = useState<{ info: AgentInfo | null; error: string | null }>({ info: null, error: null })
  useEffect(() => {
    let live = true
    http.get<AgentInfo>('/api/agent/info').then(
      (info) => live && setState({ info, error: null }),
      (e) => live && setState({ info: null, error: translateErrorValue(i18n, e) }),
    )
    return () => {
      live = false
    }
  }, [i18n])
  return state
}
