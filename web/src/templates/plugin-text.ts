import type { i18n as I18n } from 'i18next'
import { useTranslation } from 'react-i18next'

// 只有形如 identifier.path 的文本才当作 key 尝试查找，其余（含空格、冒号的自由文本）原样显示
const keyLike = /^[A-Za-z0-9_][A-Za-z0-9_-]*(\.[A-Za-z0-9_-]+)*$/

/** Ruling 34：先查 plugin.<id>.<key>，查不到原样显示 */
export function pluginText(i18n: I18n, pluginId: string | undefined, text: string): string {
  if (!pluginId || !text || !keyLike.test(text)) return text
  const key = `plugin.${pluginId}.${text}`
  return i18n.exists(key) ? (i18n.t(key) as string) : text
}

export function usePluginText(pluginId: string | undefined): (text: string) => string {
  const { i18n } = useTranslation()
  return (text) => pluginText(i18n, pluginId, text)
}
