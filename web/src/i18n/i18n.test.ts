import { describe, expect, it } from 'vitest'
import { createI18n, detectLanguage } from './index'
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
})
