import { createContext, useContext } from 'react'
import type { Proxy } from '@/types/generated'

export interface LookupCandidate {
  value: string
  label: string
}

// 表单需要的外部数据：代理列表与插件候选查询。由页面提供，组件不直接发请求。
export interface FormContextValue {
  // null 表示尚未加载完成
  proxies: Proxy[] | null
  proxiesFailed: boolean
  lookup(key: string, query: string, signal: AbortSignal): Promise<LookupCandidate[]>
}

const empty: FormContextValue = {
  proxies: [],
  proxiesFailed: false,
  lookup: async () => [],
}

export const FormContext = createContext<FormContextValue>(empty)

export function useFormContext(): FormContextValue {
  return useContext(FormContext)
}
