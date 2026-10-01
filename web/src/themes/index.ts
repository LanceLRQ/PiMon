// 主题系统对外入口。样式由使用方单独 import '@/themes/index.css'（屏幕根、编辑器画布），不进管理端主包。
export { themes, DEFAULT_THEME_ID, getTheme, isThemeId } from './registry'
export { readThemeRuntime, watchThemeRuntime } from './runtime'
export type { ThemeRuntime, StatusChannel } from './runtime'
export * from './types'
