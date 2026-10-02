// 设计 10.4 的能力档案与状态通道取值
export type ThemeId = 'ambient' | 'mission-control' | 'industrial'

export type ThemeTone = 'dark' | 'light'
export type ThemePalette = 'full' | 'duotone' | 'mono'
export type ThemeRadius = 'sharp' | 'soft' | 'round'
export type ThemeDensity = 'airy' | 'regular' | 'compact'
export type ThemeEffect = 'glow'
export type FontCategory = 'sans' | 'mono' | 'serif'

export interface ThemeProfile {
  id: ThemeId
  name: { zh: string; en: string }
  /** 名称的 i18n 键，供管理端时段计划页与主题选择器使用 */
  nameKey: string
  tone: ThemeTone
  palette: ThemePalette
  radius: ThemeRadius
  density: ThemeDensity
  /** 设置「降低特效」时逐项关闭 */
  effects: ThemeEffect[]
  fonts: { display: FontCategory; numeric: FontCategory; body: FontCategory }
}

export const statusLevels = ['ok', 'warning', 'critical', 'unknown', 'error'] as const
export type StatusLevel = (typeof statusLevels)[number]

export const statusMarkers = ['dot', 'ring', 'square', 'triangle', 'diamond'] as const
export type StatusMarker = (typeof statusMarkers)[number]

export const statusWeights = ['normal', 'strong', 'invert'] as const
export type StatusWeight = (typeof statusWeights)[number]
