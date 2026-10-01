import i18next, { type i18n } from 'i18next'
import { initReactI18next } from 'react-i18next'
import zh from './zh.json'
import en from './en.json'

export type Language = 'zh' | 'en'

export const languageStorageKey = 'pimon.admin.lang'

// 浏览器语言以 zh 开头用中文，其余回退英文
export function detectLanguage(browserLanguage: string): Language {
  return browserLanguage.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function storedLanguage(): Language | null {
  try {
    const v = localStorage.getItem(languageStorageKey)
    return v === 'zh' || v === 'en' ? v : null
  } catch {
    return null
  }
}

// 创建独立实例，便于测试；应用本身使用下方的全局实例
export async function createI18n(lng: Language): Promise<i18n> {
  const instance = i18next.createInstance()
  await instance.use(initReactI18next).init({
    lng,
    fallbackLng: 'en',
    resources: { zh: { translation: zh }, en: { translation: en } },
    interpolation: { escapeValue: false },
  })
  return instance
}

export async function initI18n(): Promise<i18n> {
  const lng = storedLanguage() ?? detectLanguage(navigator.language)
  const instance = await createI18n(lng)
  document.documentElement.lang = lng === 'zh' ? 'zh-CN' : 'en'
  return instance
}
