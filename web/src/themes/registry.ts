import { ambient } from './ambient/theme'
import { industrial } from './industrial/theme'
import { missionControl } from './mission-control/theme'
import type { ThemeId, ThemeProfile } from './types'

/** 内置主题，顺序即主题选择器的展示顺序 */
export const themes: readonly ThemeProfile[] = [ambient, missionControl, industrial]

export const DEFAULT_THEME_ID: ThemeId = 'ambient'

export function isThemeId(value: unknown): value is ThemeId {
  return themes.some((t) => t.id === value)
}

/** 取主题档案；未知 id 回退到默认主题 */
export function getTheme(id: string | null | undefined): ThemeProfile {
  return themes.find((t) => t.id === id) ?? themes[0]
}
