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

function syncDocumentLanguage(lng: Language) {
  document.documentElement.lang = lng === 'zh' ? 'zh-CN' : 'en'
}

// 本浏览器的语言覆盖：存 localStorage，只影响当前浏览器；存储不可用时仅本次生效
export async function setLanguage(instance: i18n, lng: Language) {
  try {
    localStorage.setItem(languageStorageKey, lng)
  } catch {
    // 忽略
  }
  await instance.changeLanguage(lng)
  syncDocumentLanguage(lng)
}

// 全局设置里的语言只在本浏览器没有覆盖时生效
export async function applySettingsLanguage(instance: i18n, settingsLanguage: string) {
  if (storedLanguage() !== null) return
  if (settingsLanguage !== 'zh' && settingsLanguage !== 'en') return
  if (instance.language === settingsLanguage) return
  await instance.changeLanguage(settingsLanguage)
  syncDocumentLanguage(settingsLanguage)
}

// 登录前与首次设置页没有设置可读，按浏览器语言显示
export async function initI18n(): Promise<i18n> {
  const lng = storedLanguage() ?? detectLanguage(navigator.language)
  const instance = await createI18n(lng)
  syncDocumentLanguage(lng)
  return instance
}
