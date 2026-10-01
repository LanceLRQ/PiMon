import type { ResolvedWidget, ScreenInstanceData } from '@/types/generated'

/** 实例 id → 该实例的展示状态与最新数据项；由屏幕端实时数据 store 提供，模板不直接连 WebSocket */
export type InstanceDataMap = Readonly<Record<string, ScreenInstanceData | undefined>>

/** 数值阈值：手动阈值存于 options.threshold，默认阈值（来自 manifest 的告警默认规则）由调用方传入 */
export interface Threshold {
  enabled: boolean
  warning?: number
  critical?: number
  /** above：值达到或超过阈值时升级；below：值达到或低于阈值时升级 */
  direction: 'above' | 'below'
}

/** 所有模板组件的统一 props */
export interface TemplateProps {
  widget: ResolvedWidget
  data: InstanceDataMap
  /** manifest 默认阈值（M1 尚无来源，预留给 M4 告警默认规则） */
  defaultThreshold?: Threshold
}

export type Lang = 'zh' | 'en'

/** 屏幕端环境：服务器时间提供者、全局时区、界面语言（Ruling 33，时钟不得用浏览器本地时区） */
export interface ScreenEnv {
  now: () => number
  timezone: string
  lang: Lang
}
