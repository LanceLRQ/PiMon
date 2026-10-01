import { beforeEach, describe, expect, it } from 'vitest'
import { applySettingsLanguage, createI18n, detectLanguage, languageStorageKey, setLanguage } from './index'
import zh from './zh.json'
import en from './en.json'

function flatten(obj: Record<string, unknown>, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) =>
    typeof v === 'object' && v !== null
      ? flatten(v as Record<string, unknown>, `${prefix}${k}.`)
      : [`${prefix}${k}`],
  )
}

describe('i18n', () => {
  it('中文与英文都能解析应用名', async () => {
    const zhI18n = await createI18n('zh')
    const enI18n = await createI18n('en')
    expect(zhI18n.t('app.name')).toBe('PiMon')
    expect(enI18n.t('app.name')).toBe('PiMon')
    expect(zhI18n.t('app.tagline')).not.toBe(enI18n.t('app.tagline'))
  })

  it('zh.json 与 en.json 的键集合一致', () => {
    expect(flatten(zh).sort()).toEqual(flatten(en).sort())
  })

  it('按浏览器语言选择，非中文回退英文', () => {
    expect(detectLanguage('zh-CN')).toBe('zh')
    expect(detectLanguage('zh-TW')).toBe('zh')
    expect(detectLanguage('en-US')).toBe('en')
    expect(detectLanguage('fr')).toBe('en')
  })

  describe('语言来源', () => {
    beforeEach(() => localStorage.clear())

    it('侧栏切换写入 localStorage 并更新文档语言', async () => {
      const i18n = await createI18n('zh')
      await setLanguage(i18n, 'en')
      expect(i18n.language).toBe('en')
      expect(localStorage.getItem(languageStorageKey)).toBe('en')
      expect(document.documentElement.lang).toBe('en')
    })

    it('全局设置的语言仅在本浏览器没有覆盖时生效', async () => {
      const i18n = await createI18n('zh')
      await applySettingsLanguage(i18n, 'en')
      expect(i18n.language).toBe('en')

      const other = await createI18n('zh')
      localStorage.setItem(languageStorageKey, 'zh')
      await applySettingsLanguage(other, 'en')
      expect(other.language).toBe('zh')
    })

    it('设置里的语言非法时忽略', async () => {
      const i18n = await createI18n('zh')
      await applySettingsLanguage(i18n, 'fr')
      expect(i18n.language).toBe('zh')
    })
  })
})
