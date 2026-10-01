import { DEFAULT_THEME_ID, isThemeId, type ThemeId } from '@/themes'

// 屏幕主题应用到根元素。管理端的 dark 类不属于屏幕端，这里不碰；
// 首帧由 index.html 的内联脚本按 localStorage 设置 data-theme（防闪烁，Ruling 11），
// 收到 snapshot 后以服务端为准并写回 localStorage。

export const screenThemeStorageKey = 'pimon.screen.theme'

export function readStoredScreenTheme(): ThemeId {
  try {
    const v = localStorage.getItem(screenThemeStorageKey)
    return isThemeId(v) ? v : DEFAULT_THEME_ID
  } catch {
    return DEFAULT_THEME_ID
  }
}

export function persistScreenTheme(themeId: string) {
  try {
    localStorage.setItem(screenThemeStorageKey, themeId)
  } catch {
    // 存储不可用时只影响下次首帧防闪烁
  }
}

/** data-theme 与 data-reduce-effects 必须在同一元素上（降低特效的 CSS 选择器要求） */
export function applyScreenTheme(themeId: string, reduceEffects: boolean, root: HTMLElement = document.documentElement) {
  root.setAttribute('data-theme', isThemeId(themeId) ? themeId : DEFAULT_THEME_ID)
  if (reduceEffects) root.setAttribute('data-reduce-effects', '')
  else root.removeAttribute('data-reduce-effects')
}
